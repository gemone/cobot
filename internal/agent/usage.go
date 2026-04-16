package agent

import (
	"sync"

	cobot "github.com/cobot-agent/cobot/pkg"
)

// UsageTracker accumulates token usage across multiple agent turns.
type UsageTracker struct {
	mu    sync.RWMutex
	total cobot.Usage
}

func NewUsageTracker() *UsageTracker {
	return &UsageTracker{}
}

func (t *UsageTracker) Add(u cobot.Usage) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.total.PromptTokens += u.PromptTokens
	t.total.CompletionTokens += u.CompletionTokens
	t.total.TotalTokens += u.TotalTokens
	t.total.ReasoningTokens += u.ReasoningTokens
	t.total.CacheReadTokens += u.CacheReadTokens
	t.total.CacheWriteTokens += u.CacheWriteTokens
}

func (t *UsageTracker) Get() cobot.Usage {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.total
}

func (t *UsageTracker) Set(u cobot.Usage) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.total = u
}

func (t *UsageTracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.total = cobot.Usage{}
}
