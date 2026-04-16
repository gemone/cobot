package agent

import (
	"encoding/json"
	"testing"

	cobot "github.com/cobot-agent/cobot/pkg"
)

func TestEstimateTokensEmpty(t *testing.T) {
	if got := estimateTokens(""); got != 0 {
		t.Errorf("estimateTokens(\"\") = %d, want 0", got)
	}
}

func TestEstimateTokensASCII(t *testing.T) {
	// 12 ASCII chars → 12/4 + 3 = 6
	got := estimateTokens("hello world!")
	if got != 6 {
		t.Errorf("estimateTokens(\"hello world!\") = %d, want 6", got)
	}
}

func TestEstimateTokensCJK(t *testing.T) {
	// 4 CJK chars → 0/4 + 4 + 3 = 7
	got := estimateTokens("你好世界")
	if got != 7 {
		t.Errorf("estimateTokens(\"你好世界\") = %d, want 7", got)
	}
}

func TestEstimateTokensMixed(t *testing.T) {
	// "hello 你好" → 6 ASCII + 2 CJK → 6/4 + 2 + 3 = 1 + 2 + 3 = 6
	got := estimateTokens("hello 你好")
	if got != 6 {
		t.Errorf("estimateTokens(\"hello 你好\") = %d, want 6", got)
	}
}

func TestEstimateTokensJapanese(t *testing.T) {
	// "こんにちは" → 5 Hiragana chars → 0/4 + 5 + 3 = 8
	got := estimateTokens("こんにちは")
	if got != 8 {
		t.Errorf("estimateTokens(\"こんにちは\") = %d, want 8", got)
	}
}

func TestEstimateTokensKatakana(t *testing.T) {
	// "カタカナ" → 4 Katakana chars → 0/4 + 4 + 3 = 7
	got := estimateTokens("カタカナ")
	if got != 7 {
		t.Errorf("estimateTokens(\"カタカナ\") = %d, want 7", got)
	}
}

func TestEstimateTokensKorean(t *testing.T) {
	// "안녕" → 2 Hangul chars → 0/4 + 2 + 3 = 5
	got := estimateTokens("안녕")
	if got != 5 {
		t.Errorf("estimateTokens(\"안녕\") = %d, want 5", got)
	}
}

func TestEstimateMessagesUsageEmpty(t *testing.T) {
	u := estimateMessagesUsage(nil)
	if u.PromptTokens != 0 || u.CompletionTokens != 0 || u.TotalTokens != 0 {
		t.Errorf("empty messages should yield zero usage, got %+v", u)
	}
}

func TestEstimateMessagesUsageBasic(t *testing.T) {
	msgs := []cobot.Message{
		{Role: cobot.RoleUser, Content: "hello world!"},      // 12 ASCII → 6 tokens → prompt
		{Role: cobot.RoleAssistant, Content: "hello world!"}, // 12 ASCII → 6 tokens → completion
	}
	u := estimateMessagesUsage(msgs)
	if u.PromptTokens != 6 {
		t.Errorf("PromptTokens = %d, want 6", u.PromptTokens)
	}
	if u.CompletionTokens != 6 {
		t.Errorf("CompletionTokens = %d, want 6", u.CompletionTokens)
	}
	if u.TotalTokens != 12 {
		t.Errorf("TotalTokens = %d, want 12", u.TotalTokens)
	}
}

func TestEstimateMessagesUsageWithToolCalls(t *testing.T) {
	msgs := []cobot.Message{
		{
			Role:    cobot.RoleAssistant,
			Content: "",
			ToolCalls: []cobot.ToolCall{
				{
					Name:      "read_file",
					Arguments: json.RawMessage(`{"path":"test.go"}`),
				},
			},
		},
	}
	u := estimateMessagesUsage(msgs)
	// Content "" → 0
	// "read_file" → 9 ASCII → 9/4 + 3 = 5
	// `{"path":"test.go"}` → 18 ASCII → 18/4 + 3 = 7
	// Total completion: 0 + 5 + 7 = 12
	if u.CompletionTokens != 12 {
		t.Errorf("CompletionTokens = %d, want 12", u.CompletionTokens)
	}
	if u.PromptTokens != 0 {
		t.Errorf("PromptTokens = %d, want 0", u.PromptTokens)
	}
}

func TestEstimateMessagesUsageWithToolResult(t *testing.T) {
	msgs := []cobot.Message{
		{
			Role: cobot.RoleTool,
			ToolResult: &cobot.ToolResult{
				Output: "file content here",
				Error:  "",
			},
		},
	}
	u := estimateMessagesUsage(msgs)
	// "file content here" → 17 ASCII → 17/4 + 3 = 7
	// "" → 0
	// Content "" → 0
	// Role is tool → prompt
	if u.PromptTokens != 7 {
		t.Errorf("PromptTokens = %d, want 7", u.PromptTokens)
	}
	if u.CompletionTokens != 0 {
		t.Errorf("CompletionTokens = %d, want 0", u.CompletionTokens)
	}
}

func TestEstimateMessagesUsageSystemRole(t *testing.T) {
	msgs := []cobot.Message{
		{Role: cobot.RoleSystem, Content: "You are a helpful assistant."},
	}
	u := estimateMessagesUsage(msgs)
	// "You are a helpful assistant." → 28 ASCII → 28/4 + 3 = 10
	// System role → prompt
	if u.PromptTokens != 10 {
		t.Errorf("PromptTokens = %d, want 10", u.PromptTokens)
	}
	if u.CompletionTokens != 0 {
		t.Errorf("CompletionTokens = %d, want 0", u.CompletionTokens)
	}
}
