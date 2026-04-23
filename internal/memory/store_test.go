package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cobot "github.com/cobot-agent/cobot/pkg"
)

func TestOpenCloseStore(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWingCRUD(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := context.Background()
	wing := &Wing{Name: "test-project", Type: "project", Keywords: []string{"go", "agent"}}
	if err := s.CreateWing(ctx, wing); err != nil {
		t.Fatal(err)
	}
	if wing.ID == "" {
		t.Error("expected wing ID to be set")
	}

	got, err := s.GetWing(ctx, wing.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "test-project" {
		t.Errorf("expected test-project, got %s", got.Name)
	}

	wings, err := s.GetWings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(wings) != 1 {
		t.Errorf("expected 1 wing, got %d", len(wings))
	}
}

func TestRoomCRUD(t *testing.T) {
	dir := t.TempDir()
	s, _ := OpenStore(dir, dir)
	defer s.Close()

	ctx := context.Background()
	wing := &Wing{Name: "proj", Type: "project"}
	s.CreateWing(ctx, wing)

	room := &Room{WingID: wing.ID, Name: "auth-migration", HallType: "facts"}
	if err := s.CreateRoom(ctx, room); err != nil {
		t.Fatal(err)
	}
	if room.ID == "" {
		t.Error("expected room ID")
	}

	rooms, err := s.GetRooms(ctx, wing.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rooms) != 1 {
		t.Errorf("expected 1 room, got %d", len(rooms))
	}
	if rooms[0].Name != "auth-migration" {
		t.Errorf("expected auth-migration, got %s", rooms[0].Name)
	}
}

func TestDrawerCRUD(t *testing.T) {
	dir := t.TempDir()
	s, _ := OpenStore(dir, dir)
	defer s.Close()

	ctx := context.Background()
	wing := &Wing{Name: "proj", Type: "project"}
	s.CreateWing(ctx, wing)
	room := &Room{WingID: wing.ID, Name: "decisions", HallType: "facts"}
	s.CreateRoom(ctx, room)

	id, err := s.AddDrawer(ctx, wing.ID, room.ID, "decided to use SQLite for storage")
	if err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Error("expected drawer ID")
	}
}

func TestClosetCRUD(t *testing.T) {
	dir := t.TempDir()
	s, _ := OpenStore(dir, dir)
	defer s.Close()

	ctx := context.Background()
	wing := &Wing{Name: "proj", Type: "project"}
	s.CreateWing(ctx, wing)
	room := &Room{WingID: wing.ID, Name: "summary", HallType: "facts"}
	s.CreateRoom(ctx, room)

	drawerID, _ := s.AddDrawer(ctx, wing.ID, room.ID, "content here")

	closet := &Closet{
		RoomID:    room.ID,
		DrawerIDs: []string{drawerID},
		Summary:   "brief summary",
	}
	if err := s.CreateCloset(ctx, closet); err != nil {
		t.Fatal(err)
	}

	closets, err := s.GetClosets(ctx, room.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(closets) != 1 {
		t.Fatalf("expected 1 closet, got %d", len(closets))
	}
	if closets[0].Summary != "brief summary" {
		t.Errorf("unexpected summary: %s", closets[0].Summary)
	}
}

func TestFTS5Search(t *testing.T) {
	dir := t.TempDir()
	s, _ := OpenStore(dir, dir)
	defer s.Close()

	ctx := context.Background()
	wing := &Wing{Name: "proj", Type: "project"}
	s.CreateWing(ctx, wing)
	room := &Room{WingID: wing.ID, Name: "decisions", HallType: "facts"}
	s.CreateRoom(ctx, room)

	// Store via Store() which sets tag = room.HallType and triggers FTS index.
	s.Store(ctx, "decided to use SQLite for storage", wing.ID, room.ID)

	results, err := s.searchDrawers(ctx, &cobot.SearchQuery{Text: "SQLite"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Error("expected search results for 'SQLite'")
	}
	if results[0].Content != "decided to use SQLite for storage" {
		t.Errorf("unexpected result: %s", results[0].Content)
	}
}

func TestStoreAndSearch(t *testing.T) {
	dir := t.TempDir()
	s, _ := OpenStore(dir, dir)
	defer s.Close()

	ctx := context.Background()
	wing := &Wing{Name: "proj", Type: "project"}
	s.CreateWing(ctx, wing)
	room := &Room{WingID: wing.ID, Name: "decisions", HallType: "facts"}
	s.CreateRoom(ctx, room)

	_, err := s.Store(ctx, "decided to use SQLite for storage", wing.ID, room.ID)
	if err != nil {
		t.Fatal(err)
	}

	results, err := s.Search(ctx, &cobot.SearchQuery{Text: "SQLite"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("expected search results for 'SQLite'")
	}
	found := false
	for _, r := range results {
		if r.Content == "decided to use SQLite for storage" {
			found = true
			break
		}
	}
	if !found {
		t.Error("stored content not found in search results")
	}
}

func TestStoreAndSearchMultiple(t *testing.T) {
	dir := t.TempDir()
	s, _ := OpenStore(dir, dir)
	defer s.Close()

	ctx := context.Background()
	wing := &Wing{Name: "proj", Type: "project"}
	s.CreateWing(ctx, wing)
	room := &Room{WingID: wing.ID, Name: "notes", HallType: "facts"}
	s.CreateRoom(ctx, room)

	s.Store(ctx, "decided to use SQLite for storage", wing.ID, room.ID)
	s.Store(ctx, "the API gateway handles routing", wing.ID, room.ID)
	s.Store(ctx, "SQLite WAL mode supports concurrent reads", wing.ID, room.ID)

	results, err := s.Search(ctx, &cobot.SearchQuery{Text: "SQLite"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 2 {
		t.Fatalf("expected at least 2 results, got %d", len(results))
	}
	for _, r := range results {
		if r.Content == "the API gateway handles routing" {
			t.Error("non-matching content should not appear in results")
		}
	}
	if results[0].Score < results[1].Score {
		t.Error("expected results sorted by descending score")
	}
}

func TestL3DeepSearch(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := context.Background()

	wing := &Wing{Name: "test"}
	s.CreateWing(ctx, wing)
	room := &Room{WingID: wing.ID, Name: "notes", HallType: "log"}
	s.CreateRoom(ctx, room)

	_, err = s.Store(ctx, "Important decision about architecture", wing.ID, room.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Store(ctx, "Another document about implementation", wing.ID, room.ID)
	if err != nil {
		t.Fatal(err)
	}

	results, err := s.L3DeepSearch(ctx, "decision", 10)
	if err != nil {
		t.Fatal(err)
	}

	if len(results) == 0 {
		t.Error("expected search results")
	}

	found := false
	for _, r := range results {
		if strings.Contains(r.Content, "Important decision") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected to find 'Important decision' in results")
	}
}

func TestSummarizeContent(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	tests := []struct {
		input    string
		expected string
	}{
		{
			input:    "This is a long line that should be summarized properly\nMore content here",
			expected: "This is a long line that should be summarized properly",
		},
		{
			input:    "Short",
			expected: "Short",
		},
		{
			input:    strings.Repeat("a", 300),
			expected: strings.Repeat("a", 200) + "...",
		},
	}

	for _, tt := range tests {
		result := s.SummarizeContent(tt.input)
		if result != tt.expected {
			t.Errorf("SummarizeContent(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestAutoSummarizeRoom(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := context.Background()

	wing := &Wing{Name: "test"}
	s.CreateWing(ctx, wing)
	room := &Room{WingID: wing.ID, Name: "notes", HallType: "log"}
	s.CreateRoom(ctx, room)

	_, err = s.Store(ctx, "First important note about the project", wing.ID, room.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Store(ctx, "Second note with more details", wing.ID, room.ID)
	if err != nil {
		t.Fatal(err)
	}

	err = s.AutoSummarizeRoom(ctx, wing.ID, room.ID)
	if err != nil {
		t.Fatal(err)
	}

	closets, err := s.GetClosets(ctx, room.ID)
	if err != nil {
		t.Fatal(err)
	}

	if len(closets) == 0 {
		t.Error("expected at least one closet after auto-summarize")
	}

	found := false
	for _, c := range closets {
		if c.Summary != "" && (strings.Contains(c.Summary, "First important note") || strings.Contains(c.Summary, "Second note")) {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected closet with summary containing note content")
	}
}

// --- Per-session STM tests ---

func TestSTMPerSessionDBCreation(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := context.Background()

	// Store STM items for two sessions.
	_, err = s.StoreShortTerm(ctx, "session-1", "hello from session 1", "context")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.StoreShortTerm(ctx, "session-2", "hello from session 2", "note")
	if err != nil {
		t.Fatal(err)
	}

	// Verify STM DB files were created in the memory directory.
	if _, err := os.Stat(filepath.Join(dir, "session-1.db")); err != nil {
		t.Errorf("session-1.db not created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "session-2.db")); err != nil {
		t.Errorf("session-2.db not created: %v", err)
	}

	// Verify LTM DB has no STM wings.
	wings, err := s.GetWings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range wings {
		if w.Name == "session" || w.Name == "stm_session-1" || w.Name == "stm_session-2" {
			t.Errorf("LTM DB should not contain STM wing %q", w.Name)
		}
	}
}

func TestSTMDataIsolatedBetweenSessions(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := context.Background()

	// Store items in two different sessions.
	s.StoreShortTerm(ctx, "alpha", "alpha context data", "context")
	s.StoreShortTerm(ctx, "alpha", "alpha observation data", "observation")
	s.StoreShortTerm(ctx, "beta", "beta context data", "context")

	// Recall from alpha should only have alpha data.
	alphaDrawers, err := s.RecallShortTerm(ctx, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if len(alphaDrawers) != 2 {
		t.Fatalf("expected 2 drawers for alpha, got %d", len(alphaDrawers))
	}
	for _, d := range alphaDrawers {
		if !strings.Contains(d.Content, "alpha") {
			t.Errorf("alpha session should not contain beta data: %s", d.Content)
		}
	}

	// Recall from beta should only have beta data.
	betaDrawers, err := s.RecallShortTerm(ctx, "beta")
	if err != nil {
		t.Fatal(err)
	}
	if len(betaDrawers) != 1 {
		t.Fatalf("expected 1 drawer for beta, got %d", len(betaDrawers))
	}
	if !strings.Contains(betaDrawers[0].Content, "beta") {
		t.Errorf("beta session should contain beta data: %s", betaDrawers[0].Content)
	}
}

func TestSTMPromoteToLTM(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := context.Background()

	// Store items in STM.
	s.StoreShortTerm(ctx, "promote-test", "important fact from session", "context")
	s.StoreShortTerm(ctx, "promote-test", "another fact from session", "note")

	// Verify STM has items.
	drawers, err := s.RecallShortTerm(ctx, "promote-test")
	if err != nil {
		t.Fatal(err)
	}
	if len(drawers) != 2 {
		t.Fatalf("expected 2 STM drawers, got %d", len(drawers))
	}

	// Promote to LTM.
	if err := s.PromoteToLongTerm(ctx, "promote-test"); err != nil {
		t.Fatal(err)
	}

	// Verify STM DB was NOT deleted (history is preserved after promotion).
	if _, err := os.Stat(filepath.Join(dir, "promote-test.db")); os.IsNotExist(err) {
		t.Error("STM DB file should be retained after promotion")
	}

	// Verify LTM has the promoted items under "sessions" wing.
	wing, err := s.GetWingByName(ctx, "sessions")
	if err != nil {
		t.Fatal(err)
	}
	if wing == nil {
		t.Fatal("expected 'sessions' wing in LTM after promotion")
	}
	rooms, err := s.GetRooms(ctx, wing.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rooms) == 0 {
		t.Fatal("expected at least one room in 'sessions' wing")
	}

	// Search for promoted content in LTM.
	results, err := s.Search(ctx, &cobot.SearchQuery{Text: "important fact"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Error("expected promoted content to be searchable in LTM")
	}
}

func TestSTMCleanupOnClear(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := context.Background()

	// Store an item.
	s.StoreShortTerm(ctx, "cleanup-test", "temporary data", "context")

	// Verify file exists.
	dbPath := filepath.Join(dir, "cleanup-test.db")
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("STM DB file not created: %v", err)
	}

	// Clear STM.
	if err := s.ClearShortTerm(ctx, "cleanup-test"); err != nil {
		t.Fatal(err)
	}

	// Verify file is deleted.
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Error("STM DB file should be deleted after ClearShortTerm")
	}

	// Recall should return nil (DB was deleted, getSTMDB will create fresh empty one).
	drawers, err := s.RecallShortTerm(ctx, "cleanup-test")
	if err != nil {
		t.Fatal(err)
	}
	if len(drawers) != 0 {
		t.Errorf("expected 0 drawers after clear, got %d", len(drawers))
	}
}
