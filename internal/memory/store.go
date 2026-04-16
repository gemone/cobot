package memory

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	cobot "github.com/cobot-agent/cobot/pkg"
)

// Store provides hierarchical memory storage backed by SQLite with FTS5.
// The LTM (long-term memory) uses a shared memory.db, while each session's
// STM (short-term memory) gets its own per-session SQLite database.
type Store struct {
	db     *sql.DB              // LTM database
	stmDir string               // directory for per-session STM DBs
	stmMu  sync.Mutex           // protects stmDBs map
	stmDBs map[string]*sql.DB   // sessionID → STM DB connection
}

// OpenStore opens a SQLite-backed memory store at the given directory.
// It creates the directory and database file if they don't exist,
// enables WAL mode for concurrent reads, and ensures the schema is current.
// stmDir specifies the directory for per-session STM databases; it may be
// empty to disable STM support.
func OpenStore(memoryDir string, stmDir string) (*Store, error) {
	db, err := openDB(memoryDir)
	if err != nil {
		return nil, err
	}
	return &Store{
		db:     db,
		stmDir: stmDir,
		stmDBs: make(map[string]*sql.DB),
	}, nil
}

// Close closes the LTM database and all open STM databases.
func (s *Store) Close() error {
	var firstErr error
	// Close all STM DBs.
	s.stmMu.Lock()
	for sid, db := range s.stmDBs {
		if err := db.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		delete(s.stmDBs, sid)
	}
	s.stmMu.Unlock()

	if s.db != nil {
		if err := s.db.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// --- STM DB lifecycle helpers ---

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
	_ cobot.MemoryStore    = (*Store)(nil)
	_ cobot.MemoryRecall   = (*Store)(nil)
	_ cobot.ShortTermMemory = (*Store)(nil)
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

// storeByNameOnDB stores content using a specific DB connection instead of s.db.
// This is used for STM operations that target a per-session database.
func (s *Store) storeByNameOnDB(ctx context.Context, db *sql.DB, content, wingName, roomName, hallType string) (string, error) {
	if hallType == "" {
		hallType = cobot.TagFacts
	}
	// Create wing if not exists on the target DB.
	var wingID string
	row := db.QueryRowContext(ctx, sqlSelectWingByName, wingName)
	var w cobot.Wing
	var kwJSON string
	if err := row.Scan(&w.ID, &w.Name, &w.Type, &kwJSON); err == sql.ErrNoRows {
		wingID = newID()
		if _, err := db.ExecContext(ctx, sqlInsertWing, wingID, wingName, "", "[]"); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	} else {
		wingID = w.ID
	}

	// Create room if not exists on the target DB.
	var roomID string
	var r cobot.Room
	rRow := db.QueryRowContext(ctx, sqlSelectRoomByName, wingID, roomName)
	if err := rRow.Scan(&r.ID, &r.WingID, &r.Name, &r.HallType); err == sql.ErrNoRows {
		roomID = newID()
		if _, err := db.ExecContext(ctx, sqlInsertRoom, roomID, wingID, roomName, hallType); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	} else {
		roomID = r.ID
	}

	id := newID()
	_, err := db.ExecContext(ctx, sqlInsertDrawer, id, roomID, content, hallType, time.Now().UTC())
	if err != nil {
		return "", err
	}
	return id, nil
}

// StoreShortTerm stores a short-term memory item for the given session.
// The category determines which room the item goes into:
//   "context"/"task_state"/"decision" → "context" room
//   "todo"                            → "todo" room
//   "note"/"requirement"              → "notes" room
//   "observation"/"error"             → "observation" room
//   "compressed"                      → "compressed" room
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
func (s *Store) RecallShortTerm(ctx context.Context, sessionID string) ([]*cobot.Drawer, error) {
	stmDB, err := s.getSTMDB(sessionID)
	if err != nil {
		return nil, err
	}

	// Look up the session wing in the STM DB.
	var wingID string
	var w cobot.Wing
	var kwJSON string
	row := stmDB.QueryRowContext(ctx, sqlSelectWingByName, stmWingName)
	if err := row.Scan(&w.ID, &w.Name, &w.Type, &kwJSON); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	wingID = w.ID

	// Get all rooms for this wing.
	rows, err := stmDB.QueryContext(ctx, sqlSelectRooms, wingID)
	if err != nil {
		return nil, err
	}
	var rooms []*cobot.Room
	for rows.Next() {
		var r cobot.Room
		if err := rows.Scan(&r.ID, &r.WingID, &r.Name, &r.HallType); err != nil {
			rows.Close()
			return nil, err
		}
		rooms = append(rooms, &r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var all []*cobot.Drawer
	for _, room := range rooms {
		dRows, err := stmDB.QueryContext(ctx, sqlSelectDrawersByRoomOrdered, room.ID)
		if err != nil {
			return nil, err
		}
		for dRows.Next() {
			var d cobot.Drawer
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
	var wingID string
	var w cobot.Wing
	var kwJSON string
	row := stmDB.QueryRowContext(ctx, sqlSelectWingByName, stmWingName)
	if err := row.Scan(&w.ID, &w.Name, &w.Type, &kwJSON); err == sql.ErrNoRows {
		// Wing doesn't exist yet; storeByNameOnDB will create it.
		return s.storeByNameOnDB(ctx, stmDB, content, stmWingName, stmRoomCompressed, "compressed")
	} else if err != nil {
		return "", fmt.Errorf("stm compressed: get wing: %w", err)
	}
	wingID = w.ID

	// Find or create the compressed room.
	var roomID string
	var r cobot.Room
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
	var wingID string
	var w cobot.Wing
	var kwJSON string
	row := stmDB.QueryRowContext(ctx, sqlSelectWingByName, stmWingName)
	if err := row.Scan(&w.ID, &w.Name, &w.Type, &kwJSON); err != nil {
		// Wing doesn't exist — nothing to promote.
		return nil
	}
	wingID = w.ID

	// Get all rooms.
	rows, err := stmDB.QueryContext(ctx, sqlSelectRooms, wingID)
	if err != nil {
		return err
	}
	var rooms []*cobot.Room
	for rows.Next() {
		var r cobot.Room
		if err := rows.Scan(&r.ID, &r.WingID, &r.Name, &r.HallType); err != nil {
			rows.Close()
			return err
		}
		rooms = append(rooms, &r)
	}
	rows.Close()

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
		dRows, err := stmDB.QueryContext(ctx, sqlSelectDrawersByRoomOrdered, room.ID)
		if err != nil {
			continue
		}
		for dRows.Next() {
			var d cobot.Drawer
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
		_, _ = stmDB.ExecContext(ctx, "DELETE FROM drawers WHERE room_id = ?", roomID)
	}

	return nil
}
