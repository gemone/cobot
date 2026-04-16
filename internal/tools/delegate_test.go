package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/cobot-agent/cobot/internal/workspace"
	cobot "github.com/cobot-agent/cobot/pkg"
)

type mockSubAgent struct {
	response     *cobot.ProviderResponse
	err          error
	modelErr     error
	systemPrompt string
	model        string
}

func (m *mockSubAgent) SetModel(spec string) error {
	m.model = spec
	return m.modelErr
}

func (m *mockSubAgent) SetSystemPrompt(prompt string) error {
	m.systemPrompt = prompt
	return nil
}

func (m *mockSubAgent) Prompt(_ context.Context, _ string) (*cobot.ProviderResponse, error) {
	return m.response, m.err
}

func (m *mockSubAgent) Stream(_ context.Context, _ string) (<-chan cobot.Event, error) {
	return nil, fmt.Errorf("not implemented")
}

func TestDelegateTool_Name(t *testing.T) {
	dt := NewDelegateTool(nil)
	if dt.Name() != "delegate_task" {
		t.Errorf("Name() = %q, want %q", dt.Name(), "delegate_task")
	}
}

func TestDelegateTool_ExecuteBasic(t *testing.T) {
	dt := NewDelegateTool(func() cobot.SubAgent {
		return &mockSubAgent{response: &cobot.ProviderResponse{Content: "sub-agent result", StopReason: cobot.StopEndTurn}}
	})

	args, _ := json.Marshal(map[string]string{"prompt": "do something"})
	result, err := dt.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if result != "sub-agent result" {
		t.Errorf("Execute() = %q, want %q", result, "sub-agent result")
	}
}

func TestDelegateTool_ExecuteWithModel(t *testing.T) {
	dt := NewDelegateTool(func() cobot.SubAgent {
		return &mockSubAgent{response: &cobot.ProviderResponse{Content: "model result", StopReason: cobot.StopEndTurn}}
	})

	args, _ := json.Marshal(map[string]string{
		"prompt": "do something",
		"model":  "openai:gpt-4o-mini",
	})
	result, err := dt.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if result != "model result" {
		t.Errorf("Execute() = %q, want %q", result, "model result")
	}
}

func TestDelegateTool_ExecuteEmptyPrompt(t *testing.T) {
	dt := NewDelegateTool(func() cobot.SubAgent {
		return &mockSubAgent{}
	})

	args, _ := json.Marshal(map[string]string{"prompt": ""})
	_, err := dt.Execute(context.Background(), args)
	if err == nil {
		t.Error("expected error for empty prompt")
	}
}

func TestDelegateTool_ExecuteMissingPrompt(t *testing.T) {
	dt := NewDelegateTool(func() cobot.SubAgent {
		return &mockSubAgent{}
	})

	args, _ := json.Marshal(map[string]string{})
	_, err := dt.Execute(context.Background(), args)
	if err == nil {
		t.Error("expected error for missing prompt")
	}
}

func TestDelegateTool_ExecuteInvalidJSON(t *testing.T) {
	dt := NewDelegateTool(func() cobot.SubAgent {
		return &mockSubAgent{}
	})

	_, err := dt.Execute(context.Background(), json.RawMessage(`not json`))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestDelegateTool_ExecuteModelResolutionError(t *testing.T) {
	dt := NewDelegateTool(func() cobot.SubAgent {
		return &mockSubAgent{modelErr: fmt.Errorf("bad model")}
	})

	args, _ := json.Marshal(map[string]string{
		"prompt": "do something",
		"model":  "bad:model",
	})
	_, err := dt.Execute(context.Background(), args)
	if err == nil {
		t.Error("expected error for bad model resolution")
	}
}

func TestDelegateTool_ExecuteExternalAgentViaWorkspace(t *testing.T) {
	ws := &workspace.Workspace{
		Config: &workspace.WorkspaceConfig{
			ExternalAgents: []cobot.ExternalAgentConfig{
				{
					Name:    "myagent",
					Command: "echo",
					Args:    []string{"hello"},
					Workdir: "/tmp/ws",
					Timeout: "1s",
				},
			},
		},
	}

	dt := NewDelegateTool(nil, WithDelegateWorkdir("/tmp/test"), WithDelegateAgentLookup(ws))

	args, _ := json.Marshal(map[string]any{
		"prompt":     "do something",
		"agent_type": "myagent",
	})
	params, err := dt.parseParams(args)
	if err != nil {
		t.Fatalf("parseParams() error: %v", err)
	}
	if params.AgentType != "myagent" {
		t.Errorf("AgentType = %q, want myagent", params.AgentType)
	}

	sub, err := dt.setupSubAgent(params)
	if err != nil {
		t.Fatalf("setupSubAgent() error: %v", err)
	}
	acp, ok := sub.(*ACPSubAgent)
	if !ok {
		t.Fatalf("setupSubAgent() returned %T, want *ACPSubAgent", sub)
	}
	if acp.command != "echo" {
		t.Errorf("ACP command = %q, want echo", acp.command)
	}
	if acp.workdir != "/tmp/ws" {
		t.Errorf("ACP workdir = %q, want /tmp/ws", acp.workdir)
	}
}

func TestDelegateTool_ExecuteExternalAgentNotFound(t *testing.T) {
	ws := &workspace.Workspace{
		Config: &workspace.WorkspaceConfig{
			ExternalAgents: []cobot.ExternalAgentConfig{},
		},
	}

	dt := NewDelegateTool(nil, WithDelegateWorkdir("/tmp/test"), WithDelegateAgentLookup(ws))

	args, _ := json.Marshal(map[string]any{
		"prompt":     "do something",
		"agent_type": "unknown",
	})
	params, err := dt.parseParams(args)
	if err != nil {
		t.Fatalf("parseParams() error: %v", err)
	}

	_, err = dt.setupSubAgent(params)
	if err == nil {
		t.Fatal("expected error for unknown external agent")
	}
}

func TestDelegateTool_ExecuteExternalAgentNoWorkspace(t *testing.T) {
	dt := NewDelegateTool(nil, WithDelegateWorkdir("/tmp/test"))

	args, _ := json.Marshal(map[string]any{
		"prompt":     "do something",
		"agent_type": "myagent",
	})
	params, err := dt.parseParams(args)
	if err != nil {
		t.Fatalf("parseParams() error: %v", err)
	}

	_, err = dt.setupSubAgent(params)
	if err == nil {
		t.Fatal("expected error when no workspace is configured")
	}
}

func TestDelegateTool_InternalSetsSystemPrompt(t *testing.T) {
	var sub *mockSubAgent
	dt := NewDelegateTool(func() cobot.SubAgent {
		sub = &mockSubAgent{response: &cobot.ProviderResponse{Content: "ok", StopReason: cobot.StopEndTurn}}
		return sub
	})

	args, _ := json.Marshal(map[string]string{"prompt": "do something"})
	_, err := dt.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if sub.systemPrompt != cobot.DefaultSubAgentSystemPrompt {
		t.Errorf("system prompt = %q, want %q", sub.systemPrompt, cobot.DefaultSubAgentSystemPrompt)
	}
}
