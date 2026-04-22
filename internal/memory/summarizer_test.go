package memory

import (
	"context"
	"testing"

	cobot "github.com/cobot-agent/cobot/pkg"
)

// mockProvider is a test double for cobot.Provider.
type mockProvider struct {
	response string
	err      error
}

func (m *mockProvider) Name() string { return "mock" }

func (m *mockProvider) Complete(ctx context.Context, req *cobot.ProviderRequest) (*cobot.ProviderResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &cobot.ProviderResponse{Content: m.response}, nil
}

func (m *mockProvider) Stream(ctx context.Context, req *cobot.ProviderRequest) (<-chan cobot.ProviderChunk, error) {
	return nil, nil
}

func TestSummarize_ExtractsInsights(t *testing.T) {
	provider := &mockProvider{
		response: `[FACT] Project uses Go 1.22 with generics

[PATTERN] User prefers table-driven tests

[FACT] Chose SQLite over PostgreSQL for embedded deployment
`,
	}

	s := NewSummarizer(provider, "mock-model")
	items := []STMItem{
		{Content: "We are using Go 1.22", Category: "context"},
		{Content: "Let's use generics for the repository layer", Category: "context"},
		{Content: "I like table-driven tests", Category: "notes"},
		{Content: "SQLite is simpler for embedded", Category: "context"},
	}

	insights, err := s.Summarize(context.Background(), items, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(insights) != 3 {
		t.Fatalf("expected 3 insights, got %d", len(insights))
	}

	if insights[0].Category != "fact" {
		t.Errorf("expected first insight category 'fact', got %q", insights[0].Category)
	}
	if insights[1].Category != "pattern" {
		t.Errorf("expected second insight category 'pattern', got %q", insights[1].Category)
	}
	if insights[2].Category != "fact" {
		t.Errorf("expected third insight category 'fact', got %q", insights[2].Category)
	}
}

func TestSummarize_WithCompressedSummary(t *testing.T) {
	provider := &mockProvider{
		response: `[PATTERN] User always runs tests before committing
`,
	}

	s := NewSummarizer(provider, "mock-model")
	items := []STMItem{
		{Content: "ran go test", Category: "observation"},
	}

	insights, err := s.Summarize(context.Background(), items, "Session focused on testing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(insights) != 1 {
		t.Fatalf("expected 1 insight, got %d", len(insights))
	}
	if insights[0].Category != "pattern" {
		t.Errorf("expected category 'pattern', got %q", insights[0].Category)
	}
}

func TestSummarize_FallbackOnEmptyResponse(t *testing.T) {
	provider := &mockProvider{response: "NONE"}
	s := NewSummarizer(provider, "mock-model")
	items := []STMItem{{Content: "something", Category: "context"}}

	insights, err := s.Summarize(context.Background(), items, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(insights) != 0 {
		t.Fatalf("expected 0 insights for NONE response, got %d", len(insights))
	}
}

func TestSummarize_FallbackOnLLMError(t *testing.T) {
	provider := &mockProvider{err: context.DeadlineExceeded}
	s := NewSummarizer(provider, "mock-model")
	items := []STMItem{{Content: "something", Category: "context"}}

	insights, err := s.Summarize(context.Background(), items, "")
	if err != nil {
		t.Fatalf("expected nil error on LLM failure, got %v", err)
	}
	if len(insights) != 0 {
		t.Fatalf("expected 0 insights on LLM failure, got %d", len(insights))
	}
}

func TestParseSummarizationResponse_MixedBlocks(t *testing.T) {
	response := `[FACT] SQLite WAL mode enabled
[PATTERN] Dark mode UI
[FACT] TDD with red-green-refactor
[PATTERN] Monorepo over polyrepo`

	insights := parseSummarizationResponse(response)
	if len(insights) != 4 {
		t.Fatalf("expected 4 insights, got %d", len(insights))
	}

	expected := []string{"fact", "pattern", "fact", "pattern"}
	for i, exp := range expected {
		if insights[i].Category != exp {
			t.Errorf("insight %d: expected category %q, got %q", i, exp, insights[i].Category)
		}
	}
}

func TestInsightRoomMapping(t *testing.T) {
	tests := []struct {
		input    string
		room     string
		hallType string
	}{
		{"fact", "facts", cobot.TagFacts},
		{"pattern", "patterns", "pattern"},
		{"unknown", "facts", cobot.TagFacts},
	}

	for _, tt := range tests {
		room, hallType := insightRoomMapping(tt.input)
		if room != tt.room {
			t.Errorf("insightRoomMapping(%q) room = %q, want %q", tt.input, room, tt.room)
		}
		if hallType != tt.hallType {
			t.Errorf("insightRoomMapping(%q) hallType = %q, want %q", tt.input, hallType, tt.hallType)
		}
	}
}
