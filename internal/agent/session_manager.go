package agent

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"

	cobot "github.com/cobot-agent/cobot/pkg"
)

// SessionManager manages session state, persistence, memory, and system
// prompts — all concerns that are orthogonal to LLM interaction.
type SessionManager struct {
	session      *Session
	sessionID    string
	sessionStore *SessionStore
	usageTracker *UsageTracker
	turnCount    atomic.Int64

	memoryStore        cobot.MemoryStore
	memoryRecall       cobot.MemoryRecall
	stmPromoteInterval int

	systemPrompt string
	sysPromptMu  sync.RWMutex

	sessionConfig cobot.SessionConfig
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

// AddMessage appends a message to the session and persists it.
// The model parameter is used for session file initialization.
func (sm *SessionManager) AddMessage(m cobot.Message, model string) {
	sm.session.AddMessage(m)
	sm.persistMessage(m, model)
}

func (sm *SessionManager) SetSessionStore(s *SessionStore) {
	sm.sessionStore = s
}

func (sm *SessionManager) SetSessionConfig(sc cobot.SessionConfig) {
	sm.sessionConfig = sc
}

func (sm *SessionManager) SessionConfig() cobot.SessionConfig {
	return sm.sessionConfig
}

// persistMessage appends a single message to the session JSONL file.
func (sm *SessionManager) persistMessage(m cobot.Message, model string) {
	if sm.sessionStore == nil {
		return
	}
	if err := sm.sessionStore.InitSession(sm.sessionID, model); err != nil {
		slog.Warn("failed to init session", "err", err)
		return
	}
	if err := sm.sessionStore.AppendMessage(sm.sessionID, m); err != nil {
		slog.Warn("failed to persist message", "err", err)
	}
}

// PersistUsage appends a usage snapshot to the session JSONL file.
func (sm *SessionManager) PersistUsage() {
	if sm.sessionStore == nil {
		return
	}
	if err := sm.sessionStore.AppendUsage(sm.sessionID, sm.usageTracker.Get()); err != nil {
		slog.Warn("failed to persist usage", "err", err)
	}
}

func (sm *SessionManager) SetSTMPromoteInterval(interval int) {
	sm.stmPromoteInterval = interval
}

func (sm *SessionManager) SetMemoryStore(s cobot.MemoryStore) {
	sm.memoryStore = s
}

func (sm *SessionManager) MemoryStore() cobot.MemoryStore {
	return sm.memoryStore
}

func (sm *SessionManager) SetMemoryRecall(r cobot.MemoryRecall) {
	sm.memoryRecall = r
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

func (sm *SessionManager) SessionUsage() cobot.Usage {
	return sm.usageTracker.Get()
}

func (sm *SessionManager) ResetUsage() {
	sm.usageTracker.Reset()
}

// --- Context / prompt helpers (moved from messages.go) ---

// getSystemPrompt resolves the system prompt, lazily loading from memory
// on first access when no prompt has been set explicitly.
func (sm *SessionManager) getSystemPrompt(ctx context.Context) string {
	sm.sysPromptMu.RLock()
	cached := sm.systemPrompt
	sm.sysPromptMu.RUnlock()

	if cached != "" {
		return cached
	}

	if sm.memoryRecall == nil {
		return cobot.DefaultSystemPrompt
	}

	// Double-check locking: acquire write lock and re-check to avoid
	// redundant WakeUp calls from concurrent cache misses.
	sm.sysPromptMu.Lock()
	if sm.systemPrompt != "" {
		sm.sysPromptMu.Unlock()
		return sm.systemPrompt
	}

	wakeUp, err := sm.memoryRecall.WakeUp(ctx)
	if err != nil || wakeUp == "" {
		sm.sysPromptMu.Unlock()
		return cobot.DefaultSystemPrompt
	}

	sm.systemPrompt = wakeUp
	sm.sysPromptMu.Unlock()

	return wakeUp
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
	text, err := stm.WakeUpSTM(ctx, sm.sessionID)
	if err != nil {
		return ""
	}
	return text
}
