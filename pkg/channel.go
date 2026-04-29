package cobot

import (
	"context"
	"errors"
	"sync"
)

// Channel is an abstract communication endpoint.
// Implementations: TUI, Feishu, Reverse, etc.
type Channel interface {
	// ID returns a unique identifier for this channel (e.g., "tui:default").
	ID() string

	// IsAlive returns true if the channel is still connected.
	IsAlive() bool

	// Close shuts down the channel, releasing resources.
	// It is safe to call Close multiple times.
	Close()
}

// BaseChannel provides common fields and methods for Channel implementations.
// Embed it in your struct to reuse ID/aliveness/locking behavior.
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
// The Gateway registers OnMessage handlers and calls Send for replies.
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

	// Send sends an outbound message to the platform.
	Send(ctx context.Context, msg *OutboundMessage) (*SendResult, error)

	// Start initiates the channel's connection (e.g. WebSocket handshake).
	// It is called by the Gateway after RegisterChannel, before processing messages.
	// For channels that don't need explicit startup (e.g. Reverse), this is a no-op.
	Start(ctx context.Context) error
}

// EditableChannel is implemented by MessageChannel platforms that support
// editing previously-sent messages (used for pseudo-streaming on platforms
// like Feishu). Platforms without edit support simply do not implement it.
type EditableChannel interface {
	MessageChannel

	// EditMessage updates a previously sent message.
	EditMessage(ctx context.Context, chatID, messageID, content string) (*SendResult, error)
}

// ChannelEventType describes the type of a channel system event.
type ChannelEventType string

const (
	ChannelEventMessageReaction ChannelEventType = "message_reaction"
	ChannelEventMessageRecalled ChannelEventType = "message_recalled"
	ChannelEventMemberJoined    ChannelEventType = "member_joined"
	ChannelEventMemberLeft      ChannelEventType = "member_left"
)

// ChannelEvent represents a platform-specific system event (reactions, etc.)
// delivered to the gateway via the OnEvent callback.
type ChannelEvent struct {
	Type      ChannelEventType // event type discriminator
	Platform  string           // "feishu", etc.
	Timestamp string           // ISO8601 event time

	// For message_reaction / message_recalled:
	ChatID    string
	MessageID string
	UserID    string

	// For message_reaction:
	ReactionType string // e.g. "emoji", "thumb_up"

	// For member_joined / member_left:
	MemberID string
}

// Reactioner is an optional interface implemented by MessageChannels that support
// adding emoji reactions to messages. Use a type assertion to check capability.
// Example:
//
//	if r, ok := ch.(cobot.Reactioner); ok {
//	    _ = r.ReactMessage(ctx, msgID, "👍")
//	}
type Reactioner interface {
	// ReactMessage adds a reaction emoji to a message. The reactionType
	// is a Unicode emoji string like "👍" or "🎉".
	ReactMessage(ctx context.Context, messageID, reactionType string) error
}
