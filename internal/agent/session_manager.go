package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	cobot "github.com/cobot-agent/cobot/pkg"
)

// SessionManager manages session state, persistence, memory, and system
// prompts — all concerns that are orthogonal to LLM interaction.
type SessionManager struct {
	session      *Session
	sessionID    string
	usageTracker *UsageTracker
	turnCount    atomic.Int64
	compressed   bool
	mu           sync.Mutex

	memoryStore        cobot.MemoryStore
	memoryRecall       cobot.MemoryRecall
	stmPromoteInterval int

	systemPrompt string
	sysPromptMu  sync.RWMutex

	sessionConfig cobot.SessionConfig

	// sessionsDir is the path to the per-session STM databases directory.
	// It is set by the bootstrap code that creates the memory store.
	sessionsDir string
}

func NewSessionManager() *SessionManager {
	return &SessionManager{
		session:            NewSession(),
		sessionID:          uuid.NewString(),
		usageTracker:       NewUsageTracker(),
		stmPromoteInterval: 10,
	}
}

func (sm *SessionManager) Session() *Session {
	return sm.session
}

func (sm *SessionManager) SessionID() string {
	return sm.sessionID
}

// AddMessage appends a message to the session and persists it to STM history room.
func (sm *SessionManager) AddMessage(m cobot.Message) {
	sm.session.AddMessage(m)
	sm.persistMessage(m)
}

func (sm *SessionManager) SetSessionConfig(sc cobot.SessionConfig) {
	sm.sessionConfig = sc
}

func (sm *SessionManager) SessionConfig() cobot.SessionConfig {
	return sm.sessionConfig
}

// persistMessage stores the message in the per-session STM DB history room.
func (sm *SessionManager) persistMessage(m cobot.Message) {
	if sm.memoryStore == nil {
		return
	}
	raw, err := json.Marshal(m)
	if err != nil {
		slog.Warn("failed to marshal message for STM", "err", err)
		return
	}
	stm, ok := sm.memoryStore.(cobot.ShortTermMemory)
	if !ok {
		return
	}
	if _, err := stm.StoreShortTerm(context.Background(), sm.sessionID, string(raw), "history"); err != nil {
		slog.Warn("failed to persist message to STM history", "err", err)
	}
}

// PersistUsage is a no-op; usage is tracked in-memory via usageTracker.
// (SessionStore was removed — history is now stored in STM.)
func (sm *SessionManager) PersistUsage() {
	// no-op: usage tracked in-memory
}

func (sm *SessionManager) SetSTMPromoteInterval(interval int) {
	sm.stmPromoteInterval = interval
}

func (sm *SessionManager) SetMemoryStore(s cobot.MemoryStore) {
	sm.memoryStore = s
}

// ResetSession creates a fresh session, a new sessionID, and prunes old STM files.
// It is called by the broker when a session is re-activated after expiring.
func (sm *SessionManager) ResetSession(stmDir string, sessionHistoryLimit int) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if err := pruneOldSessions(stmDir, "", sessionHistoryLimit); err != nil {
		slog.Warn("failed to prune old session STM files", "err", err)
	}
	sm.session = NewSession()
	sm.sessionID = uuid.NewString()
	sm.turnCount.Store(0)
	sm.compressed = false
}

func (sm *SessionManager) MemoryStore() cobot.MemoryStore {
	return sm.memoryStore
}

func (sm *SessionManager) SetMemoryRecall(r cobot.MemoryRecall) {
	sm.memoryRecall = r
}

// SetSessionsDir sets the sessions directory for the session manager.
func (sm *SessionManager) SetSessionsDir(dir string) {
	sm.sessionsDir = dir
}

// SessionsDir returns the sessions directory path.
func (sm *SessionManager) SessionsDir() string {
	return sm.sessionsDir
}

func (sm *SessionManager) MemoryRecall() cobot.MemoryRecall {
	return sm.memoryRecall
}

func (sm *SessionManager) SetSystemPrompt(prompt string) error {
	sm.sysPromptMu.Lock()
	defer sm.sysPromptMu.Unlock()
	sm.systemPrompt = prompt
	return nil
}

func (sm *SessionManager) GetSystemPrompt() string {
	sm.sysPromptMu.RLock()
	defer sm.sysPromptMu.RUnlock()
	return sm.systemPrompt
}

func (sm *SessionManager) RefreshSystemPrompt() {}

func (sm *SessionManager) TurnCount() int {
	return int(sm.turnCount.Load())
}

func (sm *SessionManager) IncTurnCount() {
	sm.turnCount.Add(1)
}

func (sm *SessionManager) GetUsage() cobot.Usage {
	return sm.usageTracker.Get()
}

func (sm *SessionManager) SessionUsage() cobot.Usage {
	return sm.usageTracker.Get()
}

func (sm *SessionManager) ResetUsage() {
	sm.usageTracker.Reset()
}

func (sm *SessionManager) SetCompressed(b bool) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.compressed = b
}

func (sm *SessionManager) IsCompressed() bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.compressed
}

// pruneOldSessions removes STM database files older than the keepCount most
// recent sessions, excluding the current sessionID.
func pruneOldSessions(stmDir, currentSessionID string, keepCount int) error {
	if keepCount <= 0 || stmDir == "" {
		return nil
	}
	entries, err := os.ReadDir(stmDir)
	if err != nil {
		return err
	}

	// Collect .db files, excluding the current session.
	type fileInfo struct {
		name    string
		modTime int64
	}
	var files []fileInfo
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".db") {
			continue
		}
		sessionID := strings.TrimSuffix(e.Name(), ".db")
		if sessionID == currentSessionID {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, fileInfo{name: e.Name(), modTime: info.ModTime().UnixNano()})
	}

	// Keep the most recent keepCount files.
	if len(files) <= keepCount {
		return nil
	}

	// Sort oldest-first.
	sort.Slice(files, func(i, j int) bool { return files[i].modTime < files[j].modTime })

	// Delete all but the most recent keepCount.
	deleteCount := len(files) - keepCount
	for i := 0; i < deleteCount; i++ {
		base := strings.TrimSuffix(files[i].name, ".db")
		deleteSessionFiles(stmDir, base)
	}
	return nil
}

// getSTMContext returns short-term memory text for the current session,
// refreshed every turn.
func (sm *SessionManager) getSTMContext(ctx context.Context) string {
	if sm.memoryStore == nil {
		return ""
	}
	stm, ok := sm.memoryStore.(cobot.ShortTermMemory)
	if !ok {
		return ""
	}
	recalled, err := stm.WakeUpSTM(ctx, sm.sessionID)
	if err != nil {
		slog.Debug("failed to recall STM", "err", err)
		return ""
	}
	return recalled
}

// ArchiveInactiveSessions archives sessions that have been inactive for longer
// than retentionDays. It promotes sessions with content to LTM before deleting
// their STM files.
func (sm *SessionManager) ArchiveInactiveSessions(ctx context.Context, retentionDays int) {
	if sm.sessionsDir == "" || retentionDays <= 0 {
		return
	}

	entries, err := os.ReadDir(sm.sessionsDir)
	if err != nil {
		slog.Warn("archive inactive sessions: failed to read sessions dir", "err", err)
		return
	}

	cutoff := time.Now().Add(-24 * time.Hour * time.Duration(retentionDays)).UnixNano()
	stm, isSTM := sm.memoryStore.(cobot.ShortTermMemory)
	if !isSTM {
		return
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".db") {
			continue
		}
		sessionID := strings.TrimSuffix(entry.Name(), ".db")
		// Skip the current active session.
		if sessionID == sm.sessionID {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().UnixNano() > cutoff {
			continue
		}

		// Session is inactive. Check if it has content in history/context.
		hasContent, checkErr := sm.sessionHasContent(sessionID)
		if checkErr != nil {
			slog.Warn("archive: failed to check session content", "session", sessionID, "err", checkErr)
			continue
		}

		if hasContent {
			// Promote to LTM before deleting.
			if promoteErr := stm.SummarizeAndPromoteSTM(ctx, sessionID); promoteErr != nil {
				slog.Warn("archive: failed to promote session to LTM", "session", sessionID, "err", promoteErr)
			}
		}

		// Delete session files: .db, .wal, .shm.
		deleteSessionFiles(sm.sessionsDir, sessionID)
	}
}

// sessionHasContent checks whether the session DB has any drawers in the
// history or context rooms.
func (sm *SessionManager) sessionHasContent(sessionID string) (bool, error) {
	dbPath := filepath.Join(sm.sessionsDir, sessionID+".db")
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL")
	if err != nil {
		return false, err
	}
	defer db.Close()

	// Count drawers in history and context rooms.
	// The wing is named "session" and rooms are named "history" and "context".
	query := `
		SELECT COUNT(*)
		FROM drawers d
		JOIN rooms r ON d.room_id = r.id
		JOIN wings w ON r.wing_id = w.id
		WHERE w.name = 'session' AND r.name IN ('history', 'context')
	`
	var count int
	if err := db.QueryRowContext(context.Background(), query).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

// deleteSessionFiles removes all files associated with a session (db, wal, shm).
func deleteSessionFiles(sessionsDir, sessionID string) {
	exts := []string{".db", ".wal", ".shm"}
	for _, ext := range exts {
		path := filepath.Join(sessionsDir, sessionID+ext)
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			slog.Warn("archive: failed to remove session file", "path", path, "err", err)
		}
	}
}
