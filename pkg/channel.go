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
// Implementations: TUI, Feishu, Reverse, etc.
type Channel interface {
	// ID returns a unique identifier for this channel (e.g., "tui:default").
	ID() string

	// Send delivers a notification message to this channel.
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

// MessageChannel extends Channel with bidirectional IM communication.
// Implementations: Feishu, Reverse, etc.
// The Gateway registers OnMessage handlers and calls SendMessage for replies.
type MessageChannel interface {
	Channel

	// Platform returns the platform identifier, e.g. "feishu", "reverse".
	Platform() string

	// OnMessage registers the inbound message callback. Must be called before
	// the Gateway starts processing messages.
	OnMessage(handler func(ctx context.Context, msg *InboundMessage))

	// OnEvent registers a callback for platform-specific system events
	// (e.g. message reactions, member join/leave, message edits).
	// Callers that don't care about these events may pass nil.
	OnEvent(handler func(ctx context.Context, event *ChannelEvent))

	// SendMessage sends an outbound message to the platform.
	SendMessage(ctx context.Context, msg *OutboundMessage) (*SendResult, error)

	// EditMessage updates a previously sent message (for pseudo-streaming).
	// Platforms that don't support editing should return nil, ErrNotSupported.
	EditMessage(ctx context.Context, chatID, messageID, content string) (*SendResult, error)

	// Start initiates the channel's connection (e.g. WebSocket handshake).
	// It is called by the Gateway after RegisterChannel, before processing messages.
	// For channels that don't need explicit startup (e.g. Reverse), this is a no-op.
	Start(ctx context.Context) error
}

// ChannelEventType describes the type of a channel system event.
type ChannelEventType string

const (
	ChannelEventMessageReaction  ChannelEventType = "message_reaction"
	ChannelEventMessageRecalled  ChannelEventType = "message_recalled"
	ChannelEventMemberJoined     ChannelEventType = "member_joined"
	ChannelEventMemberLeft       ChannelEventType = "member_left"
)

// ChannelEvent represents a platform-specific system event (reactions, etc.)
// delivered to the gateway via the OnEvent callback.
type ChannelEvent struct {
	Type      ChannelEventType // event type discriminator
	Platform  string          // "feishu", etc.
	Timestamp string          // ISO8601 event time

	// For message_reaction / message_recalled:
	ChatID    string
	MessageID string
	UserID    string

	// For message_reaction:
	ReactionType string // e.g. "emoji", "thumb_up"

	// For member_joined / member_left:
	MemberID string
}

// HTTPChannel is an optional extension of MessageChannel that provides a
// webhook HTTP handler. Platforms like Feishu implement this so the Gateway
// can automatically mount /webhook/{channel_id}/.
type HTTPChannel interface {
	MessageChannel

	// HTTPHandler returns the platform's webhook handler for incoming events.
	HTTPHandler() http.Handler
}
