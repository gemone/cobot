package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	cobot "github.com/cobot-agent/cobot/pkg"

	"github.com/cobot-agent/cobot/internal/channel"
)

// mockMessageChannel implements cobot.MessageChannel for testing.
type mockMessageChannel struct {
	cobot.BaseChannel
	platform    string
	handler     func(ctx context.Context, msg *cobot.InboundMessage)
	sent        []*cobot.OutboundMessage
	mu          sync.Mutex
	httpHandler http.Handler
}

func newMockMessageChannel(id, platform string) *mockMessageChannel {
	return &mockMessageChannel{
		BaseChannel: *cobot.NewBaseChannel(id),
		platform:    platform,
		httpHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	}
}

func (m *mockMessageChannel) Platform() string { return m.platform }

func (m *mockMessageChannel) OnMessage(handler func(ctx context.Context, msg *cobot.InboundMessage)) {
	m.handler = handler
}

func (m *mockMessageChannel) SendMessage(ctx context.Context, msg *cobot.OutboundMessage) (*cobot.SendResult, error) {
	m.mu.Lock()
	m.sent = append(m.sent, msg)
	m.mu.Unlock()
	return &cobot.SendResult{Success: true, MessageID: "msg_" + msg.ReceiveID}, nil
}

func (m *mockMessageChannel) EditMessage(ctx context.Context, chatID, messageID, content string) (*cobot.SendResult, error) {
	return nil, cobot.ErrNotSupported
}

func (m *mockMessageChannel) Send(ctx context.Context, msg cobot.ChannelMessage) error {
	return nil
}

func (m *mockMessageChannel) HTTPHandler() http.Handler { return m.httpHandler }

func (m *mockMessageChannel) getSent() []*cobot.OutboundMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sent
}

// simulateInbound triggers the OnMessage handler with a test message.
func (m *mockMessageChannel) simulateInbound(text, chatID, messageID string) {
	if m.handler == nil {
		return
	}
	m.handler(context.Background(), &cobot.InboundMessage{
		Platform:  m.platform,
		ChatID:    chatID,
		ChatType:  "p2p",
		SenderID:  "user1",
		Text:      text,
		MessageID: messageID,
	})
}

// --- Tests ---

func TestNewGateway(t *testing.T) {
	mgr := channel.NewManager()
	gw := New(Config{}, mgr, nil)
	if gw == nil {
		t.Fatal("expected non-nil gateway")
	}
}

func TestGatewayHealthEndpoint(t *testing.T) {
	mgr := channel.NewManager()
	gw := New(Config{Addr: "127.0.0.1:0"}, mgr, nil)
	if err := gw.Start(); err != nil {
		t.Fatal(err)
	}
	defer gw.Shutdown(context.Background())

	resp, err := http.Get("http://" + gw.Addr() + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestGatewayRegisterChannel(t *testing.T) {
	mgr := channel.NewManager()
	var received *string
	handler := func(ctx context.Context, msg *cobot.InboundMessage, replyFunc ReplyFunc) error {
		s := msg.Text
		received = &s
		replyFunc(&cobot.OutboundMessage{Text: "echo: " + msg.Text})
		return nil
	}

	gw := New(Config{Addr: "127.0.0.1:0"}, mgr, handler)
	mc := newMockMessageChannel("test-ch", "mock")

	if err := gw.RegisterChannel(mc); err != nil {
		t.Fatal(err)
	}

	// Simulate inbound message
	mc.simulateInbound("hello", "chat1", "msg1")
	if received == nil || *received != "hello" {
		t.Fatalf("expected 'hello', got %v", received)
	}

	// Check that SendMessage was called with the reply
	sent := mc.getSent()
	if len(sent) != 1 {
		t.Fatalf("expected 1 sent message, got %d", len(sent))
	}
	if sent[0].Text != "echo: hello" {
		t.Fatalf("expected 'echo: hello', got %q", sent[0].Text)
	}
	if sent[0].ReceiveID != "chat1" {
		t.Fatalf("expected ReceiveID 'chat1', got %q", sent[0].ReceiveID)
	}
}

func TestGatewayDedup(t *testing.T) {
	mgr := channel.NewManager()
	count := 0
	handler := func(ctx context.Context, msg *cobot.InboundMessage, replyFunc ReplyFunc) error {
		count++
		return nil
	}

	gw := New(Config{Addr: "127.0.0.1:0"}, mgr, handler)
	mc := newMockMessageChannel("dedup-ch", "mock")
	if err := gw.RegisterChannel(mc); err != nil {
		t.Fatal(err)
	}

	// Send same message twice
	mc.simulateInbound("hello", "chat1", "msg-dup")
	mc.simulateInbound("hello", "chat1", "msg-dup")

	if count != 1 {
		t.Fatalf("expected 1 (dedup), got %d", count)
	}
}

func TestGatewayEmptyMessageIDNoDedup(t *testing.T) {
	mgr := channel.NewManager()
	count := 0
	handler := func(ctx context.Context, msg *cobot.InboundMessage, replyFunc ReplyFunc) error {
		count++
		return nil
	}

	gw := New(Config{Addr: "127.0.0.1:0"}, mgr, handler)
	mc := newMockMessageChannel("no-id-ch", "mock")
	if err := gw.RegisterChannel(mc); err != nil {
		t.Fatal(err)
	}

	// Empty MessageID — should NOT be deduped
	mc.simulateInbound("hello", "chat1", "")
	mc.simulateInbound("hello", "chat1", "")

	if count != 2 {
		t.Fatalf("expected 2 (no dedup for empty MessageID), got %d", count)
	}
}

func TestGatewayWebhookRouting(t *testing.T) {
	mgr := channel.NewManager()
	gw := New(Config{Addr: "127.0.0.1:0"}, mgr, nil)
	mc := newMockMessageChannel("webhook-ch", "mock")
	if err := gw.RegisterChannel(mc); err != nil {
		t.Fatal(err)
	}

	if err := gw.Start(); err != nil {
		t.Fatal(err)
	}
	defer gw.Shutdown(context.Background())

	resp, err := http.Post("http://"+gw.Addr()+"/webhook/webhook-ch/", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestGatewayStartTwice(t *testing.T) {
	mgr := channel.NewManager()
	gw := New(Config{Addr: "127.0.0.1:0"}, mgr, nil)
	if err := gw.Start(); err != nil {
		t.Fatal(err)
	}
	defer gw.Shutdown(context.Background())

	err := gw.Start()
	if err == nil {
		t.Fatal("expected error on double start")
	}
}

func TestGatewayChannelValidation(t *testing.T) {
	mgr := channel.NewManager()
	gw := New(Config{}, mgr, nil)

	// Empty ID
	mc := newMockMessageChannel("", "mock")
	if err := gw.RegisterChannel(mc); err == nil {
		t.Fatal("expected error for empty ID")
	}

	// Invalid characters
	mc2 := newMockMessageChannel("UPPER CASE", "mock")
	if err := gw.RegisterChannel(mc2); err == nil {
		t.Fatal("expected error for invalid ID")
	}
}

func TestGatewayListChannels(t *testing.T) {
	mgr := channel.NewManager()
	gw := New(Config{Addr: "127.0.0.1:0"}, mgr, nil)

	mc1 := newMockMessageChannel("list-ch-1", "mock")
	mc2 := newMockMessageChannel("list-ch-2", "mock")
	gw.RegisterChannel(mc1)
	gw.RegisterChannel(mc2)

	if err := gw.Start(); err != nil {
		t.Fatal(err)
	}
	defer gw.Shutdown(context.Background())

	resp, err := http.Get("http://" + gw.Addr() + "/api/v1/channels")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result channelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if len(result.Channels) != 2 {
		t.Fatalf("expected 2 channels, got %d", len(result.Channels))
	}
}

func TestGatewaySendMessage(t *testing.T) {
	mgr := channel.NewManager()
	var receivedText string
	handler := func(ctx context.Context, msg *cobot.InboundMessage, replyFunc ReplyFunc) error {
		receivedText = msg.Text
		replyFunc(&cobot.OutboundMessage{Text: "reply: " + msg.Text})
		return nil
	}

	gw := New(Config{Addr: "127.0.0.1:0"}, mgr, handler)
	mc := newMockMessageChannel("msg-ch", "mock")
	gw.RegisterChannel(mc)

	if err := gw.Start(); err != nil {
		t.Fatal(err)
	}
	defer gw.Shutdown(context.Background())

	body := `{"chat_id":"chat1","text":"hello"}`
	resp, err := http.Post(
		"http://"+gw.Addr()+"/api/v1/channels/msg-ch/messages",
		"application/json",
		strings.NewReader(body),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	if receivedText != "hello" {
		t.Fatalf("expected 'hello', got %q", receivedText)
	}
}
