package cobot

import (
	"context"
	"errors"
	"net/http"
	"sync"
)

// MessageType constants for ChannelMessage.Type.
const (
	MessageTypeCronResult = "cron_result"
)

// ChannelMessage represents a notification to be delivered to a Channel.
type ChannelMessage struct {
	Type    string // e.g. MessageTypeCronResult
	Title   string // short summary
	Content string // full content
}

// Notifier delivers ChannelMessages to a target channel by ID.
type Notifier interface {
	Notify(ctx context.Context, channelID string, msg ChannelMessage)
}

// Channel is an abstract communication endpoint that can receive notifications.
// Implementations: TUI, WeChat, Feishu, etc.
type Channel interface {
	// ID returns a unique identifier for this channel (e.g., "tui:default").
	ID() string

	// Send delivers a message to this channel.
	// Should be non-blocking or have a short timeout.
	// Send must be safe to call concurrently with Close.
	Send(ctx context.Context, msg ChannelMessage) error

	// IsAlive returns true if the channel is still connected.
	IsAlive() bool

	// Close shuts down the channel, releasing resources.
	// It is safe to call Close multiple times.
	Close()
}

// BaseChannel provides common fields and methods for Channel implementations.
// Embed it in your struct and override Send() with your delivery logic.
type BaseChannel struct {
	id    string
	alive bool
	mu    sync.RWMutex
}

// NewBaseChannel creates a BaseChannel in alive state.
func NewBaseChannel(id string) *BaseChannel {
	return &BaseChannel{id: id, alive: true}
}

func (b *BaseChannel) ID() string { return b.id }

func (b *BaseChannel) IsAlive() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.alive
}

// TryClose marks the channel as dead. Returns true if this was the first close
// (i.e. the channel was alive). Embedding structs may call TryClose from their
// own Close() implementation to decide whether to perform one-time cleanup:
//
//	func (ch *myChannel) Close() {
//	    if ch.BaseChannel.TryClose() {
//	        close(ch.done)
//	    }
//	}
func (b *BaseChannel) TryClose() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.alive {
		return false
	}
	b.alive = false
	return true
}

// Close satisfies the Channel interface. It marks the channel as dead.
func (b *BaseChannel) Close() {
	b.TryClose()
}

// CheckAlive returns context.Canceled if the channel is dead, nil otherwise.
func (b *BaseChannel) CheckAlive() error {
	b.mu.RLock()
	alive := b.alive
	b.mu.RUnlock()
	if !alive {
		return context.Canceled
	}
	return nil
}

// WithRLock runs fn while holding the read lock. Use this to safely read
// your channel's own fields that are protected by BaseChannel's mutex.
func (b *BaseChannel) WithRLock(fn func()) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	fn()
}

// WithLock runs fn while holding the write lock. Use this to safely modify
// your channel's own fields that are protected by BaseChannel's mutex.
func (b *BaseChannel) WithLock(fn func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	fn()
}

// ErrNotSupported indicates the platform does not support the requested operation.
var ErrNotSupported = errors.New("operation not supported by this platform")

// PlatformAdapter abstracts a messaging platform's capabilities.
// Each platform (Feishu, Telegram, Discord) implements this interface.
// Lifecycle: New -> Connect -> (send/recv loop) -> Disconnect
type PlatformAdapter interface {
	// Platform returns the platform identifier, e.g. "feishu", "telegram".
	Platform() string

	// Connect initializes the platform connection.
	// Returns an http.Handler that the Gateway registers at /webhook/{platform}/.
	// Return nil if the platform does not need HTTP routes.
	Connect() (http.Handler, error)

	// Disconnect releases platform resources.
	Disconnect() error

	// Send delivers an outbound message to the platform.
	Send(ctx context.Context, msg *OutboundMessage) (*SendResult, error)

	// EditMessage updates a previously sent message (for pseudo-streaming).
	// Platforms that don't support editing should return nil, ErrNotSupported.
	EditMessage(ctx context.Context, chatID, messageID, content string) (*SendResult, error)

	// OnMessage registers the inbound message callback. Must be called before Connect.
	OnMessage(handler func(ctx context.Context, msg *InboundMessage))
}
