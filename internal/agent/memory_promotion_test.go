package agent

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cobot-agent/cobot/internal/memory"
	cobot "github.com/cobot-agent/cobot/pkg"
)

// TestPromoteSTMBackground_DetachesFromRequestContext verifies that STM promotion
// completes even when the original request context is cancelled immediately.
func TestPromoteSTMBackground_DetachesFromRequestContext(t *testing.T) {
	// Setup: create a temp dir for memory store
	tmpDir := t.TempDir()
	stmDir := filepath.Join(tmpDir, "stm")
	os.MkdirAll(stmDir, 0755)
	ltmDBPath := filepath.Join(tmpDir, "memory.db") // OpenStore creates memory.db inside tmpDir

	store, err := memory.OpenStore(tmpDir, stmDir)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()
	store.SetSTMPromoteThreshold(3)

	sessionID := "test-session-" + t.Name()

	// Seed 5 STM items so promotion threshold (3) is met
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		_, err := store.StoreShortTerm(ctx, sessionID, "test item "+string(rune('0'+i)), "context")
		if err != nil {
			t.Fatalf("StoreShortTerm: %v", err)
		}
	}

	// Create agent with the memory store
	cfg := &cobot.Config{Model: "test"}
	a := New(cfg, newTestRegistry())
	a.sessionMgr.memoryStore = store
	a.sessionMgr.sessionID = sessionID
	a.agentCtx, a.agentCancel = context.WithCancel(context.Background())

	// Trigger promotion with an already-cancelled context
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	a.promoteSTMBackground(cancelledCtx)

	// Wait for promotion to complete (it should, since it uses agentCtx not cancelledCtx)
	time.Sleep(3 * time.Second)

	// Verify LTM was written using direct SQL query.
	count := countLTMDrawers(t, ltmDBPath, "sessions", "facts")
	if count == 0 {
		t.Error("expected LTM drawers after promotion, got 0")
	}
}

// TestAgentClose_FlushesRemainingSTM verifies that Agent.Close() promotes
// remaining STM to LTM even when no mid-session promotion ran.
func TestAgentClose_FlushesRemainingSTM(t *testing.T) {
	tmpDir := t.TempDir()
	stmDir := filepath.Join(tmpDir, "stm")
	ltmDir := tmpDir // OpenStore creates memory.db inside ltmDir
	os.MkdirAll(stmDir, 0755)
	ltmDBPath := filepath.Join(tmpDir, "memory.db") // actual LTM DB path

	store, err := memory.OpenStore(ltmDir, stmDir)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()
	store.SetSTMPromoteThreshold(2)

	sessionID := "test-close-session-" + t.Name()
	ctx := context.Background()

	// Seed 3 STM items
	for i := 0; i < 3; i++ {
		_, err := store.StoreShortTerm(ctx, sessionID, "close test item", "context")
		if err != nil {
			t.Fatalf("StoreShortTerm: %v", err)
		}
	}

	cfg := &cobot.Config{Model: "test"}
	a := New(cfg, newTestRegistry())
	a.sessionMgr.memoryStore = store
	a.sessionMgr.sessionID = sessionID
	a.agentCtx, a.agentCancel = context.WithCancel(context.Background())

	// Use mockProvider from loop_test.go - it is visible in this package
	a.provider = &mockProvider{
		responses: []*cobot.ProviderResponse{
			{Content: "ok", StopReason: cobot.StopEndTurn},
		},
	}

	// Close the agent
	if err := a.Close(); err != nil {
		t.Fatalf("Agent.Close: %v", err)
	}

	// Verify LTM was written
	count := countLTMDrawers(t, ltmDBPath, "sessions", "facts")
	if count == 0 {
		t.Error("expected LTM drawers after Close, got 0")
	}
}

// countLTMDrawers queries the LTM DB directly to count drawers in a wing/room.
// This avoids depending on a non-existent RecallLongTerm method.
func countLTMDrawers(t *testing.T, ltmPath, wingName, roomName string) int {
	t.Helper()
	db, err := sql.Open("sqlite", ltmPath)
	if err != nil {
		t.Fatalf("open ltm db: %v", err)
	}
	defer db.Close()

	var count int
	query := `
		SELECT COUNT(*)
		FROM drawers d
		JOIN rooms r ON d.room_id = r.id
		JOIN wings w ON r.wing_id = w.id
		WHERE w.name = ? AND r.name = ?
	`
	if err := db.QueryRowContext(context.Background(), query, wingName, roomName).Scan(&count); err != nil {
		t.Fatalf("query drawers: %v", err)
	}
	return count
}
