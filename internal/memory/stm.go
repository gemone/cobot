package memory

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	cobot "github.com/cobot-agent/cobot/pkg"
)

// --- Short-term Memory (STM) ---
//
// Each session gets its own SQLite database file ({stmDir}/{sessionID}.db)
// with a single wing named "session" containing five rooms:
//   - "context"     — user directives, task state, decisions
//   - "todo"        — TODO items tracked during session
//   - "notes"       — temporary notes, user requirements
//   - "observation" — tool results, build/test outcomes, error states
//   - "compressed"  — compressed session records from compressor
//
// The database file is deleted when the session ends (after promoting
// valuable items to LTM).

const (
	stmMaxItems = 20

	stmRoomContext     = "context"     // user directives, decisions, task state
	stmRoomTodo        = "todo"        // TODO items tracked during session
	stmRoomNotes       = "notes"       // temporary notes, user requirements
	stmRoomObservation = "observation" // tool results, build/test outcomes, errors
	stmRoomCompressed  = "compressed"  // compressed session records from compressor

	stmWingName = "session" // wing name inside each per-session STM DB
)

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

// getSTMDB returns (or creates) the per-session STM database.
func (s *Store) getSTMDB(sessionID string) (*sql.DB, error) {
	s.stmMu.Lock()
	defer s.stmMu.Unlock()
	if db, ok := s.stmDBs[sessionID]; ok {
		return db, nil
	}
	db, err := openSTMDB(s.stmDir, sessionID)
	if err != nil {
		return nil, err
	}
	s.stmDBs[sessionID] = db
	return db, nil
}

// closeSTMDB closes and removes a per-session STM DB from the pool.
func (s *Store) closeSTMDB(sessionID string) error {
	s.stmMu.Lock()
	defer s.stmMu.Unlock()
	db, ok := s.stmDBs[sessionID]
	if !ok {
		return nil
	}
	delete(s.stmDBs, sessionID)
	return db.Close()
}

// getSTMWingID returns the wing ID for the session wing in the given STM DB.
// Returns ("", nil) if the wing doesn't exist.
func getSTMWingID(ctx context.Context, db *sql.DB) (string, error) {
	var w Wing
	var kwJSON string
	row := db.QueryRowContext(ctx, sqlSelectWingByName, stmWingName)
	if err := row.Scan(&w.ID, &w.Name, &w.Type, &kwJSON); err == sql.ErrNoRows {
		return "", nil
	} else if err != nil {
		return "", err
	}
	return w.ID, nil
}

// getSTMRooms returns all rooms for the given wing in the STM DB.
func getSTMRooms(ctx context.Context, db *sql.DB, wingID string) ([]*Room, error) {
	rows, err := db.QueryContext(ctx, sqlSelectRooms, wingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var rooms []*Room
	for rows.Next() {
		var r Room
		if err := rows.Scan(&r.ID, &r.WingID, &r.Name, &r.HallType); err != nil {
			return nil, err
		}
		rooms = append(rooms, &r)
	}
	return rooms, rows.Err()
}

// StoreShortTerm stores a short-term memory item for the given session.
// The category determines which room the item goes into:
//
//	"context"/"task_state"/"decision" → "context" room
//	"todo"                            → "todo" room
//	"note"/"requirement"              → "notes" room
//	"observation"/"error"             → "observation" room
//	"compressed"                      → "compressed" room
func (s *Store) StoreShortTerm(ctx context.Context, sessionID, content, category string) (string, error) {
	stmDB, err := s.getSTMDB(sessionID)
	if err != nil {
		return "", err
	}
	roomName := stmRoomForCategory(category)
	return s.storeByNameOnDB(ctx, stmDB, content, stmWingName, roomName, category)
}

// RecallShortTerm retrieves all short-term memories for the given session
// from all rooms, ordered by creation time (oldest first).
func (s *Store) RecallShortTerm(ctx context.Context, sessionID string) ([]*Drawer, error) {
	stmDB, err := s.getSTMDB(sessionID)
	if err != nil {
		return nil, err
	}

	wingID, err := getSTMWingID(ctx, stmDB)
	if err != nil || wingID == "" {
		return nil, nil
	}

	rooms, err := getSTMRooms(ctx, stmDB, wingID)
	if err != nil {
		return nil, err
	}

	var all []*Drawer
	for _, room := range rooms {
		dRows, err := stmDB.QueryContext(ctx, sqlSelectDrawersByRoomOrdered, room.ID)
		if err != nil {
			return nil, err
		}
		for dRows.Next() {
			var d Drawer
			if err := dRows.Scan(&d.ID, &d.RoomID, &d.Content, &d.CreatedAt); err != nil {
				dRows.Close()
				return nil, err
			}
			all = append(all, &d)
		}
		dRows.Close()
		if err := dRows.Err(); err != nil {
			return nil, err
		}
	}

	return all, nil
}

// ClearShortTerm closes the per-session STM database and deletes the file.
func (s *Store) ClearShortTerm(ctx context.Context, sessionID string) error {
	if err := s.closeSTMDB(sessionID); err != nil {
		return err
	}
	dbPath := filepath.Join(s.stmDir, sessionID+".db")
	return os.Remove(dbPath)
}

// PromoteToLongTerm moves valuable short-term items to long-term memory
// under the "sessions" wing, then deletes the STM database.
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
	stmDB, err := s.getSTMDB(sessionID)
	if err != nil {
		return "", err
	}

	// Find or create the wing.
	wingID, err := getSTMWingID(ctx, stmDB)
	if err != nil {
		return "", fmt.Errorf("stm compressed: get wing: %w", err)
	}
	if wingID == "" {
		// Wing doesn't exist yet; storeByNameOnDB will create it.
		return s.storeByNameOnDB(ctx, stmDB, content, stmWingName, stmRoomCompressed, "compressed")
	}

	// Find or create the compressed room.
	var roomID string
	var r Room
	rRow := stmDB.QueryRowContext(ctx, sqlSelectRoomByName, wingID, stmRoomCompressed)
	if err := rRow.Scan(&r.ID, &r.WingID, &r.Name, &r.HallType); err == sql.ErrNoRows {
		// Room doesn't exist yet; storeByNameOnDB will create it.
		return s.storeByNameOnDB(ctx, stmDB, content, stmWingName, stmRoomCompressed, "compressed")
	} else if err != nil {
		return "", fmt.Errorf("stm compressed: get room: %w", err)
	}
	roomID = r.ID

	// Clear existing drawers in the compressed room.
	_, err = stmDB.ExecContext(ctx, "DELETE FROM drawers WHERE room_id = ?", roomID)
	if err != nil {
		return "", fmt.Errorf("stm compressed: clear old: %w", err)
	}

	// Store the new compressed content.
	id := newID()
	_, err = stmDB.ExecContext(ctx, sqlInsertDrawer, id, roomID, content, "compressed", time.Now().UTC())
	if err != nil {
		return "", err
	}
	return id, nil
}

// SummarizeAndPromoteSTM reads items from context, todo, notes, and observation
// rooms (NOT compressed) in the per-session STM DB. If total items >= 5, it
// promotes them to LTM under the "sessions" wing and deletes them from STM.
// The compressed room is left untouched.
func (s *Store) SummarizeAndPromoteSTM(ctx context.Context, sessionID string) error {
	stmDB, err := s.getSTMDB(sessionID)
	if err != nil {
		return err
	}

	// Look up the session wing in the STM DB.
	wingID, err := getSTMWingID(ctx, stmDB)
	if err != nil || wingID == "" {
		// Wing doesn't exist — nothing to promote.
		return nil
	}

	// Get all rooms.
	rooms, err := getSTMRooms(ctx, stmDB, wingID)
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

	var allDrawers []*Drawer
	var roomIDsToClear []string

	for _, room := range rooms {
		if !promoteRoomNames[room.Name] {
			continue
		}
		dRows, err := stmDB.QueryContext(ctx, sqlSelectDrawersByRoomOrdered, room.ID)
		if err != nil {
			continue
		}
		for dRows.Next() {
			var d Drawer
			if err := dRows.Scan(&d.ID, &d.RoomID, &d.Content, &d.CreatedAt); err != nil {
				dRows.Close()
				continue
			}
			allDrawers = append(allDrawers, &d)
		}
		dRows.Close()
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
		if _, err := stmDB.ExecContext(ctx, "DELETE FROM drawers WHERE room_id = ?", roomID); err != nil {
			slog.Error("failed to clear promoted STM drawers", "room_id", roomID, "error", err)
		}
	}

	return nil
}
