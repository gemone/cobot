package main

import (
	"context"

	tea "charm.land/bubbletea/v2"

	cobot "github.com/cobot-agent/cobot/pkg"
)

// notificationMsg is a BubbleTea message carrying a channel notification.
type notificationMsg struct {
	msg *cobot.OutboundMessage
}

// notificationShutdownMsg is sent when the notification channel is closed,
// signalling BubbleTea to stop polling.
type notificationShutdownMsg struct{}

// tuiChannel implements cobot.MessageChannel for the TUI.
type tuiChannel struct {
	*cobot.BaseChannel
	notify chan<- *cobot.OutboundMessage
	done   chan struct{} // closed in Close to unblock pollNotifications
}

func newTUIChannel(id string, notify chan<- *cobot.OutboundMessage) *tuiChannel {
	return &tuiChannel{
		BaseChannel: cobot.NewBaseChannel(id),
		notify:      notify,
		done:        make(chan struct{}),
	}
}

func (ch *tuiChannel) Platform() string { return "tui" }

func (ch *tuiChannel) OnMessage(handler func(ctx context.Context, msg *cobot.InboundMessage)) {}

func (ch *tuiChannel) OnEvent(handler func(ctx context.Context, event *cobot.ChannelEvent)) {}

func (ch *tuiChannel) Send(ctx context.Context, msg *cobot.OutboundMessage) (*cobot.SendResult, error) {
	if err := ch.CheckAlive(); err != nil {
		return nil, err
	}
	var notify chan<- *cobot.OutboundMessage
	var done <-chan struct{}
	ch.WithRLock(func() {
		notify = ch.notify
		done = ch.done
	})
	if notify == nil {
		return nil, context.Canceled
	}
	select {
	case notify <- msg:
		return &cobot.SendResult{Success: true}, nil
	case <-done:
		return nil, context.Canceled
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (ch *tuiChannel) Start(ctx context.Context) error { return nil }

func (ch *tuiChannel) Close() {
	if ch.BaseChannel.TryClose() {
		ch.WithLock(func() {
			close(ch.done)
			ch.notify = nil
		})
	}
}

// Done returns a read-only channel that is closed by Close.
// Callers can select on it to detect shutdown.
func (ch *tuiChannel) Done() <-chan struct{} {
	return ch.done
}

// pollNotifications returns a tea.Cmd that waits for channel messages
// and converts them to notificationMsg for the BubbleTea Update loop.
// When the notify channel is closed, it returns notificationShutdownMsg
// to cleanly stop the polling cycle.
func pollNotifications(notify <-chan *cobot.OutboundMessage, done <-chan struct{}) tea.Cmd {
	return func() tea.Msg {
		select {
		case msg, ok := <-notify:
			if !ok {
				return notificationShutdownMsg{}
			}
			return notificationMsg{msg: msg}
		case <-done:
			return notificationShutdownMsg{}
		}
	}
}
