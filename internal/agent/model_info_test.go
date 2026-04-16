package agent

import (
	"testing"
)

func TestContextWindowForModel_BuiltinDefaults(t *testing.T) {
	tests := []struct {
		model string
		want  int
	}{
		{"gpt-4o", 128_000},
		{"gpt-4", 8_192},
		{"claude-3-5-sonnet", 200_000},
		{"o3", 200_000},
		{"deepseek-chat", 64_000},
	}
	for _, tt := range tests {
		got := ContextWindowForModel(tt.model, nil)
		if got != tt.want {
			t.Errorf("ContextWindowForModel(%q) = %d, want %d", tt.model, got, tt.want)
		}
	}
}

func TestContextWindowForModel_PrefixMatch(t *testing.T) {
	got := ContextWindowForModel("gpt-4o-2024-08-06", nil)
	if got != 128_000 {
		t.Errorf("prefix match gpt-4o-* = %d, want 128000", got)
	}
}

func TestContextWindowForModel_PrefixMatchLongestWins(t *testing.T) {
	// "gpt-4o-mini-2024-07-18" must match "gpt-4o-mini" (128K), not "gpt-4" (8K).
	got := ContextWindowForModel("gpt-4o-mini-2024-07-18", nil)
	if got != 128_000 {
		t.Errorf("prefix match gpt-4o-mini-* = %d, want 128000 (longest prefix wins)", got)
	}

	// "gpt-4-turbo-2024-04-09" must match "gpt-4-turbo" (128K), not "gpt-4" (8K).
	got = ContextWindowForModel("gpt-4-turbo-2024-04-09", nil)
	if got != 128_000 {
		t.Errorf("prefix match gpt-4-turbo-* = %d, want 128000 (longest prefix wins)", got)
	}
}

func TestContextWindowForModel_Override(t *testing.T) {
	overrides := map[string]int{"custom-model": 32_000}
	got := ContextWindowForModel("custom-model", overrides)
	if got != 32_000 {
		t.Errorf("override = %d, want 32000", got)
	}
}

func TestContextWindowForModel_Unknown(t *testing.T) {
	got := ContextWindowForModel("totally-unknown-model-xyz", nil)
	if got != defaultContextWindow {
		t.Errorf("unknown model = %d, want %d", got, defaultContextWindow)
	}
}
