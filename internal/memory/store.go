package memory

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	cobot "github.com/cobot-agent/cobot/pkg"
)

// Store provides hierarchical memory storage backed by SQLite with FTS5.
type Store struct {
	db *sql.DB
}

// OpenStore opens a SQLite-backed memory store at the given directory.
// It creates the directory and database file if they don't exist,
// enables WAL mode for concurrent reads, and ensures the schema is current.
func OpenStore(memoryDir string) (*Store, error) {
	db, err := openDB(memoryDir)
	if err != nil {
		return nil, err
	}
	if err := ensureSchema(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// Store adds content to a room's drawers and indexes it for full-text search.
// The content is automatically indexed via SQLite triggers on the drawers table.
func (s *Store) Store(ctx context.Context, content string, wingID, roomID string) (string, error) {
	// Verify room exists before inserting.
	room, err := s.GetRoom(ctx, wingID, roomID)
	if err != nil {
		return "", err
	}

	id := newID()
	_, err = s.db.ExecContext(ctx, sqlInsertDrawer, id, roomID, content, room.HallType, time.Now().UTC())
	if err != nil {
		return "", err
	}

	return id, nil
}

func (s *Store) StoreByName(ctx context.Context, content, wingName, roomName, hallType string) (string, error) {
	if hallType == "" {
		hallType = cobot.TagFacts
	}
	wingID, err := s.CreateWingIfNotExists(ctx, wingName)
	if err != nil {
		return "", err
	}
	roomID, err := s.CreateRoomIfNotExists(ctx, wingID, roomName, hallType)
	if err != nil {
		return "", err
	}
	return s.Store(ctx, content, wingID, roomID)
}

func (s *Store) ConsolidateByName(ctx context.Context, wingName, roomName string) error {
	wing, err := s.GetWingByName(ctx, wingName)
	if err != nil || wing == nil {
		return nil
	}
	room, err := s.GetRoomByName(ctx, wing.ID, roomName)
	if err != nil || room == nil {
		return nil
	}
	return s.AutoSummarizeRoom(ctx, wing.ID, room.ID)
}

// Search performs full-text search across all drawers.
func (s *Store) Search(ctx context.Context, query *cobot.SearchQuery) ([]*cobot.SearchResult, error) {
	return s.searchDrawers(ctx, query)
}

var (
	_ cobot.MemoryStore  = (*Store)(nil)
	_ cobot.MemoryRecall = (*Store)(nil)
	_ cobot.ShortTermMemory = (*Store)(nil)
)

// --- Short-term Memory (STM) ---
//
// Each session gets its own Wing named "stm_{session_id}" with five rooms:
//   - "context"     — user directives, task state, decisions
//   - "todo"        — TODO items tracked during session
//   - "notes"       — temporary notes, user requirements
//   - "observation" — tool results, build/test outcomes, error states
//   - "compressed"  — compressed session records from compressor
//
// The wing is deleted when the session ends (after promoting valuable items to LTM).

const (
	stmWingPrefix = "stm_"         // prefix + sessionID = wing name
	stmMaxItems   = 20

	stmRoomContext    = "context"     // user directives, decisions, task state
	stmRoomTodo       = "todo"        // TODO items tracked during session
	stmRoomNotes      = "notes"       // temporary notes, user requirements
	stmRoomObservation = "observation" // tool results, build/test outcomes, errors
	stmRoomCompressed  = "compressed"  // compressed session records from compressor
)

// stmWingName returns the wing name for a session's short-term memory.
func stmWingName(sessionID string) string {
	return stmWingPrefix + sessionID
}

// stmRoomForCategory maps an extractor category to an STM room name.
func stmRoomForCategory(category string) string {
	switch category {
	case "task_state", "decision", "context":
		return stmRoomContext
	case "todo":
		return stmRoomTodo
	case "note", "requirement":
		return stmRoomNotes
	case "observation", "error":
		return stmRoomObservation
	case "compressed":
		return stmRoomCompressed
	default:
		return stmRoomContext
	}
}

// StoreShortTerm stores a short-term memory item for the given session.
// The category determines which room the item goes into:
//   "context"/"task_state"/"decision" → "context" room
//   "todo"                            → "todo" room
//   "note"/"requirement"              → "notes" room
//   "observation"/"error"             → "observation" room
//   "compressed"                      → "compressed" room
func (s *Store) StoreShortTerm(ctx context.Context, sessionID, content, category string) (string, error) {
	wingName := stmWingName(sessionID)
	roomName := stmRoomForCategory(category)
	return s.StoreByName(ctx, content, wingName, roomName, category)
}

// RecallShortTerm retrieves all short-term memories for the given session
// from both rooms, ordered by creation time (oldest first).
func (s *Store) RecallShortTerm(ctx context.Context, sessionID string) ([]*cobot.Drawer, error) {
	wing, err := s.GetWingByName(ctx, stmWingName(sessionID))
	if err != nil || wing == nil {
		return nil, nil
	}

	rooms, err := s.GetRooms(ctx, wing.ID)
	if err != nil {
		return nil, err
	}

	var all []*cobot.Drawer
	for _, room := range rooms {
		rows, err := s.db.QueryContext(ctx, sqlSelectDrawersByRoomOrdered, room.ID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var d cobot.Drawer
			if err := rows.Scan(&d.ID, &d.RoomID, &d.Content, &d.CreatedAt); err != nil {
				rows.Close()
				return nil, err
			}
			all = append(all, &d)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	// Results are already ordered by created_at per room; return as-is.
	return all, nil
}

// ClearShortTerm deletes the entire STM wing for the given session
// (cascade deletes all rooms and drawers).
func (s *Store) ClearShortTerm(ctx context.Context, sessionID string) error {
	wing, err := s.GetWingByName(ctx, stmWingName(sessionID))
	if err != nil || wing == nil {
		return nil
	}
	_, err = s.db.ExecContext(ctx, "DELETE FROM wings WHERE id = ?", wing.ID)
	return err
}

// PromoteToLongTerm moves valuable short-term items to long-term memory
// under the "sessions" wing, then clears the STM wing.
func (s *Store) PromoteToLongTerm(ctx context.Context, sessionID string) error {
	drawers, err := s.RecallShortTerm(ctx, sessionID)
	if err != nil {
		return err
	}
	if len(drawers) == 0 {
		return nil
	}

	rooms := make(map[string]struct{})
	for _, d := range drawers {
		roomName := "facts"
		_, err := s.StoreByName(ctx, d.Content, "sessions", roomName, cobot.TagFacts)
		if err != nil {
			continue
		}
		rooms[roomName] = struct{}{}
	}

	// Consolidate promoted rooms.
	for room := range rooms {
		_ = s.ConsolidateByName(ctx, "sessions", room)
	}

	return s.ClearShortTerm(ctx, sessionID)
}

// StoreShortTermCompressed stores a compression summary in the STM compressed
// room for the given session. It first clears any existing drawers in the
// compressed room so that at most one drawer (the latest summary) exists.
func (s *Store) StoreShortTermCompressed(ctx context.Context, sessionID, content string) (string, error) {
	wing, err := s.GetWingByName(ctx, stmWingName(sessionID))
	if err != nil || wing == nil {
		// Wing doesn't exist yet; StoreByName will create it.
		return s.StoreByName(ctx, content, stmWingName(sessionID), stmRoomCompressed, "compressed")
	}

	// Find or create the compressed room.
	room, err := s.GetRoomByName(ctx, wing.ID, stmRoomCompressed)
	if err != nil {
		return "", fmt.Errorf("stm compressed: get room: %w", err)
	}
	if room == nil {
		// Room doesn't exist yet; StoreByName will create it.
		return s.StoreByName(ctx, content, stmWingName(sessionID), stmRoomCompressed, "compressed")
	}

	// Clear existing drawers in the compressed room.
	_, err = s.db.ExecContext(ctx, "DELETE FROM drawers WHERE room_id = ?", room.ID)
	if err != nil {
		return "", fmt.Errorf("stm compressed: clear old: %w", err)
	}

	// Store the new compressed content.
	return s.Store(ctx, content, wing.ID, room.ID)
}

// SummarizeAndPromoteSTM reads items from context, todo, notes, and observation
// rooms (NOT compressed). If total items >= 5, it promotes them to LTM under
// the "sessions" wing and deletes them from STM. The compressed room is left
// untouched.
func (s *Store) SummarizeAndPromoteSTM(ctx context.Context, sessionID string) error {
	wing, err := s.GetWingByName(ctx, stmWingName(sessionID))
	if err != nil || wing == nil {
		return nil
	}

	rooms, err := s.GetRooms(ctx, wing.ID)
	if err != nil {
		return err
	}

	// Rooms to promote (everything except compressed).
	promoteRoomNames := map[string]bool{
		stmRoomContext:     true,
		stmRoomTodo:        true,
		stmRoomNotes:       true,
		stmRoomObservation: true,
	}

	var allDrawers []*cobot.Drawer
	var roomIDsToClear []string

	for _, room := range rooms {
		if !promoteRoomNames[room.Name] {
			continue
		}
		rows, err := s.db.QueryContext(ctx, sqlSelectDrawersByRoomOrdered, room.ID)
		if err != nil {
			continue
		}
		for rows.Next() {
			var d cobot.Drawer
			if err := rows.Scan(&d.ID, &d.RoomID, &d.Content, &d.CreatedAt); err != nil {
				rows.Close()
				continue
			}
			allDrawers = append(allDrawers, &d)
		}
		rows.Close()
		roomIDsToClear = append(roomIDsToClear, room.ID)
	}

	if len(allDrawers) < 5 {
		return nil
	}

	// Promote items to LTM.
	ltmRooms := make(map[string]struct{})
	for _, d := range allDrawers {
		_, err := s.StoreByName(ctx, d.Content, "sessions", "facts", cobot.TagFacts)
		if err != nil {
			continue
		}
		ltmRooms["facts"] = struct{}{}
	}

	// Consolidate promoted LTM rooms.
	for room := range ltmRooms {
		_ = s.ConsolidateByName(ctx, "sessions", room)
	}

	// Delete promoted items from STM (clear drawers in each promoted room).
	for _, roomID := range roomIDsToClear {
		_, _ = s.db.ExecContext(ctx, "DELETE FROM drawers WHERE room_id = ?", roomID)
	}

	return nil
}
