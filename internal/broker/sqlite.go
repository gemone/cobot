package brokersqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/cobot-agent/cobot/pkg/broker"
	_ "modernc.org/sqlite"
)

// sqliteTimeFmt matches SQLite strftime('%%Y-%%m-%%d %%H:%%M:%%f', 'now') output so text comparisons work correctly.
const sqliteTimeFmt = "2006-01-02 15:04:05.000000"

// nowUTC returns the current UTC time formatted for SQLite text columns.
func nowUTC() string {
	return time.Now().UTC().Format(sqliteTimeFmt)
}

// formatTime formats a time.Time for SQLite text columns.
func formatTime(t time.Time) string {
	return t.UTC().Format(sqliteTimeFmt)
}

// sessionTTL is the duration after which a session without heartbeat is
// considered dead. Aligned with the channel manager's expiry policy
// (3 × healthCheckInterval of 30s = 90s) to avoid prematurely expiring
// sessions that are still alive from the manager's perspective.
const sessionTTL = 90 * time.Second

// messageTTL is the maximum age of a message before it is cleaned up,
// regardless of ack status. This prevents unbounded growth when consumers
// die without acking.
const messageTTL = 7 * 24 * time.Hour

// SQLiteBroker implements the broker.Broker interface using SQLite WAL mode
// for multi-process coordination.
// It corresponds to a single shared coord.db file (placed at <workspace>/coord.db).
type SQLiteBroker struct {
	db *sql.DB
}

// NewSQLiteBroker opens or creates the coordination database at dbPath.
func NewSQLiteBroker(dbPath string) (*SQLiteBroker, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, fmt.Errorf("create broker dir: %w", err)
	}
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=30000")
	if err != nil {
		return nil, fmt.Errorf("open broker db: %w", err)
	}
	db.SetMaxOpenConns(1)
	// WAL mode allows one writer and multiple readers concurrently.
	// _busy_timeout makes writers wait automatically instead of erroring.
	b := &SQLiteBroker{db: db}
	if err := b.initSchema(); err != nil {
		_ = db.Close()
		return nil, err
	}
	// Purge receipts from dead sessions on startup so they don't accumulate
	// across restarts (session IDs change every restart).
	if err := b.cleanStaleReceipts(context.Background()); err != nil {
		slog.Warn("broker startup: failed to clean stale receipts", "error", err)
	}
	return b, nil
}

func (b *SQLiteBroker) initSchema() error {
	schema := `
CREATE TABLE IF NOT EXISTS locks (
	name TEXT PRIMARY KEY,
	holder TEXT NOT NULL,
	acquired_at TEXT NOT NULL,
	expires_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
	session_id TEXT PRIMARY KEY,
	channel_id TEXT NOT NULL,
	pid INTEGER NOT NULL,
	started_at TEXT NOT NULL,
	last_heartbeat TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_sessions_channel ON sessions(channel_id);
CREATE INDEX IF NOT EXISTS idx_sessions_heartbeat ON sessions(last_heartbeat);

CREATE TABLE IF NOT EXISTS messages (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	topic TEXT NOT NULL,
	channel_id TEXT NOT NULL,
	payload BLOB NOT NULL,
	created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_messages_channel ON messages(channel_id, created_at);
CREATE INDEX IF NOT EXISTS idx_messages_created ON messages(created_at);

CREATE TABLE IF NOT EXISTS receipts (
	message_id INTEGER NOT NULL,
	session_id TEXT NOT NULL,
	acked_at TEXT NOT NULL,
	PRIMARY KEY (message_id, session_id)
);
`
	_, err := b.db.Exec(schema)
	if err != nil {
		return fmt.Errorf("init broker schema: %w", err)
	}
	return nil
}

// --- Lock implementation ---

func (b *SQLiteBroker) TryAcquire(ctx context.Context, name, holder string, ttl time.Duration) (bool, error) {
	now := time.Now().UTC()
	expires := now.Add(ttl)

	var actual string
	err := b.db.QueryRowContext(ctx, `
		INSERT INTO locks (name, holder, acquired_at, expires_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			holder = excluded.holder,
			acquired_at = excluded.acquired_at,
			expires_at = excluded.expires_at
		WHERE locks.expires_at < ?
		RETURNING holder;
	`, name, holder, formatTime(now), formatTime(expires), formatTime(now)).Scan(&actual)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return actual == holder, nil
}

func (b *SQLiteBroker) Renew(ctx context.Context, name, holder string, ttl time.Duration) error {
	now := time.Now().UTC()
	expires := now.Add(ttl)
	res, err := b.db.ExecContext(ctx, `
		UPDATE locks SET expires_at = ?
		WHERE name = ? AND holder = ? AND expires_at > ?;
	`, formatTime(expires), name, holder, formatTime(now))
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("renew lock %q: %w", name, err)
	}
	if rows == 0 {
		return fmt.Errorf("lock %q not held by %q or expired", name, holder)
	}
	return nil
}

func (b *SQLiteBroker) Release(ctx context.Context, name, holder string) error {
	_, err := b.db.ExecContext(ctx, `DELETE FROM locks WHERE name = ? AND holder = ?`, name, holder)
	return err
}

// --- SessionRegistry implementation ---

func (b *SQLiteBroker) Register(ctx context.Context, info *broker.SessionInfo) error {
	_, err := b.db.ExecContext(ctx, `
		INSERT INTO sessions (session_id, channel_id, pid, started_at, last_heartbeat)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			channel_id = excluded.channel_id,
			pid = excluded.pid,
			last_heartbeat = excluded.last_heartbeat;
	`, info.ID, info.ChannelID, info.PID, formatTime(info.StartedAt), nowUTC())
	return err
}

func (b *SQLiteBroker) Unregister(ctx context.Context, sessionID string) error {
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM receipts WHERE session_id = ?`, sessionID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE session_id = ?`, sessionID); err != nil {
		return err
	}
	return tx.Commit()
}

func (b *SQLiteBroker) Heartbeat(ctx context.Context, sessionID string) error {
	res, err := b.db.ExecContext(ctx, `
		UPDATE sessions SET last_heartbeat = ? WHERE session_id = ?;
	`, nowUTC(), sessionID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("session %q not found", sessionID)
	}
	return nil
}

func (b *SQLiteBroker) ListByChannel(ctx context.Context, channelID string) ([]*broker.SessionInfo, error) {
	threshold := formatTime(time.Now().UTC().Add(-sessionTTL))
	return b.listSessions(ctx, `SELECT session_id, channel_id, pid, started_at FROM sessions
		WHERE channel_id = ? AND last_heartbeat > ?`, channelID, threshold)
}

func (b *SQLiteBroker) ListAll(ctx context.Context) ([]*broker.SessionInfo, error) {
	threshold := formatTime(time.Now().UTC().Add(-sessionTTL))
	return b.listSessions(ctx, `SELECT session_id, channel_id, pid, started_at FROM sessions
		WHERE last_heartbeat > ?`, threshold)
}

func (b *SQLiteBroker) listSessions(ctx context.Context, query string, args ...any) ([]*broker.SessionInfo, error) {
	rows, err := b.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []*broker.SessionInfo
	for rows.Next() {
		var s broker.SessionInfo
		var startedAt string
		if err := rows.Scan(&s.ID, &s.ChannelID, &s.PID, &startedAt); err != nil {
			return nil, err
		}
		s.StartedAt, err = time.Parse(sqliteTimeFmt, startedAt)
		if err != nil {
			return nil, err
		}
		results = append(results, &s)
	}
	return results, rows.Err()
}

// --- PubSub implementation ---

func (b *SQLiteBroker) Publish(ctx context.Context, msg *broker.Message) error {
	createdAt := msg.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	_, err := b.db.ExecContext(ctx, `
		INSERT INTO messages (topic, channel_id, payload, created_at)
		VALUES (?, ?, ?, ?);
	`, msg.Topic, msg.ChannelID, msg.Payload, formatTime(createdAt))
	return err
}

func (b *SQLiteBroker) Consume(ctx context.Context, topic, channelID, sessionID string, limit int) ([]*broker.Message, error) {
	if limit <= 0 {
		limit = 50
	}
	query := `
		SELECT m.id, m.topic, m.channel_id, m.payload, m.created_at
		FROM messages m
		WHERE m.topic = ?`
	args := []any{topic}
	if channelID != "" {
		query += ` AND m.channel_id = ?`
		args = append(args, channelID)
	}
	// Filter out messages older than messageTTL so expired messages are never
	// delivered even if cleanup hasn't run yet.
	threshold := formatTime(time.Now().UTC().Add(-messageTTL))
	query += ` AND m.created_at > ?`
	args = append(args, threshold)
	query += `
		  AND NOT EXISTS (
			  SELECT 1 FROM receipts r
			  WHERE r.message_id = m.id AND r.session_id = ?
		  )
		ORDER BY m.id ASC
		LIMIT ?;`
	args = append(args, sessionID, limit)

	rows, err := b.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []*broker.Message
	for rows.Next() {
		var msg broker.Message
		var id int64
		var createdAt string
		if err := rows.Scan(&id, &msg.Topic, &msg.ChannelID, &msg.Payload, &createdAt); err != nil {
			return nil, err
		}
		msg.ID = fmt.Sprintf("%d", id)
		msg.CreatedAt, err = time.Parse(sqliteTimeFmt, createdAt)
		if err != nil {
			return nil, err
		}
		results = append(results, &msg)
	}
	return results, rows.Err()
}

// parseMsgID validates that msgID is a valid int64 to match the INTEGER
// primary key in the messages table. Returns an error for non-numeric strings
// that SQLite would otherwise silently coerce to 0.
func parseMsgID(msgID string) (int64, error) {
	n, err := strconv.ParseInt(msgID, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid message ID %q: expected numeric", msgID)
	}
	return n, nil
}

func (b *SQLiteBroker) Ack(ctx context.Context, msgID, sessionID string) error {
	id, err := parseMsgID(msgID)
	if err != nil {
		return err
	}
	_, err = b.db.ExecContext(ctx, `
		INSERT INTO receipts (message_id, session_id, acked_at)
		VALUES (?, ?, ?)
		ON CONFLICT DO NOTHING;
	`, id, sessionID, nowUTC())
	return err
}

func (b *SQLiteBroker) AckAll(ctx context.Context, msgIDs []string, sessionID string) error {
	if len(msgIDs) == 0 {
		return nil
	}
	now := nowUTC()
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, "INSERT OR IGNORE INTO receipts (message_id, session_id, acked_at) VALUES (?, ?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, rawID := range msgIDs {
		id, err := parseMsgID(rawID)
		if err != nil {
			return err
		}
		if _, err := stmt.ExecContext(ctx, id, sessionID, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// --- Cleanup and Close ---

// cleanStaleReceipts removes receipts belonging to sessions that no longer
// exist. Called on startup to prevent bloat from previous session IDs.
func (b *SQLiteBroker) cleanStaleReceipts(ctx context.Context) error {
	_, err := b.db.ExecContext(ctx, `
		DELETE FROM receipts
		WHERE session_id NOT IN (SELECT session_id FROM sessions);
	`)
	return err
}

// Cleanup deletes expired sessions and fully delivered messages.
// Typically called periodically by the leader process.
func (b *SQLiteBroker) Cleanup(ctx context.Context) error {
	now := time.Now().UTC()
	threshold := formatTime(now.Add(-sessionTTL))
	var errs []error

	// Delete sessions without heartbeat for the TTL duration.
	_, err := b.db.ExecContext(ctx, `DELETE FROM sessions WHERE last_heartbeat < ?;`, threshold)
	if err != nil {
		errs = append(errs, fmt.Errorf("cleanup sessions: %w", err))
	}

	// Delete messages older than messageTTL (7 days) regardless of ack status.
	// This prevents unbounded growth when consumers die without acking.
	messageTTLThreshold := formatTime(now.Add(-messageTTL))
	_, err = b.db.ExecContext(ctx, `DELETE FROM messages WHERE created_at < ?;`, messageTTLThreshold)
	if err != nil {
		errs = append(errs, fmt.Errorf("cleanup expired messages: %w", err))
	}

	// Delete messages that have been acked by all active sessions for that channel,
	// OR messages whose channel has no active sessions (orphaned).
	// Batched with loop to drain large backlogs within a single cleanup run.
	for i := 0; i < 100; i++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		res, err := b.db.ExecContext(ctx, `
			DELETE FROM messages
			WHERE id IN (
				SELECT m.id FROM messages m
				WHERE NOT EXISTS (
					SELECT 1 FROM sessions s
					WHERE s.channel_id = m.channel_id
					AND NOT EXISTS (
						SELECT 1 FROM receipts r
						WHERE r.message_id = m.id AND r.session_id = s.session_id
					)
				)
				LIMIT 1000
			);
		`)
		if err != nil {
			errs = append(errs, fmt.Errorf("cleanup messages: %w", err))
			break
		}
		n, err := res.RowsAffected()
		if err != nil {
			errs = append(errs, fmt.Errorf("cleanup messages rowsaffected: %w", err))
			break
		}
		if n < 1000 {
			break
		}
	}

	// Delete orphaned receipts (messages already deleted).
	_, err = b.db.ExecContext(ctx, `
		DELETE FROM receipts
		WHERE message_id NOT IN (SELECT id FROM messages);
	`)
	if err != nil {
		errs = append(errs, fmt.Errorf("cleanup orphaned receipts: %w", err))
	}

	return errors.Join(errs...)
}

// Close closes the SQLite connection.
func (b *SQLiteBroker) Close() error {
	return b.db.Close()
}

// Ensure SQLiteBroker satisfies the Broker interface.
var _ broker.Broker = (*SQLiteBroker)(nil)
