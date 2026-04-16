package agent

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	cobot "github.com/cobot-agent/cobot/pkg"
)

// --- Session ---

const maxMessages = 1000

type Session struct {
	mu       sync.RWMutex
	messages []cobot.Message
}

func NewSession() *Session {
	return &Session{}
}

func (s *Session) Messages() []cobot.Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]cobot.Message, len(s.messages))
	copy(out, s.messages)
	return out
}

// MessagesSnapshot returns a copy of the current messages along with the
// length at the time of the snapshot. This allows callers to later merge
// any messages appended after the snapshot was taken.
func (s *Session) MessagesSnapshot() ([]cobot.Message, int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := len(s.messages)
	out := make([]cobot.Message, n)
	copy(out, s.messages)
	return out, n
}

func (s *Session) AddMessage(m cobot.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, m)
	if len(s.messages) > maxMessages {
		if len(s.messages) > 0 && s.messages[0].Role == cobot.RoleSystem {
			keep := s.messages[len(s.messages)-(maxMessages-1):]
			kept := make([]cobot.Message, 0, maxMessages)
			kept = append(kept, s.messages[0])
			kept = append(kept, keep...)
			s.messages = kept
		} else {
			s.messages = s.messages[len(s.messages)-maxMessages:]
		}
	}
}

// --- Agent ---

type Agent struct {
	config             *cobot.Config
	sessionConfig      cobot.SessionConfig
	provider           cobot.Provider
	registry           cobot.ModelResolver
	tools              cobot.ToolRegistry
	session            *Session
	sessionID          string
	sessionStore       *SessionStore
	memoryStore        cobot.MemoryStore
	memoryRecall       cobot.MemoryRecall
	usageTracker       *UsageTracker
	systemPrompt       string
	sysPromptMu        sync.RWMutex
	streamMu           sync.Mutex // serializes concurrent Stream calls
	streamWg           sync.WaitGroup
	agentCtx           context.Context
	agentCancel        context.CancelFunc
	turnCount          int
	compressor         *Compressor
	compressMu         sync.Mutex // prevents concurrent compression runs
	stmPromoteInterval int        // turns between STM promotions (0 = disabled)
}

func New(config *cobot.Config, toolRegistry cobot.ToolRegistry) *Agent {
	agentCtx, agentCancel := context.WithCancel(context.Background())
	return &Agent{
		config:             config,
		sessionConfig:      config.Session,
		tools:              toolRegistry,
		session:            NewSession(),
		sessionID:          uuid.New().String(),
		usageTracker:       NewUsageTracker(),
		agentCtx:           agentCtx,
		agentCancel:        agentCancel,
		stmPromoteInterval: 10,
	}
}

func (a *Agent) SetSystemPrompt(prompt string) error {
	a.sysPromptMu.Lock()
	defer a.sysPromptMu.Unlock()
	a.systemPrompt = prompt
	return nil
}

func (a *Agent) SetProvider(p cobot.Provider) {
	a.provider = p
}

func (a *Agent) SetRegistry(r cobot.ModelResolver) {
	a.registry = r
}

func (a *Agent) Registry() cobot.ModelResolver {
	return a.registry
}

func (a *Agent) ToolRegistry() cobot.ToolRegistry {
	return a.tools
}

func (a *Agent) Session() *Session {
	return a.session
}

func (a *Agent) AddMessage(m cobot.Message) {
	a.session.AddMessage(m)
	a.persistMessage(m)
}

func (a *Agent) SetSessionStore(s *SessionStore) {
	a.sessionStore = s
}

func (a *Agent) SetSessionConfig(sc cobot.SessionConfig) {
	a.sessionConfig = sc
}

func (a *Agent) SessionConfig() cobot.SessionConfig {
	return a.sessionConfig
}

func (a *Agent) SessionID() string {
	return a.sessionID
}

func (a *Agent) persistSession() {
	if a.sessionStore == nil {
		return
	}
	if err := a.sessionStore.Save(a.sessionID, a.session, a.usageTracker.Get(), a.config.Model); err != nil {
		slog.Debug("failed to persist session", "err", err)
	}
}

// persistMessage appends a single message to the session JSONL file.
func (a *Agent) persistMessage(m cobot.Message) {
	if a.sessionStore == nil {
		return
	}
	if err := a.sessionStore.InitSession(a.sessionID, a.config.Model); err != nil {
		slog.Debug("failed to init session", "err", err)
		return
	}
	if err := a.sessionStore.AppendMessage(a.sessionID, m); err != nil {
		slog.Debug("failed to persist message", "err", err)
	}
}

// PersistUsage appends a usage snapshot to the session JSONL file.
func (a *Agent) PersistUsage() {
	if a.sessionStore == nil {
		return
	}
	if err := a.sessionStore.AppendUsage(a.sessionID, a.usageTracker.Get()); err != nil {
		slog.Debug("failed to persist usage", "err", err)
	}
}

func (a *Agent) RegisterTool(tool cobot.Tool) {
	a.tools.Register(tool)
}

func (a *Agent) SetToolRegistry(r cobot.ToolRegistry) {
	a.tools = r
}

func (a *Agent) SetMemoryStore(s cobot.MemoryStore) {
	a.memoryStore = s
}

func (a *Agent) MemoryStore() cobot.MemoryStore {
	return a.memoryStore
}

func (a *Agent) SetMemoryRecall(r cobot.MemoryRecall) {
	a.memoryRecall = r
}

func (a *Agent) MemoryRecall() cobot.MemoryRecall {
	return a.memoryRecall
}

func (a *Agent) Config() *cobot.Config {
	return a.config
}

func (a *Agent) Provider() cobot.Provider {
	return a.provider
}

func (a *Agent) SetModel(modelSpec string) error {
	if a.registry != nil {
		p, modelName, err := a.registry.ProviderForModel(modelSpec)
		if err != nil {
			return err
		}
		if v, ok := p.(cobot.ModelValidator); ok {
			if err := v.ValidateModel(a.agentCtx, modelName); err != nil {
				return err
			}
		}
		a.provider = p
		a.config.Model = modelName
		a.initCompressor()
		return nil
	}
	a.config.Model = modelSpec
	a.initCompressor()
	return nil
}

func (a *Agent) initCompressor() {
	if a.provider == nil {
		return
	}
	ctxWindow := ContextWindowForModel(a.config.Model, nil)
	a.compressor = NewCompressor(a.sessionConfig, ctxWindow, a.provider, a.config.Model)
}

func (a *Agent) Model() string {
	return a.config.Model
}

func (a *Agent) SessionUsage() cobot.Usage {
	return a.usageTracker.Get()
}

func (a *Agent) ResetUsage() {
	a.usageTracker.Reset()
}

func (a *Agent) checkAndCompress(ctx context.Context) {
	if a.compressor == nil {
		return
	}

	action := a.compressor.Check(a.usageTracker.Get(), a.turnCount)
	if action == CompressNone {
		return
	}

	msgs, snapshotLen := a.session.MessagesSnapshot()
	slog.Debug("compression triggered", "action", action, "turns", a.turnCount, "total_tokens", a.usageTracker.Get().TotalTokens, "messages", len(msgs))

	go a.runCompress(ctx, action, msgs, snapshotLen)
}

// promoteSTMBackground triggers an asynchronous STM→LTM promotion.
func (a *Agent) promoteSTMBackground(ctx context.Context) {
	if a.memoryStore == nil {
		return
	}
	stm, ok := a.memoryStore.(cobot.ShortTermMemory)
	if !ok {
		return
	}
	go func() {
		if err := stm.SummarizeAndPromoteSTM(ctx, a.sessionID); err != nil {
			slog.Debug("periodic STM promotion failed", "err", err)
		}
	}()
}

func (a *Agent) runCompress(ctx context.Context, action CompressAction, msgs []cobot.Message, snapshotLen int) {
	if !a.compressMu.TryLock() {
		slog.Debug("compression already in progress, skipping")
		return
	}
	defer a.compressMu.Unlock()

	switch action {
	case CompressSummarize:
		summary, kept, err := a.compressor.Summarize(ctx, msgs)
		if err != nil {
			slog.Debug("summarize failed", "err", err)
			return
		}
		optimized, err := a.compressor.OptimizeSummary(ctx, summary, msgs)
		if err == nil && optimized != "" {
			summary = optimized
		}
		a.replaceSessionMessages(summary, kept, snapshotLen)
		a.extractMemories(ctx, summary, msgs)
		// Store compression summary in STM compressed room.
		if stm, ok := a.memoryStore.(cobot.ShortTermMemory); ok {
			if _, err := stm.StoreShortTermCompressed(ctx, a.sessionID, summary); err != nil {
				slog.Debug("stm compressed store failed", "err", err)
			}
		}

	case CompressFull:
		summary, err := a.compressor.Compress(ctx, msgs)
		if err != nil {
			slog.Debug("compress failed", "err", err)
			return
		}
		optimized, err := a.compressor.OptimizeSummary(ctx, summary, msgs)
		if err == nil && optimized != "" {
			summary = optimized
		}
		a.replaceSessionMessages(summary, nil, snapshotLen)
		a.extractMemories(ctx, summary, msgs)
		// Store compression summary in STM compressed room.
		if stm, ok := a.memoryStore.(cobot.ShortTermMemory); ok {
			if _, err := stm.StoreShortTermCompressed(ctx, a.sessionID, summary); err != nil {
				slog.Debug("stm compressed store failed", "err", err)
			}
		}
	}
}

func (a *Agent) replaceSessionMessages(summary string, kept []cobot.Message, snapshotLen int) {
	a.session.mu.Lock()
	defer a.session.mu.Unlock()

	// Collect messages appended after the snapshot was taken so they aren't lost.
	var postSnapshot []cobot.Message
	if snapshotLen < len(a.session.messages) {
		postSnapshot = make([]cobot.Message, len(a.session.messages)-snapshotLen)
		copy(postSnapshot, a.session.messages[snapshotLen:])
	}

	var newMsgs []cobot.Message
	if len(a.session.messages) > 0 && a.session.messages[0].Role == cobot.RoleSystem {
		newMsgs = append(newMsgs, a.session.messages[0])
	}

	newMsgs = append(newMsgs, cobot.Message{
		Role:    cobot.RoleAssistant,
		Content: fmt.Sprintf("[Previous conversation summary]\n%s", summary),
	})
	newMsgs = append(newMsgs, kept...)
	newMsgs = append(newMsgs, postSnapshot...)
	originalCount := len(a.session.messages)
	a.session.messages = newMsgs

	newUsage := estimateMessagesUsage(newMsgs)
	a.usageTracker.Set(newUsage)

	if a.sessionStore != nil {
		a.sessionStore.AppendCompact(a.sessionID, CompactMarker{
			Summary:       summary,
			OriginalCount: originalCount,
		})
		a.PersistUsage()
	}

	slog.Debug("session compressed", "original_msgs", originalCount, "new_msgs", len(newMsgs), "new_tokens", newUsage.TotalTokens)
}

// deriveCtx returns a context derived from agentCtx that also cancels if the
// supplied ctx cancels. This ensures that agent-level cancellation (via Close)
// propagates into all in-flight Prompt/Stream calls.
func (a *Agent) deriveCtx(ctx context.Context) context.Context {
	derived, derivedCancel := context.WithCancel(a.agentCtx)

	go func() {
		select {
		case <-ctx.Done():
			derivedCancel()
		case <-a.agentCtx.Done():
			derivedCancel()
		}
	}()

	return derived
}

func (a *Agent) Close() error {
	if a.agentCancel != nil {
		a.agentCancel()
	}

	done := make(chan struct{})
	go func() {
		a.streamWg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		// Force proceed after timeout rather than blocking indefinitely.
	}

	// Promote valuable STM items to LTM before closing the memory store.
	if a.memoryStore != nil {
		if stm, ok := a.memoryStore.(cobot.ShortTermMemory); ok {
			go func() {
				_ = stm.PromoteToLongTerm(context.Background(), a.sessionID)
				_ = stm.ClearShortTerm(context.Background(), a.sessionID)
			}()
		}
		// Give background promotion a moment to finish.
		time.Sleep(100 * time.Millisecond)
		if err := a.memoryStore.Close(); err != nil {
			return fmt.Errorf("close memory store: %w", err)
		}
	}
	return nil
}

// --- Context / prompt helpers ---

// buildMessages assembles the message list for the LLM, prepending the system
// prompt (cached LTM + fresh STM) as the first message.
func (a *Agent) buildMessages(ctx context.Context) []cobot.Message {
	msgs := a.session.Messages()
	system := a.getSystemPrompt(ctx)
	if system == "" {
		return msgs
	}

	// Append STM context on every turn (not cached like LTM).
	stmText := a.getSTMContext(ctx)
	if stmText != "" {
		system = system + "\n\n" + stmText
	}

	return append([]cobot.Message{{Role: cobot.RoleSystem, Content: system}}, msgs...)
}

// getSTMContext returns short-term memory text for the current session,
// refreshed every turn.
func (a *Agent) getSTMContext(ctx context.Context) string {
	if a.memoryStore == nil {
		return ""
	}
	stm, ok := a.memoryStore.(cobot.ShortTermMemory)
	if !ok {
		return ""
	}
	text, err := stm.WakeUpSTM(ctx, a.sessionID)
	if err != nil {
		return ""
	}
	return text
}

func (a *Agent) getSystemPrompt(ctx context.Context) string {
	a.sysPromptMu.RLock()
	cached := a.systemPrompt
	a.sysPromptMu.RUnlock()

	if cached != "" {
		return cached
	}

	if a.memoryRecall == nil {
		return cobot.DefaultSystemPrompt
	}

	// Double-check locking: acquire write lock and re-check to avoid
	// redundant WakeUp calls from concurrent cache misses.
	a.sysPromptMu.Lock()
	if a.systemPrompt != "" {
		a.sysPromptMu.Unlock()
		return a.systemPrompt
	}

	wakeUp, err := a.memoryRecall.WakeUp(ctx)
	if err != nil || wakeUp == "" {
		a.sysPromptMu.Unlock()
		return cobot.DefaultSystemPrompt
	}

	a.systemPrompt = wakeUp
	a.sysPromptMu.Unlock()

	return wakeUp
}
