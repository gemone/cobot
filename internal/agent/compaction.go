package agent

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/cobot-agent/cobot/internal/memory"
	cobot "github.com/cobot-agent/cobot/pkg"
)

func (a *Agent) checkAndCompress(ctx context.Context) {
	if a.compressor == nil {
		return
	}

	sm := a.sessionMgr
	action := a.compressor.Check(sm.usageTracker.Get(), int(sm.turnCount.Load()))
	if action == CompressNone {
		return
	}

	msgs, snapshotLen := sm.session.MessagesSnapshot()
	slog.Debug("compression triggered", "action", action, "turns", sm.turnCount.Load(), "total_tokens", sm.usageTracker.Get().TotalTokens, "messages", len(msgs))

	go a.runCompress(ctx, action, msgs, snapshotLen)
}

// promoteSTMBackground triggers an asynchronous STM→LTM promotion.
func (a *Agent) promoteSTMBackground(ctx context.Context) {
	sm := a.sessionMgr
	if sm.memoryStore == nil {
		return
	}
	stm, ok := sm.memoryStore.(cobot.ShortTermMemory)
	if !ok {
		return
	}
	// Prevent concurrent promotions for the same session.
	if !a.stmPromoteMu.TryLock() {
		slog.Debug("STM promotion already in progress, skipping")
		return
	}
	defer a.stmPromoteMu.Unlock()

	a.bgWg.Add(1)
	go func() {
		defer a.bgWg.Done()
		// Use agent-level context with its own timeout, NOT the request ctx.
		// The request ctx is cancelled when finishStream() is called.
		// agentCtx lives for the lifetime of the Agent.
		promoCtx, cancel := context.WithTimeout(a.agentCtx, 2*time.Minute)
		defer cancel()
		if err := stm.SummarizeAndPromoteSTM(promoCtx, sm.sessionID); err != nil {
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

	var summary string
	var kept []cobot.Message

	switch action {
	case CompressSummarize:
		var err error
		summary, kept, err = a.compressor.Summarize(ctx, msgs)
		if err != nil {
			slog.Debug("summarize failed", "err", err)
			return
		}
	case CompressFull:
		var err error
		summary, err = a.compressor.Compress(ctx, msgs)
		if err != nil {
			slog.Debug("compress failed", "err", err)
			return
		}
	}

	optimized, err := a.compressor.OptimizeSummary(ctx, summary, msgs)
	if err == nil && optimized != "" {
		summary = optimized
	}
	a.replaceSessionMessages(summary, kept, snapshotLen)
	a.extractMemories(ctx, summary, msgs)
	sm := a.sessionMgr
	if stm, ok := sm.memoryStore.(cobot.ShortTermMemory); ok {
		if _, err := stm.StoreShortTermCompressed(ctx, sm.sessionID, summary); err != nil {
			slog.Debug("stm compressed store failed", "err", err)
		}
	}
}

func (a *Agent) replaceSessionMessages(summary string, kept []cobot.Message, snapshotLen int) {
	sm := a.sessionMgr
	sess := sm.session
	sess.mu.Lock()
	defer sess.mu.Unlock()

	var postSnapshot []cobot.Message
	if snapshotLen < len(sess.messages) {
		postSnapshot = make([]cobot.Message, len(sess.messages)-snapshotLen)
		copy(postSnapshot, sess.messages[snapshotLen:])
	}

	var newMsgs []cobot.Message
	if len(sess.messages) > 0 && sess.messages[0].Role == cobot.RoleSystem {
		newMsgs = append(newMsgs, sess.messages[0])
	}

	newMsgs = append(newMsgs, cobot.Message{
		Role:    cobot.RoleAssistant,
		Content: fmt.Sprintf("[Previous conversation summary]\n%s", summary),
	})
	newMsgs = append(newMsgs, kept...)
	newMsgs = append(newMsgs, postSnapshot...)
	originalCount := len(sess.messages)
	sess.messages = newMsgs

	newUsage := estimateMessagesUsage(newMsgs)
	sm.usageTracker.Set(newUsage)

	slog.Debug("session compressed", "original_msgs", originalCount, "new_msgs", len(newMsgs), "new_tokens", newUsage.TotalTokens)
}

// --- Memory extraction (post-compression) ---

func (a *Agent) extractMemories(ctx context.Context, summary string, originalMsgs []cobot.Message) {
	store := a.sessionMgr.memoryStore
	if store == nil || a.provider == nil {
		return
	}

	model := a.compressorModel()
	extractor := memory.NewExtractor(store, a.provider, model)

	a.bgWg.Add(1)
	go func() {
		defer a.bgWg.Done()
		if err := extractor.Extract(ctx, summary, originalMsgs); err != nil {
			slog.Debug("memory extraction failed", "err", err)
		}
	}()
}

func (a *Agent) compressorModel() string {
	if a.compressor != nil && a.compressor.summaryModel != "" {
		return a.compressor.summaryModel
	}
	return a.config.Model
}
