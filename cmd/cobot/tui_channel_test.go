package main

import (
	"context"
	"testing"

	cobot "github.com/cobot-agent/cobot/pkg"
)

func TestTUIChannelSendReceive(t *testing.T) {
	notify := make(chan *cobot.OutboundMessage, 1)
	ch := newTUIChannel("tui:test", notify)

	if !ch.IsAlive() {
		t.Fatal("expected channel to be alive")
	}
	if ch.ID() != "tui:test" {
		t.Fatalf("expected ID tui:test, got %s", ch.ID())
	}

	msg := &cobot.OutboundMessage{Text: "hello"}
	if _, err := ch.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	got := <-notify
	if got.Text != "hello" {
		t.Fatalf("expected text 'hello', got %q", got.Text)
	}
}

func TestTUIChannelSendAfterClose(t *testing.T) {
	notify := make(chan *cobot.OutboundMessage, 1)
	ch := newTUIChannel("tui:test", notify)

	ch.Close()

	if ch.IsAlive() {
		t.Fatal("expected channel to be dead after Close")
	}

	_, err := ch.Send(context.Background(), &cobot.OutboundMessage{})
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestTUIChannelDoubleClose(t *testing.T) {
	notify := make(chan *cobot.OutboundMessage, 1)
	ch := newTUIChannel("tui:test", notify)

	ch.Close()
	ch.Close() // must not panic

	if !isClosed(ch.Done()) {
		t.Fatal("expected Done channel to be closed")
	}
}

func TestPollNotificationsReceivesMessage(t *testing.T) {
	notify := make(chan *cobot.OutboundMessage, 1)
	done := make(chan struct{})

	notify <- &cobot.OutboundMessage{Text: "test-msg"}

	cmd := pollNotifications(notify, done)
	msg := cmd()

	nm, ok := msg.(notificationMsg)
	if !ok {
		t.Fatalf("expected notificationMsg, got %T", msg)
	}
	if nm.msg.Text != "test-msg" {
		t.Fatalf("expected text 'test-msg', got %q", nm.msg.Text)
	}
}

func TestPollNotificationsShutdownViaDone(t *testing.T) {
	notify := make(chan *cobot.OutboundMessage)
	done := make(chan struct{})
	close(done)

	cmd := pollNotifications(notify, done)
	msg := cmd()

	if _, ok := msg.(notificationShutdownMsg); !ok {
		t.Fatalf("expected notificationShutdownMsg, got %T", msg)
	}
}

func TestPollNotificationsShutdownViaNotifyClose(t *testing.T) {
	notify := make(chan *cobot.OutboundMessage)
	done := make(chan struct{})
	close(notify)

	cmd := pollNotifications(notify, done)
	msg := cmd()

	if _, ok := msg.(notificationShutdownMsg); !ok {
		t.Fatalf("expected notificationShutdownMsg, got %T", msg)
	}
}

func isClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}
