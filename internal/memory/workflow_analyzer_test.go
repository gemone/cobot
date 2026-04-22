package memory

import (
	"context"
	"testing"

	cobot "github.com/cobot-agent/cobot/pkg"
)

// mockMemoryStore is a test double for cobot.MemoryStore.
type mockMemoryStore struct {
	searchResults []*cobot.SearchResult
	searchErr     error
}

func (m *mockMemoryStore) Store(ctx context.Context, content, tier1, tier2 string) (string, error) {
	return "", nil
}

func (m *mockMemoryStore) StoreByName(ctx context.Context, content, wingName, roomName, hallType string) (string, error) {
	return "", nil
}

func (m *mockMemoryStore) Search(ctx context.Context, query *cobot.SearchQuery) ([]*cobot.SearchResult, error) {
	if m.searchErr != nil {
		return nil, m.searchErr
	}
	return m.searchResults, nil
}

func (m *mockMemoryStore) ConsolidateByName(ctx context.Context, wingName, roomName string) error {
	return nil
}

func (m *mockMemoryStore) Close() error {
	return nil
}

func TestWorkflowAnalyzer_Analyze_Changed(t *testing.T) {
	store := &mockMemoryStore{
		searchResults: []*cobot.SearchResult{
			{Content: "User always runs go test before committing"},
			{Content: "User prefers interfaces over concrete types"},
		},
	}
	provider := &mockProvider{
		response: "---\nname: auto-user-workflow-patterns\ndescription: Auto-generated summary of user workflow patterns\ncategory: auto-generated\n---\n\n# User Workflow Patterns\n\nThe user follows TDD practices and prefers interface-based design.",
	}

	wa := NewWorkflowAnalyzer(store, provider, "mock-model", "/tmp/skills")
	content, changed, err := wa.Analyze(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true")
	}
	if content == "" {
		t.Fatal("expected non-empty content")
	}
}

func TestWorkflowAnalyzer_Analyze_NoChange(t *testing.T) {
	store := &mockMemoryStore{
		searchResults: []*cobot.SearchResult{
			{Content: "User always runs go test before committing"},
		},
	}
	provider := &mockProvider{
		response: "---\nname: auto-user-workflow-patterns\ndescription: Auto-generated summary of user workflow patterns\ncategory: auto-generated\n---\n\n# User Workflow Patterns",
	}

	wa := NewWorkflowAnalyzer(store, provider, "mock-model", "/tmp/skills")

	// First call should detect change.
	_, changed, err := wa.Analyze(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true on first call")
	}

	// Second call with same response should detect no change.
	_, changed, err = wa.Analyze(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Fatal("expected changed=false on second call with same content")
	}
}

func TestWorkflowAnalyzer_Analyze_NoItems(t *testing.T) {
	store := &mockMemoryStore{searchResults: nil}
	provider := &mockProvider{response: "should not be called"}

	wa := NewWorkflowAnalyzer(store, provider, "mock-model", "/tmp/skills")
	content, changed, err := wa.Analyze(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Fatal("expected changed=false when no items")
	}
	if content != "" {
		t.Fatalf("expected empty content when no items, got %q", content)
	}
}

func TestWorkflowAnalyzer_Analyze_NoneResponse(t *testing.T) {
	store := &mockMemoryStore{
		searchResults: []*cobot.SearchResult{
			{Content: "Some vague item"},
		},
	}
	provider := &mockProvider{response: "NONE"}

	wa := NewWorkflowAnalyzer(store, provider, "mock-model", "/tmp/skills")
	content, changed, err := wa.Analyze(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Fatal("expected changed=false for NONE response")
	}
	if content != "" {
		t.Fatalf("expected empty content for NONE response, got %q", content)
	}
}

func TestWorkflowAnalyzer_Analyze_LLMError(t *testing.T) {
	store := &mockMemoryStore{
		searchResults: []*cobot.SearchResult{
			{Content: "User always runs go test before committing"},
		},
	}
	provider := &mockProvider{err: context.DeadlineExceeded}

	wa := NewWorkflowAnalyzer(store, provider, "mock-model", "/tmp/skills")
	_, _, err := wa.Analyze(context.Background())
	if err == nil {
		t.Fatal("expected error on LLM failure")
	}
}

func TestHashContent(t *testing.T) {
	h1 := hashContent("hello")
	h2 := hashContent("hello")
	h3 := hashContent("world")

	if h1 != h2 {
		t.Error("expected same hash for same content")
	}
	if h1 == h3 {
		t.Error("expected different hash for different content")
	}
	if len(h1) != 64 {
		t.Errorf("expected sha256 hex length 64, got %d", len(h1))
	}
}

func TestWorkflowAnalyzer_SkillsDir(t *testing.T) {
	wa := NewWorkflowAnalyzer(nil, nil, "", "/custom/skills")
	if wa.SkillsDir() != "/custom/skills" {
		t.Errorf("expected SkillsDir /custom/skills, got %q", wa.SkillsDir())
	}
}
