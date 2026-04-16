package agent

import (
	"context"
	"fmt"
	"sync"
	"testing"

	cobot "github.com/cobot-agent/cobot/pkg"
)

func TestUsageTrackerNew(t *testing.T) {
	tr := NewUsageTracker()
	u := tr.Get()
	if u.PromptTokens != 0 || u.CompletionTokens != 0 || u.TotalTokens != 0 {
		t.Errorf("new tracker should be zero: %+v", u)
	}
}

func TestUsageTrackerAdd(t *testing.T) {
	tr := NewUsageTracker()
	tr.Add(cobot.Usage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150})
	u := tr.Get()
	if u.PromptTokens != 100 {
		t.Errorf("PromptTokens = %d, want 100", u.PromptTokens)
	}
	if u.CompletionTokens != 50 {
		t.Errorf("CompletionTokens = %d, want 50", u.CompletionTokens)
	}
	if u.TotalTokens != 150 {
		t.Errorf("TotalTokens = %d, want 150", u.TotalTokens)
	}
}

func TestUsageTrackerAccumulate(t *testing.T) {
	tr := NewUsageTracker()
	tr.Add(cobot.Usage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150})
	tr.Add(cobot.Usage{PromptTokens: 200, CompletionTokens: 100, TotalTokens: 300})
	u := tr.Get()
	if u.PromptTokens != 300 {
		t.Errorf("PromptTokens = %d, want 300", u.PromptTokens)
	}
	if u.CompletionTokens != 150 {
		t.Errorf("CompletionTokens = %d, want 150", u.CompletionTokens)
	}
	if u.TotalTokens != 450 {
		t.Errorf("TotalTokens = %d, want 450", u.TotalTokens)
	}
}

func TestUsageTrackerReset(t *testing.T) {
	tr := NewUsageTracker()
	tr.Add(cobot.Usage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150})
	tr.Reset()
	u := tr.Get()
	if u.PromptTokens != 0 || u.CompletionTokens != 0 || u.TotalTokens != 0 {
		t.Errorf("after reset should be zero: %+v", u)
	}
}

func TestUsageTrackerConcurrent(t *testing.T) {
	tr := NewUsageTracker()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tr.Add(cobot.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2})
		}()
	}
	wg.Wait()
	u := tr.Get()
	if u.PromptTokens != 100 {
		t.Errorf("PromptTokens = %d, want 100 (concurrent adds)", u.PromptTokens)
	}
	if u.CompletionTokens != 100 {
		t.Errorf("CompletionTokens = %d, want 100 (concurrent adds)", u.CompletionTokens)
	}
	if u.TotalTokens != 200 {
		t.Errorf("TotalTokens = %d, want 200 (concurrent adds)", u.TotalTokens)
	}
}

func TestAgentSessionUsage(t *testing.T) {
	a := New(&cobot.Config{MaxTurns: 10}, newTestRegistry())
	u := a.SessionUsage()
	if u.PromptTokens != 0 {
		t.Errorf("initial usage should be zero, got PromptTokens=%d", u.PromptTokens)
	}
}

func TestAgentResetUsage(t *testing.T) {
	a := New(&cobot.Config{MaxTurns: 10}, newTestRegistry())
	a.usageTracker.Add(cobot.Usage{PromptTokens: 500, CompletionTokens: 200, TotalTokens: 700})
	a.ResetUsage()
	u := a.SessionUsage()
	if u.PromptTokens != 0 {
		t.Errorf("after reset should be zero, got PromptTokens=%d", u.PromptTokens)
	}
}

func TestAgentModelDefault(t *testing.T) {
	a := New(&cobot.Config{Model: "openai:gpt-4o", MaxTurns: 10}, newTestRegistry())
	if m := a.Model(); m != "openai:gpt-4o" {
		t.Errorf("Model() = %q, want %q", m, "openai:gpt-4o")
	}
}

func TestAgentSetModelWithoutRegistry(t *testing.T) {
	a := New(&cobot.Config{Model: "original", MaxTurns: 10}, newTestRegistry())
	if err := a.SetModel("new-model"); err != nil {
		t.Fatalf("SetModel without registry should not error: %v", err)
	}
	if m := a.Model(); m != "new-model" {
		t.Errorf("Model() = %q, want %q", m, "new-model")
	}
}

func TestAgentSetModelWithRegistry(t *testing.T) {
	a := New(&cobot.Config{Model: "original", MaxTurns: 10}, newTestRegistry())
	reg := &mockModelRegistry{provider: &registryTestProvider{name: "test"}, modelName: "gpt-4o-mini"}
	a.SetRegistry(reg)

	if err := a.SetModel("test:gpt-4o-mini"); err != nil {
		t.Fatalf("SetModel with registry: %v", err)
	}
	if m := a.Model(); m != "gpt-4o-mini" {
		t.Errorf("Model() = %q, want %q", m, "gpt-4o-mini")
	}
	if p := a.Provider(); p.Name() != "test" {
		t.Errorf("Provider.Name() = %q, want %q", p.Name(), "test")
	}
}

func TestAgentSetModelRegistryError(t *testing.T) {
	a := New(&cobot.Config{Model: "original", MaxTurns: 10}, newTestRegistry())
	reg := &mockModelRegistry{err: fmt.Errorf("unknown provider: x")}
	a.SetRegistry(reg)

	if err := a.SetModel("x:model"); err == nil {
		t.Error("expected error from registry, got nil")
	}
	// Model should remain unchanged
	if m := a.Model(); m != "original" {
		t.Errorf("Model() = %q, want %q (unchanged)", m, "original")
	}
}

// registryTestProvider implements cobot.Provider for usage/model tests.
type registryTestProvider struct {
	name string
}

func (p *registryTestProvider) Name() string { return p.name }
func (p *registryTestProvider) Complete(_ context.Context, _ *cobot.ProviderRequest) (*cobot.ProviderResponse, error) {
	return &cobot.ProviderResponse{Content: "test", StopReason: cobot.StopEndTurn}, nil
}
func (p *registryTestProvider) Stream(_ context.Context, _ *cobot.ProviderRequest) (<-chan cobot.ProviderChunk, error) {
	ch := make(chan cobot.ProviderChunk, 1)
	ch <- cobot.ProviderChunk{Content: "test", Done: true}
	close(ch)
	return ch, nil
}

// mockModelRegistry implements cobot.ModelResolver for testing.
type mockModelRegistry struct {
	provider  cobot.Provider
	modelName string
	err       error
}

func (r *mockModelRegistry) ProviderForModel(spec string) (cobot.Provider, string, error) {
	if r.err != nil {
		return nil, "", r.err
	}
	return r.provider, r.modelName, nil
}

func (r *mockModelRegistry) ValidateModel(_ context.Context, _ string) error {
	return r.err
}
