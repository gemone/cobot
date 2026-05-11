package channel

import (
	"context"
	"testing"

	cobot "github.com/cobot-agent/cobot/pkg"
)

type mockChannel struct {
	*cobot.BaseChannel
	closed bool
}

type mockMessageChannel struct {
	*mockChannel
	sent []*cobot.OutboundMessage
}

func (m *mockMessageChannel) Platform() string { return "mock" }

func (m *mockMessageChannel) OnMessage(handler func(context.Context, *cobot.InboundMessage)) {}

func (m *mockMessageChannel) OnEvent(handler func(context.Context, *cobot.ChannelEvent)) {}

func (m *mockMessageChannel) Send(_ context.Context, msg *cobot.OutboundMessage) (*cobot.SendResult, error) {
	if err := m.CheckAlive(); err != nil {
		return nil, err
	}
	m.sent = append(m.sent, msg)
	return &cobot.SendResult{Success: true}, nil
}

func (m *mockMessageChannel) Start(ctx context.Context) error { return nil }

func (m *mockChannel) Close() {
	if m.BaseChannel.TryClose() {
		m.closed = true
	}
}

func TestManagerRegisterAndGet(t *testing.T) {
	mgr := NewManager()
	ch := &mockChannel{BaseChannel: cobot.NewBaseChannel("test:1")}

	if err := mgr.Register(ch); err != nil {
		t.Fatalf("Register: %v", err)
	}

	got, alive := mgr.Get("test:1")
	if !alive {
		t.Fatal("expected channel to be alive")
	}
	if got != ch {
		t.Fatalf("expected registered channel, got %#v", got)
	}
}

func TestManagerRegisterDuplicate(t *testing.T) {
	mgr := NewManager()
	ch1 := &mockChannel{BaseChannel: cobot.NewBaseChannel("test:1")}
	ch2 := &mockChannel{BaseChannel: cobot.NewBaseChannel("test:1")}

	if err := mgr.Register(ch1); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if err := mgr.Register(ch2); err == nil {
		t.Fatal("expected duplicate channel ID error")
	}
}

func TestManagerGetUnknown(t *testing.T) {
	mgr := NewManager()

	_, alive := mgr.Get("nonexistent")
	if alive {
		t.Fatal("expected no channel for unknown ID")
	}
}

func TestManagerUnregister(t *testing.T) {
	mgr := NewManager()
	ch := &mockChannel{BaseChannel: cobot.NewBaseChannel("test:1")}

	if err := mgr.Register(ch); err != nil {
		t.Fatalf("Register: %v", err)
	}
	mgr.Unregister("test:1")

	_, alive := mgr.Get("test:1")
	if alive {
		t.Fatal("expected channel to be gone after unregister")
	}
}

func TestManagerGetDeadChannel(t *testing.T) {
	mgr := NewManager()
	ch := &mockChannel{BaseChannel: cobot.NewBaseChannel("test:1")}

	if err := mgr.Register(ch); err != nil {
		t.Fatalf("Register: %v", err)
	}
	ch.Close()

	_, alive := mgr.Get("test:1")
	if alive {
		t.Fatal("expected dead channel to report not alive")
	}
}

func TestManagerAllAliveIDs(t *testing.T) {
	mgr := NewManager()

	if ids := mgr.AllAliveIDs(); len(ids) != 0 {
		t.Fatalf("expected empty, got %v", ids)
	}

	ch1 := &mockChannel{BaseChannel: cobot.NewBaseChannel("test:1")}
	ch2 := &mockChannel{BaseChannel: cobot.NewBaseChannel("test:2")}
	if err := mgr.Register(ch1); err != nil {
		t.Fatalf("Register ch1: %v", err)
	}
	if err := mgr.Register(ch2); err != nil {
		t.Fatalf("Register ch2: %v", err)
	}

	ids := mgr.AllAliveIDs()
	if len(ids) != 2 || ids[0] != "test:1" || ids[1] != "test:2" {
		t.Fatalf("expected [test:1 test:2], got %v", ids)
	}

	ch1.Close()
	ids = mgr.AllAliveIDs()
	if len(ids) != 1 || ids[0] != "test:2" {
		t.Fatalf("expected [test:2], got %v", ids)
	}
}

func TestManagerSendDispatchesToRegisteredChannel(t *testing.T) {
	mgr := NewManager()
	ch := &mockMessageChannel{mockChannel: &mockChannel{BaseChannel: cobot.NewBaseChannel("test:1")}}
	if err := mgr.Register(ch); err != nil {
		t.Fatalf("Register: %v", err)
	}

	msg := &cobot.OutboundMessage{Text: "hello"}
	res, err := mgr.Send(context.Background(), "test:1", msg)
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if res == nil || !res.Success {
		t.Fatalf("expected successful SendResult, got %#v", res)
	}
	if len(ch.sent) != 1 || ch.sent[0] != msg {
		t.Fatalf("expected channel to receive the outbound message, got %#v", ch.sent)
	}
}

func TestManagerSendUnknownChannel(t *testing.T) {
	mgr := NewManager()

	res, err := mgr.Send(context.Background(), "nonexistent", &cobot.OutboundMessage{Text: "hello"})
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if res != nil {
		t.Fatalf("expected nil result, got %#v", res)
	}
}
