package agent

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	cobot "github.com/cobot-agent/cobot/pkg"
)

func TestSessionStoreSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	store := NewSessionStore(dir)

	session := NewSession()
	session.AddMessage(cobot.Message{Role: cobot.RoleUser, Content: "hello"})
	session.AddMessage(cobot.Message{Role: cobot.RoleAssistant, Content: "hi"})

	usage := cobot.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}

	if err := store.Save("test-session", session, usage, "openai:gpt-4o"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	path := filepath.Join(dir, "test-session.jsonl")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("session file not created: %v", err)
	}

	loaded, err := store.Load("test-session")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.ID != "test-session" {
		t.Errorf("ID = %q, want %q", loaded.ID, "test-session")
	}
	if loaded.Model != "openai:gpt-4o" {
		t.Errorf("Model = %q, want %q", loaded.Model, "openai:gpt-4o")
	}
	if len(loaded.Messages) != 2 {
		t.Fatalf("Messages len = %d, want 2", len(loaded.Messages))
	}
	if loaded.Messages[0].Content != "hello" {
		t.Errorf("Messages[0].Content = %q, want %q", loaded.Messages[0].Content, "hello")
	}
	if loaded.Messages[1].Content != "hi" {
		t.Errorf("Messages[1].Content = %q, want %q", loaded.Messages[1].Content, "hi")
	}
	if loaded.Usage.TotalTokens != 15 {
		t.Errorf("Usage.TotalTokens = %d, want 15", loaded.Usage.TotalTokens)
	}
	if loaded.CreatedAt.IsZero() {
		t.Error("CreatedAt should not be zero")
	}
	if loaded.UpdatedAt.IsZero() {
		t.Error("UpdatedAt should not be zero")
	}
}

func TestSessionStoreSavePreservesCreatedAt(t *testing.T) {
	dir := t.TempDir()
	store := NewSessionStore(dir)

	session := NewSession()
	session.AddMessage(cobot.Message{Role: cobot.RoleUser, Content: "first"})
	usage := cobot.Usage{PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8}

	if err := store.Save("s1", session, usage, "m1"); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	first, err := store.Load("s1")
	if err != nil {
		t.Fatalf("first Load: %v", err)
	}
	createdAt := first.CreatedAt

	session.AddMessage(cobot.Message{Role: cobot.RoleAssistant, Content: "second"})
	usage2 := cobot.Usage{PromptTokens: 10, CompletionTokens: 8, TotalTokens: 18}
	if err := store.Save("s1", session, usage2, "m1"); err != nil {
		t.Fatalf("second Save: %v", err)
	}

	second, err := store.Load("s1")
	if err != nil {
		t.Fatalf("second Load: %v", err)
	}
	if !second.CreatedAt.Equal(createdAt) {
		t.Errorf("CreatedAt changed: %v → %v", createdAt, second.CreatedAt)
	}
	if len(second.Messages) != 2 {
		t.Errorf("Messages len = %d, want 2", len(second.Messages))
	}
	if second.Usage.TotalTokens != 18 {
		t.Errorf("Usage.TotalTokens = %d, want 18", second.Usage.TotalTokens)
	}
}

func TestSessionStoreLoadNotFound(t *testing.T) {
	dir := t.TempDir()
	store := NewSessionStore(dir)

	_, err := store.Load("nonexistent")
	if err == nil {
		t.Error("Load non-existent should return error")
	}
}

func TestSessionStoreList(t *testing.T) {
	dir := t.TempDir()
	store := NewSessionStore(dir)

	session := NewSession()
	session.AddMessage(cobot.Message{Role: cobot.RoleUser, Content: "test"})
	usage := cobot.Usage{}

	if err := store.Save("aaa", session, usage, "m"); err != nil {
		t.Fatalf("Save aaa: %v", err)
	}
	if err := store.Save("bbb", session, usage, "m"); err != nil {
		t.Fatalf("Save bbb: %v", err)
	}

	ids, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("List len = %d, want 2", len(ids))
	}

	found := map[string]bool{}
	for _, id := range ids {
		found[id] = true
	}
	if !found["aaa"] || !found["bbb"] {
		t.Errorf("List = %v, want [aaa, bbb]", ids)
	}
}

func TestSessionStoreListEmpty(t *testing.T) {
	dir := t.TempDir()
	store := NewSessionStore(dir)

	ids, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("List len = %d, want 0", len(ids))
	}
}

func TestSessionStoreListIgnoresNonJSONL(t *testing.T) {
	dir := t.TempDir()
	store := NewSessionStore(dir)

	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hi"), 0644); err != nil {
		t.Fatalf("write non-jsonl: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	session := NewSession()
	session.AddMessage(cobot.Message{Role: cobot.RoleUser, Content: "x"})
	if err := store.Save("real", session, cobot.Usage{}, "m"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	ids, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(ids) != 1 || ids[0] != "real" {
		t.Errorf("List = %v, want [real]", ids)
	}
}

func TestSessionStoreAppendMessage(t *testing.T) {
	dir := t.TempDir()
	store := NewSessionStore(dir)

	if err := store.InitSession("s1", "openai:gpt-4o"); err != nil {
		t.Fatalf("InitSession: %v", err)
	}

	msg1 := cobot.Message{Role: cobot.RoleUser, Content: "hello"}
	msg2 := cobot.Message{Role: cobot.RoleAssistant, Content: "hi there"}
	if err := store.AppendMessage("s1", msg1); err != nil {
		t.Fatalf("AppendMessage 1: %v", err)
	}
	if err := store.AppendMessage("s1", msg2); err != nil {
		t.Fatalf("AppendMessage 2: %v", err)
	}

	loaded, err := store.Load("s1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.ID != "s1" {
		t.Errorf("ID = %q, want %q", loaded.ID, "s1")
	}
	if loaded.Model != "openai:gpt-4o" {
		t.Errorf("Model = %q, want %q", loaded.Model, "openai:gpt-4o")
	}
	if len(loaded.Messages) != 2 {
		t.Fatalf("Messages len = %d, want 2", len(loaded.Messages))
	}
	if loaded.Messages[0].Content != "hello" {
		t.Errorf("Messages[0].Content = %q, want %q", loaded.Messages[0].Content, "hello")
	}
	if loaded.Messages[1].Content != "hi there" {
		t.Errorf("Messages[1].Content = %q, want %q", loaded.Messages[1].Content, "hi there")
	}
}

func TestSessionStoreAppendUsage(t *testing.T) {
	dir := t.TempDir()
	store := NewSessionStore(dir)

	if err := store.InitSession("s1", "m1"); err != nil {
		t.Fatalf("InitSession: %v", err)
	}

	u1 := cobot.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}
	u2 := cobot.Usage{PromptTokens: 20, CompletionTokens: 10, TotalTokens: 30, ReasoningTokens: 3}
	if err := store.AppendUsage("s1", u1); err != nil {
		t.Fatalf("AppendUsage 1: %v", err)
	}
	if err := store.AppendUsage("s1", u2); err != nil {
		t.Fatalf("AppendUsage 2: %v", err)
	}

	loaded, err := store.Load("s1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.Usage.TotalTokens != 30 {
		t.Errorf("Usage.TotalTokens = %d, want 30 (latest)", loaded.Usage.TotalTokens)
	}
	if loaded.Usage.ReasoningTokens != 3 {
		t.Errorf("Usage.ReasoningTokens = %d, want 3", loaded.Usage.ReasoningTokens)
	}
}

func TestSessionStoreInitSessionIdempotent(t *testing.T) {
	dir := t.TempDir()
	store := NewSessionStore(dir)

	if err := store.InitSession("s1", "m1"); err != nil {
		t.Fatalf("first InitSession: %v", err)
	}
	if err := store.InitSession("s1", "m2"); err != nil {
		t.Fatalf("second InitSession: %v", err)
	}

	path := filepath.Join(dir, "s1.jsonl")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()

	lineCount := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lineCount++
	}
	if lineCount != 1 {
		t.Errorf("line count = %d, want 1 (idempotent)", lineCount)
	}
}

func TestSessionStoreJSONLFormat(t *testing.T) {
	dir := t.TempDir()
	store := NewSessionStore(dir)

	if err := store.InitSession("s1", "openai:gpt-4o"); err != nil {
		t.Fatalf("InitSession: %v", err)
	}
	if err := store.AppendMessage("s1", cobot.Message{Role: cobot.RoleUser, Content: "hello"}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if err := store.AppendUsage("s1", cobot.Usage{TotalTokens: 10}); err != nil {
		t.Fatalf("AppendUsage: %v", err)
	}

	path := filepath.Join(dir, "s1.jsonl")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()

	var entries []SessionEntry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var entry SessionEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			t.Fatalf("unmarshal line: %v", err)
		}
		entries = append(entries, entry)
	}

	if len(entries) != 3 {
		t.Fatalf("entry count = %d, want 3", len(entries))
	}
	if entries[0].Type != EntrySession {
		t.Errorf("entries[0].Type = %q, want %q", entries[0].Type, EntrySession)
	}
	if entries[1].Type != EntryMessage {
		t.Errorf("entries[1].Type = %q, want %q", entries[1].Type, EntryMessage)
	}
	if entries[2].Type != EntryUsage {
		t.Errorf("entries[2].Type = %q, want %q", entries[2].Type, EntryUsage)
	}
}

func TestSessionStoreLoadLegacyJSON(t *testing.T) {
	dir := t.TempDir()
	store := NewSessionStore(dir)

	legacyData := `{
		"id": "legacy-1",
		"messages": [{"role": "user", "content": "old format"}],
		"usage": {"prompt_tokens": 5, "completion_tokens": 3, "total_tokens": 8},
		"model": "openai:gpt-3.5",
		"created_at": "2025-01-01T00:00:00Z",
		"updated_at": "2025-01-01T00:01:00Z"
	}`
	if err := os.WriteFile(filepath.Join(dir, "legacy-1.json"), []byte(legacyData), 0644); err != nil {
		t.Fatalf("write legacy: %v", err)
	}

	loaded, err := store.Load("legacy-1")
	if err != nil {
		t.Fatalf("Load legacy: %v", err)
	}
	if loaded.ID != "legacy-1" {
		t.Errorf("ID = %q, want %q", loaded.ID, "legacy-1")
	}
	if len(loaded.Messages) != 1 {
		t.Fatalf("Messages len = %d, want 1", len(loaded.Messages))
	}
	if loaded.Messages[0].Content != "old format" {
		t.Errorf("Messages[0].Content = %q, want %q", loaded.Messages[0].Content, "old format")
	}
	if loaded.Usage.TotalTokens != 8 {
		t.Errorf("Usage.TotalTokens = %d, want 8", loaded.Usage.TotalTokens)
	}
}

func TestSessionStoreListMixed(t *testing.T) {
	dir := t.TempDir()
	store := NewSessionStore(dir)

	if err := os.WriteFile(filepath.Join(dir, "old.json"), []byte(`{"id":"old"}`), 0644); err != nil {
		t.Fatalf("write json: %v", err)
	}

	session := NewSession()
	session.AddMessage(cobot.Message{Role: cobot.RoleUser, Content: "x"})
	if err := store.Save("new", session, cobot.Usage{}, "m"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	ids, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("List len = %d, want 2", len(ids))
	}

	found := map[string]bool{}
	for _, id := range ids {
		found[id] = true
	}
	if !found["old"] || !found["new"] {
		t.Errorf("List = %v, want [old, new]", ids)
	}
}
