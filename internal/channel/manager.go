package channel

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	cobot "github.com/cobot-agent/cobot/pkg"
)

type channelEntry struct {
	ch        cobot.Channel
	sessionID string
}

// Manager tracks active channels and routes messages to them.
type Manager struct {
	mu       sync.RWMutex
	channels map[string][]channelEntry // channelID -> entries
	lastHB   map[string]time.Time      // last heartbeat timestamp per sessionID
	local    map[string]struct{}       // local sessionIDs never expire
	cancelHC context.CancelFunc        // stops health check goroutine
	hcDone   chan struct{}             // signals health check goroutine exited
}

func NewManager() *Manager {
	return &Manager{
		channels: make(map[string][]channelEntry),
		lastHB:   make(map[string]time.Time),
		local:    make(map[string]struct{}),
	}
}

// Register adds a channel to the manager and records an initial heartbeat.
// If the sessionID is already registered anywhere in the manager, the heartbeat
// is updated without adding a duplicate entry (sessionIDs are globally unique).
func (m *Manager) Register(ch cobot.Channel, sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Check for duplicate sessionID globally — update heartbeat if already registered.
	if _, ok := m.lastHB[sessionID]; ok {
		m.lastHB[sessionID] = time.Now()
		return
	}
	m.channels[ch.ID()] = append(m.channels[ch.ID()], channelEntry{ch: ch, sessionID: sessionID})
	m.lastHB[sessionID] = time.Now()
}

// Unregister removes a channel entry from the manager.
func (m *Manager) Unregister(channelID, sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entries := m.channels[channelID]
	for i, e := range entries {
		if e.sessionID == sessionID {
			// remove entry at i
			entries[i] = entries[len(entries)-1]
			entries = entries[:len(entries)-1]
			break
		}
	}
	if len(entries) == 0 {
		delete(m.channels, channelID)
	} else {
		m.channels[channelID] = entries
	}
	// Only clean up heartbeat/local state if the sessionID has no remaining registrations.
	for _, channelEntries := range m.channels {
		for _, e := range channelEntries {
			if e.sessionID == sessionID {
				return
			}
		}
	}
	delete(m.lastHB, sessionID)
	delete(m.local, sessionID)
}

// Get returns a channel by ID and whether it exists and is alive.
func (m *Manager) Get(id string) (cobot.Channel, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, e := range m.channels[id] {
		if e.ch.IsAlive() {
			return e.ch, true
		}
	}
	return nil, false
}

// AllAliveIDs returns the IDs of all alive channels.
func (m *Manager) AllAliveIDs() []string {
	m.mu.RLock()
	var ids []string
	for id, entries := range m.channels {
		for _, e := range entries {
			if e.ch.IsAlive() {
				ids = append(ids, id)
				break
			}
		}
	}
	m.mu.RUnlock()
	sort.Strings(ids)
	return ids
}

// Send fans out an outbound message to all alive registered instances for the
// given channel. The first non-nil SendResult is returned; per-entry errors are
// logged. If no alive message-capable entries exist, it returns nil, nil.
func (m *Manager) Send(ctx context.Context, channelID string, msg *cobot.OutboundMessage) (*cobot.SendResult, error) {
	m.mu.RLock()
	entries := make([]channelEntry, len(m.channels[channelID]))
	copy(entries, m.channels[channelID])
	m.mu.RUnlock()

	if len(entries) == 0 {
		slog.Debug("send: channel not found", "channel", channelID)
		return nil, nil
	}

	type sendOutcome struct {
		res *cobot.SendResult
	}
	results := make(chan sendOutcome, len(entries))

	var wg sync.WaitGroup
	for _, e := range entries {
		if !e.ch.IsAlive() {
			slog.Debug("send: skipping dead channel instance", "channel", channelID, "session", e.sessionID)
			continue
		}
		mc, ok := e.ch.(cobot.MessageChannel)
		if !ok {
			continue
		}
		wg.Add(1)
		go func(entry channelEntry, ch cobot.MessageChannel) {
			defer wg.Done()
			res, err := ch.Send(ctx, msg)
			if err != nil {
				slog.Warn("failed to deliver message",
					"channel", channelID, "session", entry.sessionID, "error", err)
			}
			results <- sendOutcome{res: res}
		}(e, mc)
	}
	wg.Wait()
	close(results)

	for outcome := range results {
		if outcome.res != nil {
			return outcome.res, nil
		}
	}

	return nil, nil
}

// Heartbeat records a heartbeat from the given session.
// Returns error if the session is not registered.
func (m *Manager) Heartbeat(sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.lastHB[sessionID]; !ok {
		return fmt.Errorf("session %q not registered", sessionID)
	}
	m.lastHB[sessionID] = time.Now()
	return nil
}

// MarkLocal marks a session as local (in-process). Local sessions are never
// expired by the health check, since they share the process lifetime.
func (m *Manager) MarkLocal(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.local[sessionID] = struct{}{}
}

// StartHealthCheck begins periodic expiry of sessions whose last heartbeat
// exceeds 3× the given interval. Dead channels are closed and unregistered.
// Call StopHealthCheck to terminate. If a previous health check is running it
// is stopped first.
func (m *Manager) StartHealthCheck(parent context.Context, interval time.Duration) {
	m.StopHealthCheck()

	if interval <= 0 {
		slog.Warn("health check interval must be positive, skipping", "interval", interval)
		return
	}

	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})

	m.mu.Lock()
	m.cancelHC = cancel
	m.hcDone = done
	m.mu.Unlock()

	timeout := interval * 3 // session is dead if no heartbeat for 3 intervals
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.expireStale(timeout)
			}
		}
	}()
}

// StopHealthCheck stops the background health check goroutine and waits for it to exit.
// It is safe to call StopHealthCheck multiple times.
func (m *Manager) StopHealthCheck() {
	m.mu.Lock()
	cancel := m.cancelHC
	done := m.hcDone
	m.cancelHC = nil
	m.hcDone = nil
	m.mu.Unlock()

	if cancel != nil {
		cancel()
		<-done
	}
}

// expireStale removes sessions whose last heartbeat exceeds the timeout.
// Local sessions are never expired.
func (m *Manager) expireStale(timeout time.Duration) {
	m.mu.Lock()
	now := time.Now()
	var toClose []cobot.Channel
	for channelID, entries := range m.channels {
		var kept []channelEntry
		for _, e := range entries {
			if _, isLocal := m.local[e.sessionID]; isLocal {
				kept = append(kept, e)
				continue
			}
			last, ok := m.lastHB[e.sessionID]
			if ok && now.Sub(last) <= timeout {
				kept = append(kept, e)
				continue
			}
			// expired
			delete(m.lastHB, e.sessionID)
			delete(m.local, e.sessionID)
			toClose = append(toClose, e.ch)
		}
		if len(kept) == 0 {
			delete(m.channels, channelID)
		} else {
			m.channels[channelID] = kept
		}
	}
	m.mu.Unlock()

	for _, ch := range toClose {
		slog.Warn("channel heartbeat timeout, removing", "channel", ch.ID())
		ch.Close()
	}
}
