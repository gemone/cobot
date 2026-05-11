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
	ch := NewReverseChannel("rev-1", "http://example.com/cb", "")
	if ch.Platform() != "reverse" {
		t.Fatalf("expected platform 'reverse', got %q", ch.Platform())
	}
	if ch.ID() != "rev-1" {
		t.Fatalf("expected id 'rev-1', got %q", ch.ID())
	}
}

func TestReverseChannelSend(t *testing.T) {
	var receivedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		r.Body.Read(buf)
		receivedBody = buf
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ch := NewReverseChannel("rev-2", server.URL, "mysecret")
	defer ch.Close()

	msg := &cobot.OutboundMessage{
		ReceiveID: "chat1",
		Text:      "hello from reverse",
	}
	result, err := ch.Send(context.Background(), msg)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success {
		t.Fatal("expected success")
	}

	// Verify callback received the message.
	var parsed cobot.OutboundMessage
	if err := json.Unmarshal(receivedBody, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Text != "hello from reverse" {
		t.Fatalf("expected 'hello from reverse', got %q", parsed.Text)
	}
}

func TestReverseChannelSendSecret(t *testing.T) {
	var receivedSecret string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedSecret = r.Header.Get("X-Reverse-Secret")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ch := NewReverseChannel("rev-3", server.URL, "s3cret")
	defer ch.Close()

	_, err := ch.Send(context.Background(), &cobot.OutboundMessage{Text: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if receivedSecret != "s3cret" {
		t.Fatalf("expected secret 's3cret', got %q", receivedSecret)
	}
}

func TestReverseChannelSendError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	ch := NewReverseChannel("rev-4", server.URL, "")
	defer ch.Close()

	_, err := ch.Send(context.Background(), &cobot.OutboundMessage{Text: "test"})
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestReverseChannelAliveAndClose(t *testing.T) {
	ch := NewReverseChannel("rev-5", "http://localhost:99999/cb", "")
	if !ch.IsAlive() {
		t.Fatal("expected channel to be alive")
	}
	ch.Close()
	if ch.IsAlive() {
		t.Fatal("expected channel to be dead after Close")
	}
	// Double close should not panic.
	ch.Close()
}

func TestReverseChannelSendNotificationAsMessage(t *testing.T) {
	var receivedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		r.Body.Read(buf)
		receivedBody = buf
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ch := NewReverseChannel("rev-7", server.URL, "")
	defer ch.Close()

	_, err := ch.Send(context.Background(), &cobot.OutboundMessage{Text: "task completed"})
	if err != nil {
		t.Fatal(err)
	}

	var parsed cobot.OutboundMessage
	if err := json.Unmarshal(receivedBody, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Text != "task completed" {
		t.Fatalf("expected 'task completed', got %q", parsed.Text)
	}
}
