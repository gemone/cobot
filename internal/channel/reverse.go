package channel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	cobot "github.com/cobot-agent/cobot/pkg"
)

// ReverseChannel implements cobot.MessageChannel for remotely registered channels.
// Messages are forwarded to a callback URL and inbound messages arrive via Gateway API.
type ReverseChannel struct {
	cobot.BaseChannel
	platform    string
	callbackURL string
	secret      string
	handler     func(ctx context.Context, msg *cobot.InboundMessage)
	httpClient  *http.Client
}

// NewReverseChannel creates a ReverseChannel that delivers messages via callback URL.
func NewReverseChannel(id, callbackURL, secret string) *ReverseChannel {
	return &ReverseChannel{
		BaseChannel: *cobot.NewBaseChannel(id),
		platform:    "reverse",
		callbackURL: callbackURL,
		secret:      secret,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (rc *ReverseChannel) Platform() string { return rc.platform }

func (rc *ReverseChannel) OnMessage(handler func(ctx context.Context, msg *cobot.InboundMessage)) {
	rc.handler = handler
}

// Send delivers a notification message to the callback URL (implements Channel).
func (rc *ReverseChannel) Send(ctx context.Context, msg cobot.ChannelMessage) error {
	return rc.postCallback(ctx, "notification", msg)
}

// SendMessage sends an outbound message to the callback URL.
func (rc *ReverseChannel) SendMessage(ctx context.Context, msg *cobot.OutboundMessage) (*cobot.SendResult, error) {
	err := rc.postCallback(ctx, "message", msg)
	if err != nil {
		return nil, err
	}
	return &cobot.SendResult{Success: true}, nil
}

// EditMessage sends an edit request to the callback URL.
func (rc *ReverseChannel) EditMessage(ctx context.Context, chatID, messageID, content string) (*cobot.SendResult, error) {
	payload := map[string]string{
		"type":       "edit",
		"chat_id":    chatID,
		"message_id": messageID,
		"content":    content,
	}
	err := rc.postCallback(ctx, "edit", payload)
	if err != nil {
		return nil, err
	}
	return &cobot.SendResult{Success: true, MessageID: messageID}, nil
}

// ReceiveMessage allows the Gateway API to inject inbound messages.
func (rc *ReverseChannel) ReceiveMessage(ctx context.Context, msg *cobot.InboundMessage) {
	if rc.handler != nil {
		rc.handler(ctx, msg)
	}
}

func (rc *ReverseChannel) postCallback(ctx context.Context, eventType string, payload interface{}) error {
	body, err := json.Marshal(map[string]interface{}{
		"channel_id": rc.ID(),
		"event_type": eventType,
		"data":       payload,
	})
	if err != nil {
		return fmt.Errorf("reverse: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rc.callbackURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("reverse: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if rc.secret != "" {
		req.Header.Set("X-Reverse-Secret", rc.secret)
	}

	resp, err := rc.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("reverse: callback failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("reverse: callback returned %d", resp.StatusCode)
	}
	return nil
}
