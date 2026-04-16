package memory

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	cobot "github.com/cobot-agent/cobot/pkg"
)

func TestWakeUpBasic(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir, filepath.Join(dir, "stm"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := context.Background()
	result, err := s.WakeUp(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "You are Cobot") {
		t.Errorf("expected identity prompt, got: %s", result)
	}
}

func TestWakeUpWithFacts(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir, filepath.Join(dir, "stm"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := context.Background()

	wing := &cobot.Wing{Name: "test"}
	s.CreateWing(ctx, wing)
	room := &cobot.Room{WingID: wing.ID, Name: "facts", HallType: "facts"}
	s.CreateRoom(ctx, room)
	closet := &cobot.Closet{RoomID: room.ID, Summary: "Important fact about testing"}
	s.CreateCloset(ctx, closet)

	result, err := s.WakeUp(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "Known Facts") {
		t.Errorf("expected facts section, got: %s", result)
	}
	if !strings.Contains(result, "Important fact about testing") {
		t.Errorf("expected fact content, got: %s", result)
	}
}

func TestWakeUpWithRoomContext(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir, filepath.Join(dir, "stm"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := context.Background()

	wing := &cobot.Wing{Name: "myproject"}
	s.CreateWing(ctx, wing)
	room := &cobot.Room{WingID: wing.ID, Name: "notes", HallType: "log"}
	s.CreateRoom(ctx, room)

	_, err = s.Store(ctx, "First note about the project", wing.ID, room.ID)
	if err != nil {
		t.Fatal(err)
	}

	result, err := s.WakeUp(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "Room Context") {
		t.Errorf("expected room context section, got: %s", result)
	}
	if !strings.Contains(result, "myproject") {
		t.Errorf("expected wing name, got: %s", result)
	}
	if !strings.Contains(result, "notes") {
		t.Errorf("expected room name, got: %s", result)
	}
}

func TestWakeUpIgnoresNonFactsInFactsSection(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir, filepath.Join(dir, "stm"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := context.Background()
	wing := &cobot.Wing{Name: "proj", Type: "project"}
	s.CreateWing(ctx, wing)
	room := &cobot.Room{WingID: wing.ID, Name: "log-room", HallType: "log"}
	s.CreateRoom(ctx, room)

	closet := &cobot.Closet{
		RoomID:  room.ID,
		Summary: "this should not appear in Known Facts",
	}
	s.CreateCloset(ctx, closet)

	got, err := s.WakeUp(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "Known Facts") && strings.Contains(got, "this should not appear in Known Facts") {
		t.Errorf("non-fact rooms should not appear in Known Facts section, got %q", got)
	}
}

func TestWakeUpWithDeepSearch(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir, filepath.Join(dir, "stm"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := context.Background()

	wing := &cobot.Wing{Name: "myproject"}
	s.CreateWing(ctx, wing)
	room := &cobot.Room{WingID: wing.ID, Name: "notes", HallType: "log"}
	s.CreateRoom(ctx, room)

	_, err = s.Store(ctx, "Important decision made about the architecture", wing.ID, room.ID)
	if err != nil {
		t.Fatal(err)
	}

	result, err := s.WakeUpWithDeepSearch(ctx, true)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(result, "Deep Search") {
		t.Errorf("expected deep search section, got: %s", result)
	}

	if !strings.Contains(result, "Important decision") && !strings.Contains(result, "Related:") {
		t.Errorf("expected deep search content, got: %s", result)
	}
}
