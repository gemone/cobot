package agent

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	cobot "github.com/cobot-agent/cobot/pkg"
)

// MemoryPromoter runs LTM promotion for inactive sessions in the background.
type MemoryPromoter struct {
	stm             cobot.ShortTermMemory
	sessionsDir     string
	retentionDays   int
	archiveInterval time.Duration
	ctx             context.Context
	cancel          context.CancelFunc
}

// NewMemoryPromoter creates a MemoryPromoter that archives inactive sessions
// and promotes their content to LTM before deleting STM files.
// If archiveInterval is zero, defaults to 1 hour.
func NewMemoryPromoter(stm cobot.ShortTermMemory, sessionsDir string, retentionDays int, archiveInterval time.Duration) *MemoryPromoter {
	if archiveInterval <= 0 {
		archiveInterval = 1 * time.Hour
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &MemoryPromoter{
		stm:             stm,
		sessionsDir:     sessionsDir,
		retentionDays:   retentionDays,
		archiveInterval: archiveInterval,
		ctx:             ctx,
		cancel:          cancel,
	}
}

// Start begins the background archive loop.
func (m *MemoryPromoter) Start() {
	if m.stm == nil || m.sessionsDir == "" || m.retentionDays <= 0 {
		return
	}
	go m.loop()
}

// Stop cancels the archive loop and waits for it to exit.
func (m *MemoryPromoter) Stop() {
	m.cancel()
}

// loop runs archive on startup then every archiveInterval.
func (m *MemoryPromoter) loop() {
	m.runOnce()
	ticker := time.NewTicker(m.archiveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.runOnce()
		}
	}
}

// runOnce performs one archive pass.
func (m *MemoryPromoter) runOnce() {
	ctx := context.Background()
	entries, err := os.ReadDir(m.sessionsDir)
	if err != nil {
		slog.Warn("memory promoter: failed to read sessions dir", "err", err)
		return
	}

	cutoff := time.Now().Add(-24 * time.Hour * time.Duration(m.retentionDays)).UnixNano()

	for _, entry := range entries {
		if entry.IsDir() || !isSessionDBFile(entry.Name()) {
			continue
		}
		sessionID := stripDBExt(entry.Name())

		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().UnixNano() > cutoff {
			continue
		}

		hasContent, checkErr := sessionHasContent(m.sessionsDir, sessionID)
		if checkErr != nil {
			slog.Warn("memory promoter: failed to check session content", "session", sessionID, "err", checkErr)
			continue
		}

		if hasContent {
			if promoteErr := m.stm.SummarizeAndPromoteSTM(ctx, sessionID); promoteErr != nil {
				slog.Warn("memory promoter: failed to promote session to LTM", "session", sessionID, "err", promoteErr)
				continue
			}
		}

		deleteSessionFiles(m.sessionsDir, sessionID)
	}
}

// sessionHasContent reports whether the session DB has any drawers in the
// history or context rooms.
func sessionHasContent(sessionsDir, sessionID string) (bool, error) {
	dbPath := filepath.Join(sessionsDir, sessionID+".db")
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL")
	if err != nil {
		return false, err
	}
	defer db.Close()

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
			slog.Warn("memory promoter: failed to remove session file", "path", path, "err", err)
		}
	}
}

// isSessionDBFile returns true if the filename looks like a session STM db file.
func isSessionDBFile(name string) bool {
	return len(name) > 3 && name[len(name)-3:] == ".db"
}

// stripDBExt removes the .db extension from a session filename.
func stripDBExt(name string) string {
	if len(name) > 3 && name[len(name)-3:] == ".db" {
		return name[:len(name)-3]
	}
	return name
}
