package gateway

import (
	"context"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	cobot "github.com/cobot-agent/cobot/pkg"
)

type mockAdapter struct {
	platform     string
	handler      func(ctx context.Context, msg *cobot.InboundMessage)
	httpHandler  http.Handler
	mu           sync.Mutex
	sent         []*cobot.OutboundMessage
	connected    bool
	disconnected bool
}

func (m *mockAdapter) Platform() string { return m.platform }
func (m *mockAdapter) Connect() (http.Handler, error) {
	m.connected = true
	return m.httpHandler, nil
}
func (m *mockAdapter) Disconnect() error {
	m.disconnected = true
	return nil
}
func (m *mockAdapter) Send(ctx context.Context, msg *cobot.OutboundMessage) (*cobot.SendResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, msg)
	return &cobot.SendResult{Success: true, MessageID: "msg_" + msg.ReceiveID}, nil
}
func (m *mockAdapter) EditMessage(ctx context.Context, chatID, messageID, content string) (*cobot.SendResult, error) {
	return nil, cobot.ErrNotSupported
}
func (m *mockAdapter) OnMessage(handler func(ctx context.Context, msg *cobot.InboundMessage)) {
	m.handler = handler
}
func (m *mockAdapter) fireMessage(msg *cobot.InboundMessage) {
	if m.handler != nil {
		m.handler(context.Background(), msg)
	}
}
func (m *mockAdapter) getSent() []*cobot.OutboundMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]*cobot.OutboundMessage{}, m.sent...)
}

func TestNewGateway(t *testing.T) {
	gw := New(Config{}, nil)
	if gw.Addr() != ":8080" {
		t.Errorf("expected Addr :8080, got %s", gw.Addr())
	}
}

func TestGatewayRegisterAdapter(t *testing.T) {
	mock := &mockAdapter{platform: "testplat"}
	gw := New(Config{Addr: ":8080"}, nil)

	err := gw.RegisterAdapter(mock)
	if err != nil {
		t.Fatalf("RegisterAdapter failed: %v", err)
	}

	adapter, ok := gw.GetAdapter("testplat")
	if !ok {
		t.Fatal("adapter not found via GetAdapter")
	}
	if adapter.Platform() != "testplat" {
		t.Errorf("expected platform testplat, got %s", adapter.Platform())
	}
	if !mock.connected {
		t.Error("Connect was not called on adapter")
	}
}

func TestGatewayStartShutdown(t *testing.T) {
	gw := New(Config{Addr: "127.0.0.1:0"}, nil)

	err := gw.Start()
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer gw.Shutdown(context.Background())

	addr := gw.Addr()
	if addr == "127.0.0.1:0" {
		t.Error("Addr should not be 127.0.0.1:0 after start")
	}
	if addr == "" {
		t.Error("Addr should not be empty")
	}
}

func TestGatewayHealthEndpoint(t *testing.T) {
	gw := New(Config{Addr: "127.0.0.1:0"}, nil)

	err := gw.Start()
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer gw.Shutdown(context.Background())

	resp, err := http.Get("http://" + gw.Addr() + "/health")
	if err != nil {
		t.Fatalf("GET /health failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Errorf("expected body ok, got %s", string(body))
	}
}

func TestGatewayWebhookRouting(t *testing.T) {
	mock := &mockAdapter{
		platform: "testplat",
		httpHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("hello"))
		}),
	}
	gw := New(Config{Addr: "127.0.0.1:0"}, nil)

	err := gw.RegisterAdapter(mock)
	if err != nil {
		t.Fatalf("RegisterAdapter failed: %v", err)
	}

	err = gw.Start()
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer gw.Shutdown(context.Background())

	resp, err := http.Get("http://" + gw.Addr() + "/webhook/testplat/path")
	if err != nil {
		t.Fatalf("GET /webhook/testplat/path failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "hello" {
		t.Errorf("expected body hello, got %s", string(body))
	}
}

func TestGatewayDedup(t *testing.T) {
	callCount := 0
	mock := &mockAdapter{
		platform: "testplat",
	}
	gw := New(Config{Addr: ":8080"}, func(ctx context.Context, msg *cobot.InboundMessage, replyFunc ReplyFunc) error {
		callCount++
		return nil
	})

	err := gw.RegisterAdapter(mock)
	if err != nil {
		t.Fatalf("RegisterAdapter failed: %v", err)
	}

	msg := &cobot.InboundMessage{
		Platform:  "testplat",
		ChatID:    "chat1",
		MessageID: "msg1",
		Text:      "hello",
	}
	mock.fireMessage(msg)

	mock.fireMessage(msg)

	if callCount != 1 {
		t.Errorf("expected call count 1, got %d", callCount)
	}
}

func TestGatewayMessageRouting(t *testing.T) {
	var receivedMsg *cobot.InboundMessage
	mock := &mockAdapter{
		platform: "testplat",
	}
	gw := New(Config{Addr: ":8080"}, func(ctx context.Context, msg *cobot.InboundMessage, replyFunc ReplyFunc) error {
		receivedMsg = msg
		return nil
	})

	err := gw.RegisterAdapter(mock)
	if err != nil {
		t.Fatalf("RegisterAdapter failed: %v", err)
	}

	inbound := &cobot.InboundMessage{
		Platform:    "testplat",
		ChatID:      "chat123",
		ChatType:    "group",
		SenderID:    "user456",
		SenderName:  "TestUser",
		Text:        "hello world",
		MessageType: "text",
		MessageID:   "msg123",
		ReplyToID:   "parent123",
		Timestamp:   time.Now(),
	}

	mock.fireMessage(inbound)

	if receivedMsg == nil {
		t.Fatal("message was not routed to handler")
	}
	if receivedMsg.Platform != "testplat" {
		t.Errorf("expected Platform testplat, got %s", receivedMsg.Platform)
	}
	if receivedMsg.ChatID != "chat123" {
		t.Errorf("expected ChatID chat123, got %s", receivedMsg.ChatID)
	}
	if receivedMsg.SenderID != "user456" {
		t.Errorf("expected SenderID user456, got %s", receivedMsg.SenderID)
	}
	if receivedMsg.Text != "hello world" {
		t.Errorf("expected Text 'hello world', got %s", receivedMsg.Text)
	}
	if receivedMsg.MessageID != "msg123" {
		t.Errorf("expected MessageID msg123, got %s", receivedMsg.MessageID)
	}
}
