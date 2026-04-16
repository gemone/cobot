package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	cobot "github.com/cobot-agent/cobot/pkg"
)

// mockSummaryProvider implements SummaryProvider for testing.
type mockSummaryProvider struct {
	// response is returned by Complete.
	response string
	// err is returned by Complete.
	err error
	// calls records every request sent.
	calls []*cobot.ProviderRequest
}

func (m *mockSummaryProvider) Complete(_ context.Context, req *cobot.ProviderRequest) (*cobot.ProviderResponse, error) {
	m.calls = append(m.calls, req)
	if m.err != nil {
		return nil, m.err
	}
	return &cobot.ProviderResponse{Content: m.response}, nil
}

func defaultCfg() cobot.SessionConfig {
	return cobot.SessionConfig{
		SummarizeThreshold: 0.5,
		CompressThreshold:  0.7,
		SummarizeTurns:     60,
	}
}

func makeMessages(n int) []cobot.Message {
	msgs := make([]cobot.Message, n)
	for i := range msgs {
		role := cobot.RoleUser
		if i%2 == 1 {
			role = cobot.RoleAssistant
		}
		msgs[i] = cobot.Message{Role: role, Content: fmt.Sprintf("message-%d", i)}
	}
	return msgs
}

// --- Check ---

func TestCompressor_Check_None(t *testing.T) {
	c := NewCompressor(defaultCfg(), 100_000, nil, "gpt-4o")
	usage := cobot.Usage{TotalTokens: 10_000} // 10% — below all thresholds
	got := c.Check(usage, 5)
	if got != CompressNone {
		t.Errorf("Check = %d, want CompressNone", got)
	}
}

func TestCompressor_Check_SummarizeByRatio(t *testing.T) {
	c := NewCompressor(defaultCfg(), 100_000, nil, "gpt-4o")
	usage := cobot.Usage{TotalTokens: 55_000} // 55% — above 50% summarize threshold
	got := c.Check(usage, 5)
	if got != CompressSummarize {
		t.Errorf("Check = %d, want CompressSummarize", got)
	}
}

func TestCompressor_Check_SummarizeByTurns(t *testing.T) {
	c := NewCompressor(defaultCfg(), 100_000, nil, "gpt-4o")
	usage := cobot.Usage{TotalTokens: 1_000} // low usage, but turns exceeded
	got := c.Check(usage, 60)
	if got != CompressSummarize {
		t.Errorf("Check = %d, want CompressSummarize", got)
	}
}

func TestCompressor_Check_CompressFull(t *testing.T) {
	c := NewCompressor(defaultCfg(), 100_000, nil, "gpt-4o")
	usage := cobot.Usage{TotalTokens: 75_000} // 75% — above 70% compress threshold
	got := c.Check(usage, 5)
	if got != CompressFull {
		t.Errorf("Check = %d, want CompressFull", got)
	}
}

func TestCompressor_Check_CompressBeats_Summarize(t *testing.T) {
	// When both thresholds are exceeded, CompressFull takes priority.
	c := NewCompressor(defaultCfg(), 100_000, nil, "gpt-4o")
	usage := cobot.Usage{TotalTokens: 80_000}
	got := c.Check(usage, 100) // also exceeds turn threshold
	if got != CompressFull {
		t.Errorf("Check = %d, want CompressFull (highest priority)", got)
	}
}

func TestCompressor_Check_ZeroContextWindow(t *testing.T) {
	c := NewCompressor(defaultCfg(), 0, nil, "gpt-4o")
	usage := cobot.Usage{TotalTokens: 999_999}
	got := c.Check(usage, 999)
	if got != CompressNone {
		t.Errorf("Check with zero context window = %d, want CompressNone", got)
	}
}

func TestCompressor_Check_DisabledThresholds(t *testing.T) {
	cfg := cobot.SessionConfig{
		SummarizeThreshold: 0,
		CompressThreshold:  0,
		SummarizeTurns:     0,
	}
	c := NewCompressor(cfg, 100_000, nil, "gpt-4o")
	usage := cobot.Usage{TotalTokens: 99_000}
	got := c.Check(usage, 999)
	if got != CompressNone {
		t.Errorf("Check with all thresholds disabled = %d, want CompressNone", got)
	}
}

// --- Summarize ---

func TestCompressor_Summarize_TooFewMessages(t *testing.T) {
	mp := &mockSummaryProvider{response: "should not be called"}
	c := NewCompressor(defaultCfg(), 100_000, mp, "gpt-4o")

	msgs := makeMessages(3) // less than 4 → no-op
	summary, kept, err := c.Summarize(context.Background(), msgs)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if summary != "" {
		t.Errorf("summary = %q, want empty", summary)
	}
	if len(kept) != 3 {
		t.Errorf("kept = %d messages, want 3 (all)", len(kept))
	}
	if len(mp.calls) != 0 {
		t.Errorf("provider called %d times, want 0", len(mp.calls))
	}
}

func TestCompressor_Summarize_SplitsMessages(t *testing.T) {
	mp := &mockSummaryProvider{response: "summary of older messages"}
	c := NewCompressor(defaultCfg(), 100_000, mp, "test-model")

	msgs := makeMessages(9) // keep 3 (9/3), summarize 6
	summary, kept, err := c.Summarize(context.Background(), msgs)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if summary != "summary of older messages" {
		t.Errorf("summary = %q", summary)
	}
	if len(kept) != 3 {
		t.Errorf("kept = %d, want 3", len(kept))
	}
	// Kept messages should be the last 3
	for i, m := range kept {
		expected := fmt.Sprintf("message-%d", 6+i)
		if m.Content != expected {
			t.Errorf("kept[%d].Content = %q, want %q", i, m.Content, expected)
		}
	}
	// Provider should have been called once
	if len(mp.calls) != 1 {
		t.Fatalf("provider calls = %d, want 1", len(mp.calls))
	}
	// Model should be passed correctly
	if mp.calls[0].Model != "test-model" {
		t.Errorf("request model = %q, want %q", mp.calls[0].Model, "test-model")
	}
}

func TestCompressor_Summarize_ProviderError(t *testing.T) {
	mp := &mockSummaryProvider{err: fmt.Errorf("connection timeout")}
	c := NewCompressor(defaultCfg(), 100_000, mp, "gpt-4o")

	msgs := makeMessages(6)
	summary, kept, err := c.Summarize(context.Background(), msgs)
	if err == nil {
		t.Fatal("expected error from Summarize")
	}
	if !strings.Contains(err.Error(), "connection timeout") {
		t.Errorf("error = %q, want to contain 'connection timeout'", err)
	}
	if summary != "" {
		t.Errorf("summary on error = %q, want empty", summary)
	}
	// On error, original messages should be returned unchanged
	if len(kept) != 6 {
		t.Errorf("kept on error = %d, want 6 (original)", len(kept))
	}
}

func TestCompressor_Summarize_NilProvider(t *testing.T) {
	c := NewCompressor(defaultCfg(), 100_000, nil, "gpt-4o")
	msgs := makeMessages(6)
	_, _, err := c.Summarize(context.Background(), msgs)
	if err == nil {
		t.Fatal("expected error with nil provider")
	}
}

func TestCompressor_Summarize_CustomSummaryModel(t *testing.T) {
	mp := &mockSummaryProvider{response: "ok"}
	cfg := defaultCfg()
	cfg.SummaryModel = "cheap-model"
	c := NewCompressor(cfg, 100_000, mp, "expensive-model")

	msgs := makeMessages(6)
	_, _, err := c.Summarize(context.Background(), msgs)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if mp.calls[0].Model != "cheap-model" {
		t.Errorf("model = %q, want %q", mp.calls[0].Model, "cheap-model")
	}
}

// --- Compress ---

func TestCompressor_Compress_Empty(t *testing.T) {
	mp := &mockSummaryProvider{response: "should not be called"}
	c := NewCompressor(defaultCfg(), 100_000, mp, "gpt-4o")

	summary, err := c.Compress(context.Background(), nil)
	if err != nil {
		t.Fatalf("Compress: %v", err)
	}
	if summary != "" {
		t.Errorf("summary = %q, want empty", summary)
	}
	if len(mp.calls) != 0 {
		t.Errorf("provider called %d times, want 0", len(mp.calls))
	}
}

func TestCompressor_Compress_CallsProvider(t *testing.T) {
	mp := &mockSummaryProvider{response: "compressed output"}
	c := NewCompressor(defaultCfg(), 100_000, mp, "gpt-4o")

	msgs := makeMessages(10)
	summary, err := c.Compress(context.Background(), msgs)
	if err != nil {
		t.Fatalf("Compress: %v", err)
	}
	if summary != "compressed output" {
		t.Errorf("summary = %q, want %q", summary, "compressed output")
	}
	if len(mp.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(mp.calls))
	}
	// Compress uses aggressive prompt
	sysContent := mp.calls[0].Messages[0].Content
	if !strings.Contains(sysContent, "aggressive") {
		t.Errorf("compress prompt should contain 'aggressive', got: %.100s", sysContent)
	}
}

func TestCompressor_Compress_ProviderError(t *testing.T) {
	mp := &mockSummaryProvider{err: fmt.Errorf("rate limited")}
	c := NewCompressor(defaultCfg(), 100_000, mp, "gpt-4o")

	_, err := c.Compress(context.Background(), makeMessages(5))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("error = %q, want to contain 'rate limited'", err)
	}
}

// --- OptimizeSummary ---

func TestCompressor_OptimizeSummary_NilProvider(t *testing.T) {
	c := NewCompressor(defaultCfg(), 100_000, nil, "gpt-4o")
	result, err := c.OptimizeSummary(context.Background(), "original summary", makeMessages(5))
	if err != nil {
		t.Fatalf("OptimizeSummary: %v", err)
	}
	// With nil provider, should return original unchanged
	if result != "original summary" {
		t.Errorf("result = %q, want %q", result, "original summary")
	}
}

func TestCompressor_OptimizeSummary_AcceptsShorter(t *testing.T) {
	mp := &mockSummaryProvider{response: "shorter"}
	c := NewCompressor(defaultCfg(), 100_000, mp, "gpt-4o")

	result, err := c.OptimizeSummary(context.Background(), "a longer original summary text", makeMessages(3))
	if err != nil {
		t.Fatalf("OptimizeSummary: %v", err)
	}
	if result != "shorter" {
		t.Errorf("result = %q, want %q", result, "shorter")
	}
}

func TestCompressor_OptimizeSummary_AcceptsSimilarLength(t *testing.T) {
	original := "original summary"    // 16 chars
	optimized := "improved summary!!" // 18 chars — within 120% of 16 (19.2)
	mp := &mockSummaryProvider{response: optimized}
	c := NewCompressor(defaultCfg(), 100_000, mp, "gpt-4o")

	result, err := c.OptimizeSummary(context.Background(), original, makeMessages(3))
	if err != nil {
		t.Fatalf("OptimizeSummary: %v", err)
	}
	if result != optimized {
		t.Errorf("result = %q, want %q", result, optimized)
	}
}

func TestCompressor_OptimizeSummary_RejectsBloated(t *testing.T) {
	original := "short"
	bloated := "this is a much much longer summary that far exceeds 120 percent of the original"
	mp := &mockSummaryProvider{response: bloated}
	c := NewCompressor(defaultCfg(), 100_000, mp, "gpt-4o")

	result, err := c.OptimizeSummary(context.Background(), original, makeMessages(3))
	if err != nil {
		t.Fatalf("OptimizeSummary: %v", err)
	}
	// Should reject bloated and return original
	if result != original {
		t.Errorf("result = %q, want original %q (bloated rejected)", result, original)
	}
}

func TestCompressor_OptimizeSummary_RejectsEmpty(t *testing.T) {
	mp := &mockSummaryProvider{response: "   "} // whitespace only → trimmed to empty
	c := NewCompressor(defaultCfg(), 100_000, mp, "gpt-4o")

	result, err := c.OptimizeSummary(context.Background(), "keep this", makeMessages(3))
	if err != nil {
		t.Fatalf("OptimizeSummary: %v", err)
	}
	if result != "keep this" {
		t.Errorf("result = %q, want %q", result, "keep this")
	}
}

func TestCompressor_OptimizeSummary_ProviderError_Graceful(t *testing.T) {
	mp := &mockSummaryProvider{err: fmt.Errorf("API error")}
	c := NewCompressor(defaultCfg(), 100_000, mp, "gpt-4o")

	result, err := c.OptimizeSummary(context.Background(), "original", makeMessages(3))
	if err != nil {
		t.Fatalf("OptimizeSummary should not return error: %v", err)
	}
	// Graceful degradation — return original
	if result != "original" {
		t.Errorf("result = %q, want %q (graceful fallback)", result, "original")
	}
}

func TestCompressor_OptimizeSummary_SamplesOriginal(t *testing.T) {
	mp := &mockSummaryProvider{response: "ok"}
	c := NewCompressor(defaultCfg(), 100_000, mp, "gpt-4o")

	msgs := makeMessages(15) // more than 10 → should truncate sample
	_, err := c.OptimizeSummary(context.Background(), "summary", msgs)
	if err != nil {
		t.Fatalf("OptimizeSummary: %v", err)
	}

	if len(mp.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(mp.calls))
	}
	userContent := mp.calls[0].Messages[1].Content
	// Should contain the truncation marker
	if !strings.Contains(userContent, "5 more messages") {
		t.Errorf("user prompt should note truncated messages, got: %s", userContent)
	}
}

// --- Summarize with tool calls and tool results ---

func TestCompressor_Summarize_IncludesToolCalls(t *testing.T) {
	mp := &mockSummaryProvider{response: "summary with tools"}
	c := NewCompressor(defaultCfg(), 100_000, mp, "gpt-4o")

	msgs := []cobot.Message{
		{Role: cobot.RoleUser, Content: "find files"},
		{Role: cobot.RoleAssistant, Content: "searching...", ToolCalls: []cobot.ToolCall{
			{ID: "tc1", Name: "grep", Arguments: []byte(`{"query":"*.go"}`)},
		}},
		{Role: cobot.RoleTool, Content: "", ToolResult: &cobot.ToolResult{
			CallID: "tc1", Output: "found 10 files",
		}},
		{Role: cobot.RoleAssistant, Content: "found your files"},
		{Role: cobot.RoleUser, Content: "thanks"},
	}

	summary, kept, err := c.Summarize(context.Background(), msgs)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if summary != "summary with tools" {
		t.Errorf("summary = %q", summary)
	}
	// 5 messages: keep 2 (5/3=1, min 2), summarize 3
	if len(kept) != 2 {
		t.Errorf("kept = %d, want 2", len(kept))
	}

	// Verify the summarized content sent to provider includes tool info
	userContent := mp.calls[0].Messages[1].Content
	if !strings.Contains(userContent, "tool_call: grep") {
		t.Errorf("summarize input should include tool_call, got: %s", userContent)
	}
	if !strings.Contains(userContent, "tool_result: found 10 files") {
		t.Errorf("summarize input should include tool_result, got: %s", userContent)
	}
}
