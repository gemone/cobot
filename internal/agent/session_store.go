package agent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	cobot "github.com/cobot-agent/cobot/pkg"
)

// EntryType distinguishes JSONL line types within a session file.
type EntryType string

const (
	EntrySession EntryType = "session" // First line: session metadata
	EntryMessage EntryType = "message" // Chat message (user/assistant/tool)
	EntryUsage   EntryType = "usage"   // Token usage snapshot after a turn
	EntryCompact EntryType = "compact" // Compaction marker (future use)
)

// SessionEntry is a single JSONL line in a session file.
type SessionEntry struct {
	Type      EntryType      `json:"type"`
	Timestamp time.Time      `json:"ts"`
	Message   *cobot.Message `json:"message,omitempty"`
	Usage     *cobot.Usage   `json:"usage,omitempty"`
	Session   *SessionMeta   `json:"session,omitempty"`
	Compact   *CompactMarker `json:"compact,omitempty"`
}

// SessionMeta is the payload for an EntrySession line.
type SessionMeta struct {
	ID    string `json:"id"`
	Model string `json:"model"`
}

// CompactMarker records a compaction event (for future use).
type CompactMarker struct {
	Summary       string `json:"summary"`
	OriginalCount int    `json:"original_count"`
}

// SessionData is the in-memory representation of a loaded session.
type SessionData struct {
	ID        string          `json:"id"`
	Messages  []cobot.Message `json:"messages"`
	Usage     cobot.Usage     `json:"usage"`
	Model     string          `json:"model"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

type SessionStore struct {
	dir string
}

func NewSessionStore(dir string) *SessionStore {
	return &SessionStore{dir: dir}
}

// --- Write operations (append-only) ---

// InitSession writes the first line (session metadata) to a new JSONL file.
// It is idempotent: if the file already exists, it does nothing.
func (s *SessionStore) InitSession(id, model string) error {
	path := s.path(id)
	entry := SessionEntry{
		Type:      EntrySession,
		Timestamp: time.Now(),
		Session:   &SessionMeta{ID: id, Model: model},
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal session entry: %w", err)
	}
	raw = append(raw, '\n')

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND|os.O_EXCL, 0644)
	if err != nil {
		if os.IsExist(err) {
			return nil // already initialized
		}
		return fmt.Errorf("create session file: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(raw); err != nil {
		return fmt.Errorf("write session entry: %w", err)
	}
	return nil
}

// AppendMessage appends a single message entry to the session file.
func (s *SessionStore) AppendMessage(id string, msg cobot.Message) error {
	entry := SessionEntry{
		Type:      EntryMessage,
		Timestamp: time.Now(),
		Message:   &msg,
	}
	return s.appendEntry(s.path(id), entry)
}

// AppendUsage appends a usage snapshot entry to the session file.
func (s *SessionStore) AppendUsage(id string, usage cobot.Usage) error {
	entry := SessionEntry{
		Type:      EntryUsage,
		Timestamp: time.Now(),
		Usage:     &usage,
	}
	return s.appendEntry(s.path(id), entry)
}

// AppendCompact appends a compaction marker to the session file (future use).
func (s *SessionStore) AppendCompact(id string, marker CompactMarker) error {
	entry := SessionEntry{
		Type:      EntryCompact,
		Timestamp: time.Now(),
		Compact:   &marker,
	}
	return s.appendEntry(s.path(id), entry)
}

// Save provides backward-compatible full-session save. It rewrites the entire
// session as a fresh JSONL file. Prefer the append methods for incremental writes.
func (s *SessionStore) Save(id string, session *Session, usage cobot.Usage, model string) error {
	path := s.path(id)
	now := time.Now()

	// Determine creation time from existing file if present.
	createdAt := now
	if existing, err := s.readCreatedAt(id); err == nil && !existing.IsZero() {
		createdAt = existing
	}

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create session file: %w", err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)

	// Session meta line
	if err := enc.Encode(SessionEntry{
		Type:      EntrySession,
		Timestamp: createdAt,
		Session:   &SessionMeta{ID: id, Model: model},
	}); err != nil {
		return fmt.Errorf("write session meta: %w", err)
	}

	// Message lines
	for _, msg := range session.Messages() {
		if err := enc.Encode(SessionEntry{
			Type:      EntryMessage,
			Timestamp: now,
			Message:   &msg,
		}); err != nil {
			return fmt.Errorf("write message: %w", err)
		}
	}

	// Final usage line
	if err := enc.Encode(SessionEntry{
		Type:      EntryUsage,
		Timestamp: now,
		Usage:     &usage,
	}); err != nil {
		return fmt.Errorf("write usage: %w", err)
	}

	return nil
}

// --- Read operations ---

// Load reads a session file (JSONL or legacy JSON) and returns the
// reconstructed SessionData.
func (s *SessionStore) Load(id string) (*SessionData, error) {
	// Try JSONL first
	jsonlPath := s.path(id)
	if _, err := os.Stat(jsonlPath); err == nil {
		return s.loadJSONL(jsonlPath)
	}

	// Fall back to legacy .json
	jsonPath := filepath.Join(s.dir, id+".json")
	if _, err := os.Stat(jsonPath); err == nil {
		return s.loadLegacyJSON(jsonPath)
	}

	return nil, fmt.Errorf("read session: session %q not found", id)
}

func (s *SessionStore) loadJSONL(path string) (*SessionData, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open session: %w", err)
	}
	defer f.Close()

	data := &SessionData{}
	var firstTS, lastTS time.Time
	var latestUsage cobot.Usage

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024) // up to 1MB per line
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var entry SessionEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue // skip malformed lines
		}

		if firstTS.IsZero() {
			firstTS = entry.Timestamp
		}
		lastTS = entry.Timestamp

		switch entry.Type {
		case EntrySession:
			if entry.Session != nil {
				data.ID = entry.Session.ID
				data.Model = entry.Session.Model
			}
		case EntryMessage:
			if entry.Message != nil {
				data.Messages = append(data.Messages, *entry.Message)
			}
		case EntryUsage:
			if entry.Usage != nil {
				latestUsage = *entry.Usage
			}
		case EntryCompact:
			// Future: handle compaction markers
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan session: %w", err)
	}

	data.Usage = latestUsage
	data.CreatedAt = firstTS
	data.UpdatedAt = lastTS
	return data, nil
}

func (s *SessionStore) loadLegacyJSON(path string) (*SessionData, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read session: %w", err)
	}
	var data SessionData
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("unmarshal session: %w", err)
	}
	return &data, nil
}

// List returns all session IDs, recognizing both .jsonl and legacy .json files.
func (s *SessionStore) List() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("read sessions dir: %w", err)
	}

	seen := map[string]bool{}
	var ids []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		var id string
		switch {
		case strings.HasSuffix(name, ".jsonl"):
			id = name[:len(name)-6]
		case strings.HasSuffix(name, ".json"):
			id = name[:len(name)-5]
		default:
			continue
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// --- helpers ---

// readCreatedAt reads only the first line of the session JSONL to extract
// the CreatedAt timestamp, avoiding a full Load for large sessions.
func (s *SessionStore) readCreatedAt(id string) (time.Time, error) {
	path := s.path(id)
	f, err := os.Open(path)
	if err != nil {
		return time.Time{}, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if scanner.Scan() {
		var entry SessionEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err == nil {
			return entry.Timestamp, nil
		}
	}
	return time.Time{}, fmt.Errorf("no timestamp found")
}

func (s *SessionStore) path(id string) string {
	base := filepath.Base(id)
	return filepath.Join(s.dir, base+".jsonl")
}

func (s *SessionStore) appendEntry(path string, entry SessionEntry) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open session file: %w", err)
	}
	defer f.Close()

	raw, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal entry: %w", err)
	}
	raw = append(raw, '\n')
	if _, err := f.Write(raw); err != nil {
		return fmt.Errorf("write entry: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync session file: %w", err)
	}
	return nil
}
