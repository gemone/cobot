package openai

import (
	"strings"
	"testing"

	"github.com/cobot-agent/cobot/internal/llm/base"
	cobot "github.com/cobot-agent/cobot/pkg"
)

func TestReadStreamSingleToolCall(t *testing.T) {
	sseData := strings.Join([]string{
		`data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_abc","type":"function","function":{"name":"get_weather","arguments":""}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"ci"}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"ty\":"}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"SF\"}"}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		"data: [DONE]",
	}, "\n") + "\n"

	sse := base.NewSSEScanner(strings.NewReader(sseData))
	ch := make(chan cobot.ProviderChunk, 64)

	p := &Provider{}
	go func() {
		defer close(ch)
		p.readStream(sse, ch)
	}()

	var chunks []cobot.ProviderChunk
	for c := range ch {
		chunks = append(chunks, c)
	}

	doneIdx := -1
	var toolCallChunks []cobot.ProviderChunk
	for i, c := range chunks {
		if c.Done {
			doneIdx = i
		}
		if c.ToolCall != nil {
			toolCallChunks = append(toolCallChunks, c)
		}
	}

	if doneIdx < 0 {
		t.Fatal("expected a Done chunk")
	}

	var firstToolCallIdx int
	foundToolCall := false
	for i, c := range chunks {
		if c.ToolCall != nil && !foundToolCall {
			firstToolCallIdx = i
			foundToolCall = true
			break
		}
	}
	if !foundToolCall {
		t.Fatal("expected at least one tool call chunk")
	}
	if firstToolCallIdx >= doneIdx {
		t.Errorf("first tool call at index %d must come before Done at index %d", firstToolCallIdx, doneIdx)
	}

	if len(toolCallChunks) != 1 {
		t.Fatalf("expected exactly 1 assembled tool call chunk, got %d", len(toolCallChunks))
	}

	tc := toolCallChunks[0].ToolCall
	if tc.ID != "call_abc" {
		t.Errorf("expected ID call_abc, got %s", tc.ID)
	}
	if tc.Name != "get_weather" {
		t.Errorf("expected Name get_weather, got %s", tc.Name)
	}
	if string(tc.Arguments) != `{"city":"SF"}` {
		t.Errorf("expected assembled arguments {\"city\":\"SF\"}, got %s", string(tc.Arguments))
	}
}

func TestReadStreamMultipleToolCalls(t *testing.T) {
	sseData := strings.Join([]string{
		`data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":""}},{"index":1,"id":"call_2","type":"function","function":{"name":"get_time","arguments":""}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":\"SF\"}"}},{"index":1,"function":{"arguments":"{\"tz\":\"PST\"}"}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		"data: [DONE]",
	}, "\n") + "\n"

	sse := base.NewSSEScanner(strings.NewReader(sseData))
	ch := make(chan cobot.ProviderChunk, 64)

	p := &Provider{}
	go func() {
		defer close(ch)
		p.readStream(sse, ch)
	}()

	var toolCallChunks []cobot.ProviderChunk
	for c := range ch {
		if c.ToolCall != nil {
			toolCallChunks = append(toolCallChunks, c)
		}
	}

	if len(toolCallChunks) != 2 {
		t.Fatalf("expected 2 assembled tool calls, got %d", len(toolCallChunks))
	}

	tc0 := toolCallChunks[0].ToolCall
	if tc0.ID != "call_1" || tc0.Name != "get_weather" {
		t.Errorf("first tool call: expected call_1/get_weather, got %s/%s", tc0.ID, tc0.Name)
	}
	if string(tc0.Arguments) != `{"city":"SF"}` {
		t.Errorf("first tool call arguments: got %s", string(tc0.Arguments))
	}

	tc1 := toolCallChunks[1].ToolCall
	if tc1.ID != "call_2" || tc1.Name != "get_time" {
		t.Errorf("second tool call: expected call_2/get_time, got %s/%s", tc1.ID, tc1.Name)
	}
	if string(tc1.Arguments) != `{"tz":"PST"}` {
		t.Errorf("second tool call arguments: got %s", string(tc1.Arguments))
	}
}

func TestReadStreamTextOnly(t *testing.T) {
	sseData := strings.Join([]string{
		`data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		"data: [DONE]",
	}, "\n") + "\n"

	sse := base.NewSSEScanner(strings.NewReader(sseData))
	ch := make(chan cobot.ProviderChunk, 64)

	p := &Provider{}
	go func() {
		defer close(ch)
		p.readStream(sse, ch)
	}()

	var content string
	var gotDone bool
	var toolCallCount int
	for c := range ch {
		content += c.Content
		if c.Done {
			gotDone = true
		}
		if c.ToolCall != nil {
			toolCallCount++
		}
	}

	if !gotDone {
		t.Error("expected Done chunk")
	}
	if content != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", content)
	}
	if toolCallCount != 0 {
		t.Errorf("expected 0 tool calls, got %d", toolCallCount)
	}
}
