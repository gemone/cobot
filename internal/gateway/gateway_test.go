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
	*cobot.BaseChannel
	platform    string
	handler     func(ctx context.Context, msg *cobot.InboundMessage)
	sent        []*cobot.OutboundMessage
	mu          sync.Mutex
	httpHandler http.Handler
}

func newMockMessageChannel(id, platform string) *mockMessageChannel {
	return &mockMessageChannel{
		BaseChannel: cobot.NewBaseChannel(id),
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

func (m *mockMessageChannel) OnEvent(handler func(ctx context.Context, event *cobot.ChannelEvent)) {}

func (m *mockMessageChannel) Send(ctx context.Context, msg *cobot.OutboundMessage) (*cobot.SendResult, error) {
	m.mu.Lock()
	m.sent = append(m.sent, msg)
	m.mu.Unlock()
	return &cobot.SendResult{Success: true, MessageID: "msg_" + msg.ReceiveID}, nil
}

func (m *mockMessageChannel) Start(ctx context.Context) error {
	return nil
}

func (m *mockMessageChannel) HTTPHandler() http.Handler { return m.httpHandler }

func (m *mockMessageChannel) getSent() []*cobot.OutboundMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	sent := make([]*cobot.OutboundMessage, len(m.sent))
	for i, msg := range m.sent {
		if msg == nil {
			continue
		}
		copy := *msg
		sent[i] = &copy
	}
	return sent
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

func TestNewGatewayNilManager(t *testing.T) {
	gw := New(Config{Addr: "127.0.0.1:0"}, nil, nil)
	if gw == nil {
		t.Fatal("expected non-nil gateway with nil manager")
	}
	if err := gw.Start(); err != nil {
		t.Fatal(err)
	}
	defer gw.Shutdown(context.Background())
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

	mc.simulateInbound("hello", "chat1", "msg1")
	if received == nil || *received != "hello" {
		t.Fatalf("expected 'hello', got %v", received)
	}

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

func TestGatewayDuplicateRegistration(t *testing.T) {
	mgr := channel.NewManager()
	gw := New(Config{}, mgr, nil)

	mc := newMockMessageChannel("dup-ch", "mock")
	if err := gw.RegisterChannel(mc); err != nil {
		t.Fatal(err)
	}
	// Second registration of same ID should fail.
	mc2 := newMockMessageChannel("dup-ch", "mock")
	if err := gw.RegisterChannel(mc2); err == nil {
		t.Fatal("expected error for duplicate ID")
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

func TestGatewaySendMessageMissingChatID(t *testing.T) {
	mgr := channel.NewManager()
	gw := New(Config{Addr: "127.0.0.1:0"}, mgr, func(ctx context.Context, msg *cobot.InboundMessage, rf ReplyFunc) error { return nil })
	mc := newMockMessageChannel("nochid-ch", "mock")
	gw.RegisterChannel(mc)

	if err := gw.Start(); err != nil {
		t.Fatal(err)
	}
	defer gw.Shutdown(context.Background())

	body := `{"text":"hello"}`
	resp, err := http.Post(
		"http://"+gw.Addr()+"/api/v1/channels/nochid-ch/messages",
		"application/json",
		strings.NewReader(body),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing chat_id, got %d", resp.StatusCode)
	}
}

func TestGatewaySendMessageNilHandler(t *testing.T) {
	mgr := channel.NewManager()
	gw := New(Config{Addr: "127.0.0.1:0"}, mgr, nil) // nil handler
	mc := newMockMessageChannel("nilh-ch", "mock")
	gw.RegisterChannel(mc)

	if err := gw.Start(); err != nil {
		t.Fatal(err)
	}
	defer gw.Shutdown(context.Background())

	body := `{"chat_id":"chat1","text":"hello"}`
	resp, err := http.Post(
		"http://"+gw.Addr()+"/api/v1/channels/nilh-ch/messages",
		"application/json",
		strings.NewReader(body),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 for nil handler, got %d", resp.StatusCode)
	}
}

func TestGatewayRegisterReverseChannel(t *testing.T) {
	mgr := channel.NewManager()
	gw := New(Config{Addr: "127.0.0.1:0"}, mgr, func(ctx context.Context, msg *cobot.InboundMessage, rf ReplyFunc) error { return nil })
	gw.SetRegisterReverseFunc(func(id, callbackURL, secret string) (cobot.MessageChannel, error) {
		return newMockMessageChannel(id, "reverse"), nil
	})

	if err := gw.Start(); err != nil {
		t.Fatal(err)
	}
	defer gw.Shutdown(context.Background())

	body := `{"id":"rev-ch","callback_url":"http://example.com/cb"}`
	resp, err := http.Post(
		"http://"+gw.Addr()+"/api/v1/channels",
		"application/json",
		strings.NewReader(body),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	if result["id"] != "rev-ch" {
		t.Fatalf("expected id 'rev-ch', got %v", result["id"])
	}
	// mock channel implements webhookProvider, so webhook should be present
	if _, hasWebhook := result["webhook"]; !hasWebhook {
		t.Fatal("expected webhook field for webhookProvider mock")
	}
}

func TestGatewayUnregisterChannel(t *testing.T) {
	mgr := channel.NewManager()
	gw := New(Config{Addr: "127.0.0.1:0"}, mgr, nil)
	mc := newMockMessageChannel("unreg-ch", "mock")
	gw.RegisterChannel(mc)

	if err := gw.Start(); err != nil {
		t.Fatal(err)
	}
	defer gw.Shutdown(context.Background())

	req, _ := http.NewRequest(http.MethodDelete, "http://"+gw.Addr()+"/api/v1/channels/unreg-ch", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}

	// Channel should be gone now
	_, ok := gw.channelMgr.Get("unreg-ch")
	if ok {
		t.Fatal("expected channel to be unregistered")
	}
}

func TestGatewayAPIKeyAuth(t *testing.T) {
	mgr := channel.NewManager()
	gw := New(Config{Addr: "127.0.0.1:0"}, mgr, nil)
	gw.SetAPIKey("secret123")

	if err := gw.Start(); err != nil {
		t.Fatal(err)
	}
	defer gw.Shutdown(context.Background())

	// Without API key → 401
	resp, err := http.Get("http://" + gw.Addr() + "/api/v1/channels")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without API key, got %d", resp.StatusCode)
	}

	// With API key → 200
	req, _ := http.NewRequest(http.MethodGet, "http://"+gw.Addr()+"/api/v1/channels", nil)
	req.Header.Set("Authorization", "Bearer secret123")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 with API key, got %d", resp2.StatusCode)
	}
}

func TestGatewayHealthNoAuthRequired(t *testing.T) {
	mgr := channel.NewManager()
	gw := New(Config{Addr: "127.0.0.1:0"}, mgr, nil)
	gw.SetAPIKey("secret123")

	if err := gw.Start(); err != nil {
		t.Fatal(err)
	}
	defer gw.Shutdown(context.Background())

	// /health should work without API key
	resp, err := http.Get("http://" + gw.Addr() + "/health")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on health without auth, got %d", resp.StatusCode)
	}
}
