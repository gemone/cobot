package openai

import (
	"encoding/json"
	"testing"

	cobot "github.com/cobot-agent/cobot/pkg"
)

func TestNewProviderDefaultBaseURL(t *testing.T) {
	p := NewProvider("key", "", nil)
	if p.cfg.BaseURL != "https://api.openai.com/v1" {
		t.Errorf("expected default base URL, got %s", p.cfg.BaseURL)
	}
}

func TestNewProviderCustomBaseURL(t *testing.T) {
	p := NewProvider("key", "https://custom.api.com/v1", nil)
	if p.cfg.BaseURL != "https://custom.api.com/v1" {
		t.Errorf("expected custom base URL, got %s", p.cfg.BaseURL)
	}
}

func TestNewProviderTrailingSlash(t *testing.T) {
	p := NewProvider("key", "https://custom.api.com/v1/", nil)
	if p.cfg.BaseURL != "https://custom.api.com/v1" {
		t.Errorf("expected trimmed base URL, got %s", p.cfg.BaseURL)
	}
}

func TestProviderName(t *testing.T) {
	p := NewProvider("key", "", nil)
	if p.Name() != ProviderName {
		t.Errorf("expected name %s, got %s", ProviderName, p.Name())
	}
}

func TestToProviderResponseToolCalls(t *testing.T) {
	resp := &chatResponse{
		Choices: []chatChoice{
			{
				Message: chatMessage{
					Content: nil, // assistant with tool_calls has nil content
					ToolCalls: []chatToolCall{
						{
							ID:   "call_123",
							Type: "function",
							Function: chatFuncCall{
								Name:      "get_weather",
								Arguments: `{"city":"SF"}`,
							},
						},
					},
				},
				FinishReason: "tool_calls",
			},
		},
		Usage: chatUsage{
			PromptTokens:     10,
			CompletionTokens: 20,
			TotalTokens:      30,
		},
	}

	result := toProviderResponse(resp)
	if result.StopReason != cobot.StopEndTurn {
		t.Errorf("expected stop_end_turn, got %s", result.StopReason)
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	tc := result.ToolCalls[0]
	if tc.ID != "call_123" {
		t.Errorf("expected call_123, got %s", tc.ID)
	}
	if tc.Name != "get_weather" {
		t.Errorf("expected get_weather, got %s", tc.Name)
	}
	if string(tc.Arguments) != `{"city":"SF"}` {
		t.Errorf("unexpected arguments: %s", string(tc.Arguments))
	}
	if result.Usage.TotalTokens != 30 {
		t.Errorf("expected 30 total tokens, got %d", result.Usage.TotalTokens)
	}
}

func TestToProviderResponseStopReasons(t *testing.T) {
	tests := []struct {
		finish string
		want   cobot.StopReason
	}{
		{"stop", cobot.StopEndTurn},
		{"length", cobot.StopMaxTokens},
		{"tool_calls", cobot.StopEndTurn},
		{"unknown", cobot.StopEndTurn},
	}
	for _, tt := range tests {
		resp := &chatResponse{
			Choices: []chatChoice{
				{FinishReason: tt.finish},
			},
		}
		result := toProviderResponse(resp)
		if result.StopReason != tt.want {
			t.Errorf("finish_reason=%q: got %s, want %s", tt.finish, result.StopReason, tt.want)
		}
	}
}

func TestFromProviderMessages(t *testing.T) {
	msgs := []cobot.Message{
		{Role: cobot.RoleSystem, Content: "You are helpful."},
		{Role: cobot.RoleUser, Content: "Hello"},
		{Role: cobot.RoleAssistant, Content: "Hi!", ToolCalls: []cobot.ToolCall{
			{ID: "call_1", Name: "search", Arguments: json.RawMessage(`{"q":"test"}`)},
		}},
		{Role: cobot.RoleTool, Content: "", ToolResult: &cobot.ToolResult{CallID: "call_1", Output: "result data"}},
	}

	result := fromProviderMessages(msgs)
	if len(result) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(result))
	}

	if result[0].Role != "system" || derefString(result[0].Content) != "You are helpful." {
		t.Errorf("system message not converted correctly")
	}
	if result[1].Role != "user" || derefString(result[1].Content) != "Hello" {
		t.Errorf("user message not converted correctly")
	}
	if result[2].Role != "assistant" {
		t.Errorf("assistant role not converted correctly")
	}
	if result[2].Content != nil {
		t.Errorf("assistant with tool_calls should have nil content, got %q", derefString(result[2].Content))
	}
	if len(result[2].ToolCalls) != 1 || result[2].ToolCalls[0].ID != "call_1" {
		t.Errorf("assistant tool calls not converted correctly")
	}
	if result[3].ToolCallID != "call_1" || derefString(result[3].Content) != "result data" {
		t.Errorf("tool result message not converted correctly")
	}
}

func TestFromProviderTools(t *testing.T) {
	tools := []cobot.ToolDef{
		{
			Name:        "search",
			Description: "Search the web",
			Parameters:  json.RawMessage(`{"type":"object"}`),
		},
	}

	result := fromProviderTools(tools)
	if len(result) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(result))
	}
	if result[0].Type != "function" {
		t.Errorf("expected function type, got %s", result[0].Type)
	}
	if result[0].Function.Name != "search" {
		t.Errorf("expected search, got %s", result[0].Function.Name)
	}
	if result[0].Function.Description != "Search the web" {
		t.Errorf("description not converted correctly")
	}
	if string(result[0].Function.Parameters) != `{"type":"object"}` {
		t.Errorf("parameters not converted correctly")
	}
}

func TestToProviderResponseEmpty(t *testing.T) {
	resp := &chatResponse{}
	result := toProviderResponse(resp)
	if result == nil {
		t.Fatal("expected non-nil response")
	}
	if result.Content != "" {
		t.Errorf("expected empty content, got %s", result.Content)
	}
}
