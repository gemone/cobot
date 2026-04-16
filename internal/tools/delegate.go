package tools

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	cobot "github.com/cobot-agent/cobot/pkg"
)

//go:embed embed_delegate_task_params.json
var delegateTaskParamsJSON []byte

const delegateTimeout = 10 * time.Minute

type SubAgentFactory func() cobot.SubAgent

var _ cobot.StreamingTool = (*DelegateTool)(nil)

type ExternalAgentLookup interface {
	ExternalAgent(name string) (*cobot.ExternalAgentConfig, bool)
}

type DelegateTool struct {
	factory     SubAgentFactory
	workdir     string
	agentLookup ExternalAgentLookup // to lookup external agent configs
	sandbox     *cobot.SandboxConfig
}

func WithDelegateWorkdir(workdir string) func(*DelegateTool) {
	return func(t *DelegateTool) { t.workdir = workdir }
}

func WithDelegateAgentLookup(l ExternalAgentLookup) func(*DelegateTool) {
	return func(t *DelegateTool) { t.agentLookup = l }
}

func WithDelegateSandbox(s *cobot.SandboxConfig) func(*DelegateTool) {
	return func(t *DelegateTool) { t.sandbox = s }
}

func NewDelegateTool(factory SubAgentFactory, opts ...func(*DelegateTool)) *DelegateTool {
	t := &DelegateTool{factory: factory}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

func (t *DelegateTool) Name() string { return "delegate_task" }

func (t *DelegateTool) Description() string {
	return `Delegate a subtask to a sub-agent. The sub-agent runs autonomously and returns its result. Use for: complex subtasks, parallel work, isolated research. Parameters: prompt (required) - what the sub-agent should do; model (optional) - override model only if a different model is explicitly needed, otherwise omit to inherit the current model; agent_type (optional) - internal (default) or the name of a configured external agent.`
}

func (t *DelegateTool) Parameters() json.RawMessage {
	return json.RawMessage(delegateTaskParamsJSON)
}

type delegateParams struct {
	Prompt    string `json:"prompt"`
	Model     string `json:"model"`
	AgentType string `json:"agent_type"`
}

func (t *DelegateTool) parseParams(args json.RawMessage) (delegateParams, error) {
	var params delegateParams
	if err := decodeArgs(args, &params); err != nil {
		return delegateParams{}, err
	}
	if params.Prompt == "" {
		return delegateParams{}, fmt.Errorf("prompt is required")
	}
	return params, nil
}

func (t *DelegateTool) setupSubAgent(params delegateParams) (cobot.SubAgent, error) {
	if params.AgentType != "" && params.AgentType != "internal" {
		if t.agentLookup == nil {
			return nil, fmt.Errorf("external agent %q not found: no workspace configured", params.AgentType)
		}
		cfg, ok := t.agentLookup.ExternalAgent(params.AgentType)
		if !ok {
			return nil, fmt.Errorf("external agent %q not found in workspace config", params.AgentType)
		}
		if cfg.Command == "" {
			return nil, fmt.Errorf("external agent %q has no command configured", params.AgentType)
		}
		cmd := cfg.Command
		args := cfg.Args
		if len(args) == 0 {
			args = []string{"acp", "--port", "0"}
		}
		timeout := delegateTimeout
		if cfg.Timeout != "" {
			d, err := time.ParseDuration(cfg.Timeout)
			if err != nil {
				return nil, fmt.Errorf("invalid timeout %q: %w", cfg.Timeout, err)
			}
			timeout = d
		}
		wd := cfg.Workdir
		if wd == "" {
			wd = t.workdir
		}
		acp := NewACPSubAgent(cmd, args, wd, timeout)
		if params.Model != "" {
			if err := acp.SetModel(params.Model); err != nil {
				return nil, fmt.Errorf("set model: %w", err)
			}
		}
		if err := acp.SetSystemPrompt(cobot.DefaultSubAgentSystemPrompt); err != nil {
			return nil, fmt.Errorf("set system prompt: %w", err)
		}
		return acp, nil
	}

	sub := t.factory()
	if params.Model != "" {
		if err := sub.SetModel(params.Model); err != nil {
			return nil, fmt.Errorf("set model: %w", err)
		}
	}
	if err := sub.SetSystemPrompt(cobot.DefaultSubAgentSystemPrompt); err != nil {
		return nil, fmt.Errorf("set system prompt: %w", err)
	}
	return sub, nil
}

func (t *DelegateTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	params, err := t.parseParams(args)
	if err != nil {
		return "", err
	}
	sub, err := t.setupSubAgent(params)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, delegateTimeout)
	defer cancel()
	resp, err := sub.Prompt(ctx, params.Prompt)
	if err != nil {
		return "", fmt.Errorf("sub-agent error: %w", err)
	}
	content := resp.Content
	if t.sandbox != nil && t.sandbox.VirtualRoot != "" {
		content = t.sandbox.RewriteOutputPaths(content)
	}
	return content, nil
}

func (t *DelegateTool) ExecuteStream(ctx context.Context, args json.RawMessage, eventCh chan<- cobot.Event) (string, error) {
	params, err := t.parseParams(args)
	if err != nil {
		return "", err
	}
	sub, err := t.setupSubAgent(params)
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(ctx, delegateTimeout)
	defer cancel()

	streamCh, err := sub.Stream(ctx, params.Prompt)
	if err != nil {
		return "", fmt.Errorf("sub-agent stream error: %w", err)
	}

	var result strings.Builder
	for evt := range streamCh {
		switch evt.Type {
		case cobot.EventText:
			// Sanitize real paths in streaming events
			if t.sandbox != nil && t.sandbox.VirtualRoot != "" {
				evt.Content = t.sandbox.RewriteOutputPaths(evt.Content)
			}
			result.WriteString(evt.Content)
			select {
			case eventCh <- evt:
			case <-ctx.Done():
				return result.String(), ctx.Err()
			}
		case cobot.EventToolCall, cobot.EventToolResult, cobot.EventToolStart:
			if t.sandbox != nil && t.sandbox.VirtualRoot != "" && evt.Content != "" {
				evt.Content = t.sandbox.RewriteOutputPaths(evt.Content)
			}
			select {
			case eventCh <- evt:
			case <-ctx.Done():
				return result.String(), ctx.Err()
			}
		case cobot.EventError:
			return result.String(), fmt.Errorf("sub-agent error: %s", evt.Error)
		case cobot.EventDone:
			select {
			case eventCh <- evt:
			case <-ctx.Done():
				return result.String(), ctx.Err()
			}
		}
	}
	content := result.String()
	if t.sandbox != nil && t.sandbox.VirtualRoot != "" {
		content = t.sandbox.RewriteOutputPaths(content)
	}
	return content, nil
}
