package memory

import (
	"context"
	"database/sql"
	"sync"
	"time"

	cobot "github.com/cobot-agent/cobot/pkg"
)

// Store provides hierarchical memory storage backed by SQLite with FTS5.
// Both LTM (long-term memory) and per-session STM (short-term memory)
// databases live in the same directory.
type Store struct {
	db                  *sql.DB            // LTM database
	stmDir              string             // directory for per-session STM DBs
	stmMu               sync.Mutex         // protects stmDBs map
	stmDBs              map[string]*sql.DB // sessionID → STM DB connection
	summarizer          *Summarizer        // optional LLM-powered summarizer for STM→LTM
	stmPromoteThreshold int                // minimum STM items to trigger mid-session promotion
}

// OpenStore opens a SQLite-backed memory store.
// dataDir is the workspace root where memory.db (LTM) will be created.
// sessionsDir is where per-session STM databases ({sessionID}.db) are stored.
func OpenStore(dataDir string, sessionsDir string) (*Store, error) {
	db, err := openDB(dataDir)
	if err != nil {
		return nil, err
	}
	return &Store{
		db:     db,
		stmDir: sessionsDir,
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

// SetSummarizer sets the LLM-powered summarizer for smart STM→LTM promotion.
func (s *Store) SetSummarizer(summarizer *Summarizer) {
	s.summarizer = summarizer
}

// SetSTMPromoteThreshold sets the minimum number of STM items required
// before a mid-session promotion is attempted. Zero defaults to 5.
func (s *Store) SetSTMPromoteThreshold(threshold int) {
	s.stmPromoteThreshold = threshold
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
	return s.storeByNameOnDB(ctx, s.db, content, wingName, roomName, hallType)
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
	_ cobot.MemoryStore     = (*Store)(nil)
	_ cobot.MemoryRecall    = (*Store)(nil)
	_ cobot.ShortTermMemory = (*Store)(nil)
)

// storeByNameOnDB stores content using a specific DB connection instead of s.db.
// This is used for STM operations that target a per-session database.
func (s *Store) storeByNameOnDB(ctx context.Context, db *sql.DB, content, wingName, roomName, hallType string) (string, error) {
	if hallType == "" {
		hallType = cobot.TagFacts
	}
	// Create wing if not exists on the target DB.
	var wingID string
	row := db.QueryRowContext(ctx, sqlSelectWingByName, wingName)
	var w Wing
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
	var r Room
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
