package channel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	cobot "github.com/cobot-agent/cobot/pkg"
)

// ReverseChannel implements cobot.MessageChannel by forwarding outbound
// messages to a remote HTTP callback URL. It is used for third-party
// integrations via the Gateway REST API.
type ReverseChannel struct {
	*cobot.BaseChannel
	platform     string
	callbackURL  string
	secret       string
	httpClient   *http.Client
	handler      func(ctx context.Context, msg *cobot.InboundMessage)
	handlerMu    sync.RWMutex
}

// NewReverseChannel creates a ReverseChannel that POSTs messages to callbackURL.
func NewReverseChannel(id, callbackURL, secret string) *ReverseChannel {
	return &ReverseChannel{
		BaseChannel: cobot.NewBaseChannel(id),
		platform:    "reverse",
		callbackURL: callbackURL,
		secret:      secret,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (ch *ReverseChannel) Platform() string { return ch.platform }

// OnMessage registers the inbound message callback.
// For ReverseChannel, inbound messages are handled via the Gateway REST API;
// they are not delivered through the outbound callback flow implemented here.
func (ch *ReverseChannel) OnMessage(handler func(ctx context.Context, msg *cobot.InboundMessage)) {
	ch.handlerMu.Lock()
	ch.handler = handler
	ch.handlerMu.Unlock()
}

// SendMessage POSTs the outbound message to the callback URL.
func (ch *ReverseChannel) SendMessage(ctx context.Context, msg *cobot.OutboundMessage) (*cobot.SendResult, error) {
	if !ch.IsAlive() {
		return nil, fmt.Errorf("reverse channel %s is closed", ch.ID())
	}

	payload, err := json.Marshal(msg)
	if err != nil {
		return &cobot.SendResult{Success: false}, fmt.Errorf("reverse marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ch.callbackURL, bytes.NewReader(payload))
	if err != nil {
		return &cobot.SendResult{Success: false}, fmt.Errorf("reverse create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if ch.secret != "" {
		req.Header.Set("X-Reverse-Secret", ch.secret)
	}

	resp, err := ch.httpClient.Do(req)
	if err != nil {
		return &cobot.SendResult{Success: false}, fmt.Errorf("reverse callback: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return &cobot.SendResult{Success: false}, fmt.Errorf("reverse callback returned status %d", resp.StatusCode)
	}

	slog.Debug("reverse: message forwarded", "channel", ch.ID(), "callback_host", req.URL.Host)
	return &cobot.SendResult{Success: true}, nil
}

// EditMessage is not supported for reverse channels.
func (ch *ReverseChannel) EditMessage(ctx context.Context, chatID, messageID, content string) (*cobot.SendResult, error) {
	return nil, cobot.ErrNotSupported
}

// Send delivers a notification (Channel interface) via the callback URL.
func (ch *ReverseChannel) Send(ctx context.Context, msg cobot.ChannelMessage) error {
	out := &cobot.OutboundMessage{
		Text: msg.Content,
	}
	_, err := ch.SendMessage(ctx, out)
	return err
}

// Close shuts down the ReverseChannel.
func (ch *ReverseChannel) Close() {
	if ch.BaseChannel.TryClose() {
		slog.Info("reverse: channel closed", "channel", ch.ID())
	}
}
