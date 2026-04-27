package channel

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkdispatch "github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkhttp "github.com/larksuite/oapi-sdk-go/v3/core/httpserverext"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"

	cobot "github.com/cobot-agent/cobot/pkg"
)

// FeishuConfig holds Feishu-specific credentials and settings.
type FeishuConfig struct {
	AppID             string
	AppSecret         string
	VerificationToken string
	EncryptKey        string
}

// FeishuChannel implements cobot.HTTPChannel for Feishu/Lark bots.
// It receives messages via webhook and sends replies through the Lark IM API.
type FeishuChannel struct {
	*cobot.BaseChannel
	platform string
	config   FeishuConfig
	client   *lark.Client

	handler     func(ctx context.Context, msg *cobot.InboundMessage)
	handlerMu   sync.RWMutex
	httpHandler http.Handler
}

// NewFeishuChannel creates a FeishuChannel with the given ID and config.
func NewFeishuChannel(id string, cfg FeishuConfig) *FeishuChannel {
	ch := &FeishuChannel{
		BaseChannel: cobot.NewBaseChannel(id),
		platform:    "feishu",
		config:      cfg,
		client:      lark.NewClient(cfg.AppID, cfg.AppSecret),
	}
	ch.buildHTTPHandler()
	return ch
}

func (ch *FeishuChannel) Platform() string { return ch.platform }

// OnMessage registers the inbound message callback.
func (ch *FeishuChannel) OnMessage(handler func(ctx context.Context, msg *cobot.InboundMessage)) {
	ch.handlerMu.Lock()
	ch.handler = handler
	ch.handlerMu.Unlock()
}

// HTTPHandler returns the webhook handler for the Lark event dispatcher.
func (ch *FeishuChannel) HTTPHandler() http.Handler {
	return ch.httpHandler
}

// buildHTTPHandler creates the Lark SDK event dispatcher and wraps it as http.Handler.
func (ch *FeishuChannel) buildHTTPHandler() {
	dispatcher := larkdispatch.NewEventDispatcher(ch.config.VerificationToken, ch.config.EncryptKey).
		OnP2MessageReceiveV1(ch.handleReceive)

	// larkhttp.NewEventHandlerFunc returns func(w http.ResponseWriter, r *http.Request)
	fn := larkhttp.NewEventHandlerFunc(dispatcher)
	ch.httpHandler = http.HandlerFunc(fn)
}

// handleReceive is the Lark SDK callback for incoming messages.
func (ch *FeishuChannel) handleReceive(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
	if event == nil || event.Event == nil || event.Event.Message == nil {
		return nil
	}

	msgData := event.Event.Message
	text := ExtractTextContent(ptrStr(msgData.Content))

	// Skip empty messages (e.g. system events, reactions).
	if strings.TrimSpace(text) == "" {
		return nil
	}

	chatID := ptrStr(msgData.ChatId)
	messageID := ptrStr(msgData.MessageId)
	msgType := ptrStr(msgData.MessageType)

	senderID := ""
	if event.Event.Sender != nil && event.Event.Sender.SenderId != nil {
		senderID = ptrStr(event.Event.Sender.SenderId.OpenId)
	}

	inbound := &cobot.InboundMessage{
		Platform:    ch.platform,
		ChatID:      chatID,
		ChatType:    ptrStr(msgData.ChatType),
		SenderID:    senderID,
		Text:        text,
		MessageType: msgType,
		MessageID:   messageID,
		Raw:         []byte(ptrStr(msgData.Content)),
	}

	ch.handlerMu.RLock()
	handler := ch.handler
	ch.handlerMu.RUnlock()

	if handler != nil {
		handler(ctx, inbound)
	}
	return nil
}

// SendMessage sends a text message to a Feishu chat.
func (ch *FeishuChannel) SendMessage(ctx context.Context, msg *cobot.OutboundMessage) (*cobot.SendResult, error) {
	if !ch.IsAlive() {
		return nil, fmt.Errorf("feishu channel %s is closed", ch.ID())
	}
	if msg.ReceiveID == "" {
		return nil, fmt.Errorf("feishu SendMessage: receive_id is required")
	}

	content := formatTextContent(msg.Text)
	resp, err := ch.client.Im.V1.Message.Create(ctx,
		larkim.NewCreateMessageReqBuilder().
			ReceiveIdType("chat_id").
			Body(larkim.NewCreateMessageReqBodyBuilder().
				ReceiveId(msg.ReceiveID).
				MsgType("text").
				Content(content).
				Build()).
			Build(),
	)
	if err != nil {
		return &cobot.SendResult{Success: false}, fmt.Errorf("feishu send message: %w", err)
	}

	messageID := ""
	if resp != nil && resp.Data != nil {
		messageID = ptrStr(resp.Data.MessageId)
	}
	slog.Debug("feishu: message sent", "channel", ch.ID(), "chat_id", msg.ReceiveID, "message_id", messageID)
	return &cobot.SendResult{Success: true, MessageID: messageID}, nil
}

// EditMessage updates a previously sent message (for pseudo-streaming).
func (ch *FeishuChannel) EditMessage(ctx context.Context, chatID, messageID, content string) (*cobot.SendResult, error) {
	if !ch.IsAlive() {
		return nil, fmt.Errorf("feishu channel %s is closed", ch.ID())
	}
	if messageID == "" {
		return nil, fmt.Errorf("feishu EditMessage: message_id is required")
	}

	_, err := ch.client.Im.V1.Message.Update(ctx,
		larkim.NewUpdateMessageReqBuilder().
			MessageId(messageID).
			Body(larkim.NewUpdateMessageReqBodyBuilder().
				Content(formatTextContent(content)).
				Build()).
			Build(),
	)
	if err != nil {
		return &cobot.SendResult{Success: false}, fmt.Errorf("feishu edit message: %w", err)
	}
	return &cobot.SendResult{Success: true, MessageID: messageID}, nil
}

// Send delivers a notification via the generic Channel interface.
// Feishu does not currently support notification delivery through Send.
func (ch *FeishuChannel) Send(ctx context.Context, msg cobot.ChannelMessage) error {
	// Notification delivery is not the primary use case for Feishu.
	// This is used for cron results etc. — requires a default chat ID.
	slog.Warn("feishu: Send (notification) called but no default chat configured", "channel", ch.ID())
	return cobot.ErrNotSupported
}

// Close shuts down the FeishuChannel.
func (ch *FeishuChannel) Close() {
	if ch.BaseChannel.TryClose() {
		slog.Info("feishu: channel closed", "channel", ch.ID())
	}
}

// formatTextContent wraps plain text into the JSON format expected by Feishu.
func formatTextContent(text string) string {
	payload := map[string]string{"text": text}
	data, _ := json.Marshal(payload) // json.Marshal always succeeds for a flat map
	return string(data)
}

// ptrStr safely dereferences a *string, returning "" for nil.
func ptrStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
