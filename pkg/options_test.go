package cobot

import "testing"

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.MaxTurns != 50 {
		t.Errorf("MaxTurns = %d, want 50", cfg.MaxTurns)
	}
	if cfg.Model != "openai:gpt-4o" {
		t.Errorf("Model = %q, want %q", cfg.Model, "openai:gpt-4o")
	}
	if cfg.APIKeys == nil {
		t.Error("APIKeys is nil, want non-nil map")
	}
	if !cfg.Memory.Enabled {
		t.Error("Memory.Enabled = false, want true")
	}
}
