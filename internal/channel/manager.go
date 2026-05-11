package channel

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"

	cobot "github.com/cobot-agent/cobot/pkg"
)

// Manager is a concurrency-safe 1:1 registry of channels keyed by channel ID.
type Manager struct {
	mu       sync.RWMutex
	channels map[string]cobot.Channel
}

func NewManager() *Manager {
	return &Manager{channels: make(map[string]cobot.Channel)}
}

// Register adds a channel to the manager.
// Returns an error if the channel is nil, has an empty ID, or the ID already exists.
func (m *Manager) Register(ch cobot.Channel) error {
	if ch == nil {
		return fmt.Errorf("manager: nil channel")
	}
	id := ch.ID()
	if id == "" {
		return fmt.Errorf("manager: empty channel id")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.channels[id]; exists {
		return fmt.Errorf("manager: channel %q already registered", id)
	}
	m.channels[id] = ch
	return nil
}

// Unregister removes a channel by ID. It is a no-op if the channel is absent.
func (m *Manager) Unregister(channelID string) {
	m.mu.Lock()
	delete(m.channels, channelID)
	m.mu.Unlock()
}

// Get returns a registered alive channel by ID.
func (m *Manager) Get(id string) (cobot.Channel, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ch, ok := m.channels[id]
	if !ok || !ch.IsAlive() {
		return nil, false
	}
	return ch, true
}

// AllAliveIDs returns the IDs of all alive registered channels, sorted.
func (m *Manager) AllAliveIDs() []string {
	m.mu.RLock()
	ids := make([]string, 0, len(m.channels))
	for id, ch := range m.channels {
		if ch.IsAlive() {
			ids = append(ids, id)
		}
	}
	m.mu.RUnlock()
	sort.Strings(ids)
	return ids
}

// Send delivers an outbound message to the registered alive channel.
// Returns nil, nil when the channel is not found or not alive.
func (m *Manager) Send(ctx context.Context, channelID string, msg *cobot.OutboundMessage) (*cobot.SendResult, error) {
	ch, ok := m.Get(channelID)
	if !ok {
		slog.Debug("send: channel not found or not alive", "channel", channelID)
		return nil, nil
	}
	mc, ok := ch.(cobot.MessageChannel)
	if !ok {
		slog.Debug("send: channel is not message-capable", "channel", channelID)
		return nil, nil
	}
	return mc.Send(ctx, msg)
}
