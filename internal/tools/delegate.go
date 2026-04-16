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

type DelegateTool struct {
	factory SubAgentFactory
}

func NewDelegateTool(factory SubAgentFactory) *DelegateTool {
	return &DelegateTool{factory: factory}
}

func (t *DelegateTool) Name() string { return "delegate_task" }

func (t *DelegateTool) Description() string {
	return `Delegate a subtask to a sub-agent. The sub-agent runs autonomously and returns its result. Use for: complex subtasks, parallel work, isolated research. Parameters: prompt (required) - what the sub-agent should do; model (optional) - override model only if a different model is explicitly needed, otherwise omit to inherit the current model.`
}

func (t *DelegateTool) Parameters() json.RawMessage {
	return json.RawMessage(delegateTaskParamsJSON)
}

func (t *DelegateTool) parseParams(args json.RawMessage) (prompt, model string, err error) {
	var params struct {
		Prompt string `json:"prompt"`
		Model  string `json:"model"`
	}
	if err := decodeArgs(args, &params); err != nil {
		return "", "", err
	}
	if params.Prompt == "" {
		return "", "", fmt.Errorf("prompt is required")
	}
	return params.Prompt, params.Model, nil
}

func (t *DelegateTool) setupSubAgent(model string) (cobot.SubAgent, error) {
	sub := t.factory()
	if model != "" {
		if err := sub.SetModel(model); err != nil {
			return nil, fmt.Errorf("set model: %w", err)
		}
	}
	return sub, nil
}

func (t *DelegateTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	prompt, model, err := t.parseParams(args)
	if err != nil {
		return "", err
	}
	sub, err := t.setupSubAgent(model)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, delegateTimeout)
	defer cancel()
	resp, err := sub.Prompt(ctx, prompt)
	if err != nil {
		return "", fmt.Errorf("sub-agent error: %w", err)
	}
	return resp.Content, nil
}

func (t *DelegateTool) ExecuteStream(ctx context.Context, args json.RawMessage, eventCh chan<- cobot.Event) (string, error) {
	prompt, model, err := t.parseParams(args)
	if err != nil {
		return "", err
	}
	sub, err := t.setupSubAgent(model)
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(ctx, delegateTimeout)
	defer cancel()

	streamCh, err := sub.Stream(ctx, prompt)
	if err != nil {
		return "", fmt.Errorf("sub-agent stream error: %w", err)
	}

	var result strings.Builder
	for evt := range streamCh {
		switch evt.Type {
		case cobot.EventText:
			result.WriteString(evt.Content)
			select {
			case eventCh <- evt:
			case <-ctx.Done():
				return result.String(), ctx.Err()
			}
		case cobot.EventToolCall, cobot.EventToolResult, cobot.EventToolStart:
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
	return result.String(), nil
}
