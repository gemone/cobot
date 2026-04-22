package agent

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/cobot-agent/cobot/internal/channel"
	cobot "github.com/cobot-agent/cobot/pkg"
	"github.com/cobot-agent/cobot/pkg/broker"
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
	config     *cobot.Config
	sessionMgr *SessionManager
	provider   cobot.Provider
	registry   cobot.ModelResolver
	tools      cobot.ToolRegistry
	compressor *Compressor

	streamMu     sync.Mutex // serializes concurrent Stream calls
	streamWg     sync.WaitGroup
	bgWg         sync.WaitGroup // tracks background goroutines (STM promotion, extraction)
	compressMu   sync.Mutex     // prevents concurrent compression runs
	stmPromoteMu sync.Mutex     // prevents concurrent STM promotions

	agentCtx      context.Context
	agentCancel   context.CancelFunc
	cronScheduler CronScheduler
	channelMgr    *channel.Manager
	broker        broker.Broker
	skillSyncer   *BackgroundSkillSyncer
}

// CronScheduler is a minimal interface for stopping the cron scheduler.
// This avoids a circular dependency between agent and cron packages.
type CronScheduler interface {
	Stop()
}

func New(config *cobot.Config, toolRegistry cobot.ToolRegistry) *Agent {
	agentCtx, agentCancel := context.WithCancel(context.Background())
	return &Agent{
		config:      config,
		sessionMgr:  NewSessionManager(),
		tools:       toolRegistry,
		agentCtx:    agentCtx,
		agentCancel: agentCancel,
	}
}

func (a *Agent) SessionMgr() *SessionManager { return a.sessionMgr }

// SetSystemPrompt delegates to SessionManager. Kept on Agent to satisfy
// the cobot.SubAgent interface used by the delegate tool.
func (a *Agent) SetSystemPrompt(prompt string) error {
	return a.sessionMgr.SetSystemPrompt(prompt)
}

// GetSystemPrompt delegates to SessionManager.
func (a *Agent) GetSystemPrompt() string {
	return a.sessionMgr.GetSystemPrompt()
}

// Convenience delegation methods for frequently-used SessionManager methods.

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

func (a *Agent) RegisterTool(tool cobot.Tool) {
	a.tools.Register(tool)
}

func (a *Agent) SetCronScheduler(s CronScheduler) {
	a.cronScheduler = s
}

func (a *Agent) CronScheduler() CronScheduler {
	return a.cronScheduler
}

func (a *Agent) SetChannelManager(mgr *channel.Manager) {
	a.channelMgr = mgr
}

func (a *Agent) ChannelManager() *channel.Manager {
	return a.channelMgr
}

func (a *Agent) SetBroker(b broker.Broker) {
	a.closeBroker()
	a.broker = b
}

// SetSkillSyncer sets the background skill syncer.
func (a *Agent) SetSkillSyncer(s *BackgroundSkillSyncer) {
	a.skillSyncer = s
}

// closeBroker safely closes the current broker, logging any error.
func (a *Agent) closeBroker() {
	if a.broker != nil {
		if err := a.broker.Close(); err != nil {
			slog.Warn("close broker", "error", err)
		}
		a.broker = nil
	}
}

func (a *Agent) Context() context.Context {
	return a.agentCtx
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
	a.compressor = NewCompressor(a.sessionMgr.sessionConfig, ctxWindow, a.provider, a.config.Model)
}

func (a *Agent) Model() string {
	return a.config.Model
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
		a.bgWg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		// Force proceed after timeout rather than blocking indefinitely.
	}

	// Stop skill syncer if running.
	if a.skillSyncer != nil {
		a.skillSyncer.Stop()
	}

	// Stop cron scheduler if running.
	if a.cronScheduler != nil {
		a.cronScheduler.Stop()
	}

	// Close broker if set.
	a.closeBroker()

	// Promote valuable STM items to LTM before closing the memory store.
	sm := a.sessionMgr
	if sm.memoryStore != nil {
		if stm, ok := sm.memoryStore.(cobot.ShortTermMemory); ok {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			_ = stm.PromoteToLongTerm(ctx, sm.sessionID)
			_ = stm.ClearShortTerm(ctx, sm.sessionID)
			cancel()
		}
		if err := sm.memoryStore.Close(); err != nil {
			return fmt.Errorf("close memory store: %w", err)
		}
	}
	return nil
}
