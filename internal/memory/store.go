package memory

import (
	"context"
	"database/sql"
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
)
