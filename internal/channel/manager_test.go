package channel

import (
	"context"
	"testing"
	"time"

	cobot "github.com/cobot-agent/cobot/pkg"
)

// mockChannel implements cobot.Channel for testing.
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

func (m *mockMessageChannel) EditMessage(ctx context.Context, chatID, messageID, content string) (*cobot.SendResult, error) {
	return nil, cobot.ErrNotSupported
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

	mgr.Register(ch, "test-session")

	got, alive := mgr.Get("test:1")
	if !alive {
		t.Fatal("expected channel to be alive")
	}
	if got.ID() != "test:1" {
		t.Fatalf("expected ID test:1, got %s", got.ID())
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

	mgr.Register(ch, "test-session")
	mgr.Unregister("test:1", "test-session")

	_, alive := mgr.Get("test:1")
	if alive {
		t.Fatal("expected channel to be gone after unregister")
	}
}

func TestManagerGetDeadChannel(t *testing.T) {
	mgr := NewManager()
	ch := &mockChannel{BaseChannel: cobot.NewBaseChannel("test:1")}

	mgr.Register(ch, "test-session")
	ch.Close()

	_, alive := mgr.Get("test:1")
	if alive {
		t.Fatal("expected dead channel to report not alive")
	}
}

func TestManagerAllAliveIDs(t *testing.T) {
	mgr := NewManager()

	// Empty manager
	if ids := mgr.AllAliveIDs(); len(ids) != 0 {
		t.Fatalf("expected empty, got %v", ids)
	}

	// With alive channels
	ch1 := &mockChannel{BaseChannel: cobot.NewBaseChannel("test:1")}
	ch2 := &mockChannel{BaseChannel: cobot.NewBaseChannel("test:2")}
	mgr.Register(ch1, "session-1")
	mgr.Register(ch2, "session-2")
	ids := mgr.AllAliveIDs()
	if len(ids) != 2 {
		t.Fatalf("expected 2 IDs, got %d", len(ids))
	}

	// Dead channel
	ch1.Close()
	ids = mgr.AllAliveIDs()
	if len(ids) != 1 || ids[0] != "test:2" {
		t.Fatalf("expected [test:2], got %v", ids)
	}
}

func TestManagerSendFansOutToAliveEntries(t *testing.T) {
	mgr := NewManager()
	ch1 := &mockMessageChannel{mockChannel: &mockChannel{BaseChannel: cobot.NewBaseChannel("test:1")}}
	ch2 := &mockMessageChannel{mockChannel: &mockChannel{BaseChannel: cobot.NewBaseChannel("test:1")}}
	dead := &mockMessageChannel{mockChannel: &mockChannel{BaseChannel: cobot.NewBaseChannel("test:1")}}
	dead.Close()

	mgr.Register(ch1, "session-1")
	mgr.Register(ch2, "session-2")
	mgr.Register(dead, "session-dead")

	msg := &cobot.OutboundMessage{Text: "hello"}
	res, err := mgr.Send(context.Background(), "test:1", msg)
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if res == nil || !res.Success {
		t.Fatalf("expected successful SendResult, got %#v", res)
	}
	if len(ch1.sent) != 1 {
		t.Fatalf("expected ch1 to receive 1 message, got %d", len(ch1.sent))
	}
	if len(ch2.sent) != 1 {
		t.Fatalf("expected ch2 to receive 1 message, got %d", len(ch2.sent))
	}
	if len(dead.sent) != 0 {
		t.Fatalf("expected dead channel to receive 0 messages, got %d", len(dead.sent))
	}
	if ch1.sent[0] != msg || ch2.sent[0] != msg {
		t.Fatal("expected fan-out to deliver the same outbound message instance")
	}
}

func TestManagerHeartbeat(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr := NewManager()
	ch := &mockChannel{BaseChannel: cobot.NewBaseChannel("test:1")}
	mgr.Register(ch, "test-session")

	// Start health check: 200ms interval, 600ms timeout (3x)
	mgr.StartHealthCheck(ctx, 200*time.Millisecond)

	// Channel should be alive initially (registered sets heartbeat)
	_, alive := mgr.Get("test:1")
	if !alive {
		t.Fatal("expected channel to be alive")
	}

	// Wait for 2 check cycles without heartbeat — channel should be removed
	time.Sleep(800 * time.Millisecond)

	_, alive = mgr.Get("test:1")
	if alive {
		t.Fatal("expected channel to be removed after heartbeat timeout")
	}

	mgr.StopHealthCheck()
}

func TestManagerHeartbeatKeepsAlive(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr := NewManager()
	ch := &mockChannel{BaseChannel: cobot.NewBaseChannel("test:1")}
	mgr.Register(ch, "test-session")

	mgr.StartHealthCheck(ctx, 50*time.Millisecond)

	// Continuously send heartbeats
	go func() {
		ticker := time.NewTicker(30 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				mgr.Heartbeat("test-session")
			}
		}
	}()

	// After 300ms, channel should still be alive
	time.Sleep(300 * time.Millisecond)

	_, alive := mgr.Get("test:1")
	if !alive {
		t.Fatal("expected channel to stay alive with active heartbeats")
	}

	mgr.StopHealthCheck()
}

func TestManagerHeartbeatUnknown(t *testing.T) {
	mgr := NewManager()
	err := mgr.Heartbeat("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown channel")
	}
}

func TestManagerMarkLocalSkipsExpiry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr := NewManager()
	ch := &mockChannel{BaseChannel: cobot.NewBaseChannel("local:1")}
	mgr.Register(ch, "test-session")
	mgr.MarkLocal("test-session")

	// Start health check: 50ms interval, 150ms timeout (3x)
	mgr.StartHealthCheck(ctx, 50*time.Millisecond)

	// Wait long enough that a non-local channel would expire
	time.Sleep(250 * time.Millisecond)

	_, alive := mgr.Get("local:1")
	if !alive {
		t.Fatal("expected local channel to stay alive despite no heartbeats")
	}

	mgr.StopHealthCheck()
}

func TestManagerStopHealthCheckIdempotent(t *testing.T) {
	mgr := NewManager()

	// StopHealthCheck on a manager that never started should not panic.
	mgr.StopHealthCheck()
	mgr.StopHealthCheck()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr.StartHealthCheck(ctx, 50*time.Millisecond)
	mgr.StopHealthCheck()
	// Second stop should be safe.
	mgr.StopHealthCheck()
}

func TestManagerHealthCheckRestart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr := NewManager()
	ch1 := &mockChannel{BaseChannel: cobot.NewBaseChannel("test:1")}
	mgr.Register(ch1, "test-session")

	// Start health check, then restart it
	mgr.StartHealthCheck(ctx, 50*time.Millisecond)
	mgr.StartHealthCheck(ctx, 50*time.Millisecond)

	// Channel should still be alive
	_, alive := mgr.Get("test:1")
	if !alive {
		t.Fatal("expected channel to be alive after restart")
	}

	mgr.StopHealthCheck()
}
