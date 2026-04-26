package channel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	cobot "github.com/cobot-agent/cobot/pkg"
)

func TestReverseChannelPlatform(t *testing.T) {
	rc := NewReverseChannel("test-reverse", "http://example.com/callback", "secret")
	if rc.Platform() != "reverse" {
		t.Fatalf("expected 'reverse', got %q", rc.Platform())
	}
	if rc.ID() != "test-reverse" {
		t.Fatalf("expected 'test-reverse', got %q", rc.ID())
	}
}

func TestReverseChannelSendMessage(t *testing.T) {
	var receivedBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Reverse-Secret") != "mysecret" {
			t.Error("missing or wrong secret header")
		}
		json.NewDecoder(r.Body).Decode(&receivedBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	rc := NewReverseChannel("test-reverse", server.URL, "mysecret")
	result, err := rc.SendMessage(context.Background(), &cobot.OutboundMessage{
		ReceiveID: "chat1",
		Text:      "hello world",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success {
		t.Fatal("expected success")
	}
	if receivedBody["event_type"] != "message" {
		t.Fatalf("expected event_type 'message', got %v", receivedBody["event_type"])
	}
}

func TestReverseChannelSendNotification(t *testing.T) {
	var receivedBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&receivedBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	rc := NewReverseChannel("test-reverse", server.URL, "")
	err := rc.Send(context.Background(), cobot.ChannelMessage{
		Type:    "cron_result",
		Title:   "Task Done",
		Content: "output here",
	})
	if err != nil {
		t.Fatal(err)
	}
	if receivedBody["event_type"] != "notification" {
		t.Fatalf("expected event_type 'notification', got %v", receivedBody["event_type"])
	}
}

func TestReverseChannelReceiveMessage(t *testing.T) {
	rc := NewReverseChannel("test-reverse", "http://example.com", "")
	var received *cobot.InboundMessage
	rc.OnMessage(func(ctx context.Context, msg *cobot.InboundMessage) {
		received = msg
	})

	rc.ReceiveMessage(context.Background(), &cobot.InboundMessage{
		Platform: "reverse",
		ChatID:   "chat1",
		Text:     "hello from api",
	})

	if received == nil {
		t.Fatal("expected to receive message")
	}
	if received.Text != "hello from api" {
		t.Fatalf("expected 'hello from api', got %q", received.Text)
	}
}

func TestReverseChannelCallbackFailure(t *testing.T) {
	rc := NewReverseChannel("test-reverse", "http://127.0.0.1:1/impossible", "")
	err := rc.Send(context.Background(), cobot.ChannelMessage{Title: "test"})
	if err == nil {
		t.Fatal("expected error for unreachable callback")
	}
}
