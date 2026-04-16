package memory

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	cobot "github.com/cobot-agent/cobot/pkg"
)

func TestMemorySearchTool(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir, filepath.Join(dir, "stm"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := context.Background()
	wing := &cobot.Wing{Name: "proj", Type: "project"}
	s.CreateWing(ctx, wing)
	room := &cobot.Room{WingID: wing.ID, Name: "decisions", HallType: "facts"}
	s.CreateRoom(ctx, room)

	id, err := s.Store(ctx, "decided to use SQLite for storage", wing.ID, room.ID)
	if err != nil {
		t.Fatal(err)
	}
	_ = id

	tool := NewMemorySearchTool(s)
	result, err := tool.Execute(ctx, []byte(`{"query":"SQLite"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "SQLite") {
		t.Errorf("expected result to contain 'SQLite', got: %s", result)
	}
	if !strings.Contains(result, "Found") {
		t.Errorf("expected result to contain 'Found', got: %s", result)
	}
}

func TestMemoryStoreTool(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir, filepath.Join(dir, "stm"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := context.Background()

	tool := NewMemoryStoreTool(s)
	result, err := tool.Execute(ctx, []byte(`{"content":"test fact","wing_name":"project","room_name":"decisions","hall_type":"facts"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "Stored in drawer") {
		t.Errorf("expected drawer ID in result, got: %s", result)
	}
	if !strings.Contains(result, "project") || !strings.Contains(result, "decisions") {
		t.Errorf("expected wing/room names in result, got: %s", result)
	}

	wings, err := s.GetWings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(wings) != 1 {
		t.Errorf("expected 1 wing, got %d", len(wings))
	}
	if wings[0].Name != "project" {
		t.Errorf("expected wing name 'project', got %s", wings[0].Name)
	}

	rooms, err := s.GetRooms(ctx, wings[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rooms) != 1 {
		t.Errorf("expected 1 room, got %d", len(rooms))
	}
	if rooms[0].Name != "decisions" {
		t.Errorf("expected room name 'decisions', got %s", rooms[0].Name)
	}
}

func TestMemoryStoreToolAutoCreate(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir, filepath.Join(dir, "stm"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := context.Background()
	tool := NewMemoryStoreTool(s)

	_, err = tool.Execute(ctx, []byte(`{"content":"first fact","wing_name":"myproject","room_name":"notes"}`))
	if err != nil {
		t.Fatal(err)
	}

	_, err = tool.Execute(ctx, []byte(`{"content":"second fact","wing_name":"myproject","room_name":"notes"}`))
	if err != nil {
		t.Fatal(err)
	}

	wings, err := s.GetWings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(wings) != 1 {
		t.Errorf("expected wing to be reused (1 wing), got %d", len(wings))
	}

	rooms, err := s.GetRooms(ctx, wings[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rooms) != 1 {
		t.Errorf("expected room to be reused (1 room), got %d", len(rooms))
	}
}

func TestMemorySearchToolNoResults(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir, filepath.Join(dir, "stm"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := context.Background()
	tool := NewMemorySearchTool(s)

	result, err := tool.Execute(ctx, []byte(`{"query":"nonexistent"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "Found 0 results") {
		t.Errorf("expected no results message, got: %s", result)
	}
}

func TestMemorySearchToolWithWingFilter(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir, filepath.Join(dir, "stm"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := context.Background()
	wing1 := &cobot.Wing{Name: "alpha", Type: "project"}
	s.CreateWing(ctx, wing1)
	wing2 := &cobot.Wing{Name: "beta", Type: "project"}
	s.CreateWing(ctx, wing2)

	room1 := &cobot.Room{WingID: wing1.ID, Name: "notes", HallType: "facts"}
	s.CreateRoom(ctx, room1)
	room2 := &cobot.Room{WingID: wing2.ID, Name: "notes", HallType: "facts"}
	s.CreateRoom(ctx, room2)

	s.Store(ctx, "alpha content about SQLite", wing1.ID, room1.ID)
	s.Store(ctx, "beta content about SQLite", wing2.ID, room2.ID)

	tool := NewMemorySearchTool(s)
	result, err := tool.Execute(ctx, []byte(`{"query":"SQLite","tier1":"`+wing1.ID+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "alpha content") {
		t.Errorf("expected alpha content, got: %s", result)
	}
}

func TestMemoryStoreToolInvalidArgs(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir, filepath.Join(dir, "stm"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := context.Background()
	tool := NewMemoryStoreTool(s)

	_, err = tool.Execute(ctx, []byte(`{invalid json`))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestMemorySearchToolInvalidArgs(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir, filepath.Join(dir, "stm"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := context.Background()
	tool := NewMemorySearchTool(s)

	_, err = tool.Execute(ctx, []byte(`{invalid json`))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestMemoryStoreToolDefaultHallType(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir, filepath.Join(dir, "stm"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := context.Background()
	tool := NewMemoryStoreTool(s)

	_, err = tool.Execute(ctx, []byte(`{"content":"a fact","wing_name":"w","room_name":"r"}`))
	if err != nil {
		t.Fatal(err)
	}

	wings, _ := s.GetWings(ctx)
	rooms, _ := s.GetRooms(ctx, wings[0].ID)
	if rooms[0].HallType != "facts" {
		t.Errorf("expected default hall_type 'facts', got %s", rooms[0].HallType)
	}
}
