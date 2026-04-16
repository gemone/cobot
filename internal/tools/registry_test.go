package tools

import (
	"context"
	"encoding/json"
	"testing"

	cobot "github.com/cobot-agent/cobot/pkg"
)

type mockTool struct {
	name        string
	description string
	parameters  json.RawMessage
	executeFn   func(ctx context.Context, args json.RawMessage) (string, error)
}

func (m *mockTool) Name() string                { return m.name }
func (m *mockTool) Description() string         { return m.description }
func (m *mockTool) Parameters() json.RawMessage { return m.parameters }
func (m *mockTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	if m.executeFn != nil {
		return m.executeFn(ctx, args)
	}
	return "", nil
}

func TestRegistryRegisterAndGet(t *testing.T) {
	r := NewRegistry()
	tool := &mockTool{
		name:        "echo",
		description: "echoes input",
		parameters:  json.RawMessage(`{"type":"object"}`),
	}
	r.Register(tool)

	got, err := r.Get("echo")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.Name() != "echo" {
		t.Errorf("expected tool name %q, got %q", "echo", got.Name())
	}
}

func TestRegistryList(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockTool{name: "tool_a", description: "first", parameters: json.RawMessage(`{"type":"object"}`)})
	r.Register(&mockTool{name: "tool_b", description: "second", parameters: json.RawMessage(`{"type":"object"}`)})

	defs := r.ToolDefs()
	if len(defs) != 2 {
		t.Fatalf("expected 2 tool defs, got %d", len(defs))
	}

	names := map[string]bool{}
	for _, d := range defs {
		names[d.Name] = true
		if d.Name == "tool_a" && d.Description != "first" {
			t.Errorf("expected description %q, got %q", "first", d.Description)
		}
		if d.Name == "tool_b" && d.Description != "second" {
			t.Errorf("expected description %q, got %q", "second", d.Description)
		}
	}
	if !names["tool_a"] || !names["tool_b"] {
		t.Error("missing expected tools in ToolDefs")
	}
}

func TestRegistryGetNotFound(t *testing.T) {
	r := NewRegistry()
	_, err := r.Get("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent tool")
	}
}

func TestRegistry_Without(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockTool{name: "keep", description: "keep me", parameters: json.RawMessage(`{"type":"object"}`)})
	r.Register(&mockTool{name: "drop", description: "drop me", parameters: json.RawMessage(`{"type":"object"}`)})

	filtered := r.Without("drop")
	if _, err := filtered.Get("keep"); err != nil {
		t.Fatalf("expected keep to exist: %v", err)
	}
	if _, err := filtered.Get("drop"); err == nil {
		t.Fatal("expected drop to be removed")
	}
}

func TestExecuteTool(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockTool{
		name:        "greet",
		description: "says hello",
		parameters:  json.RawMessage(`{"type":"object"}`),
		executeFn: func(_ context.Context, args json.RawMessage) (string, error) {
			return "hello", nil
		},
	})

	result, err := r.Execute(context.Background(), cobot.ToolCall{
		ID:   "call-1",
		Name: "greet",
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Output != "hello" {
		t.Errorf("expected output %q, got %q", "hello", result.Output)
	}
	if result.CallID != "call-1" {
		t.Errorf("expected call_id %q, got %q", "call-1", result.CallID)
	}
}
