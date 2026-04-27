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
	rawContent := ptrStr(msgData.Content)
	msgType := ptrStr(msgData.MessageType)

	// Extract text — for image/audio/file types, text may be empty or a placeholder.
	text := ExtractTextContent(rawContent)

	// Skip empty messages (e.g. system events with no text and no media).
	if strings.TrimSpace(text) == "" && !isMediaMessageType(msgType) {
		return nil
	}

	chatID := ptrStr(msgData.ChatId)
	messageID := ptrStr(msgData.MessageId)

	senderID := ""
	if event.Event.Sender != nil && event.Event.Sender.SenderId != nil {
		senderID = ptrStr(event.Event.Sender.SenderId.OpenId)
	}

	// Build mentions list from Lark SDK MentionEvent structs.
	var mentionList []Mention
	if sdkMentions := msgData.Mentions; len(sdkMentions) > 0 {
		for _, m := range sdkMentions {
			if m == nil {
				continue
			}
			userID := ""
			if m.Id != nil {
				userID = ptrStr(m.Id.OpenId)
				if userID == "" {
					userID = ptrStr(m.Id.UserId)
				}
			}
			mentionList = append(mentionList, Mention{
				UserID:   userID,
				UserName: ptrStr(m.Name),
			})
		}
	}

	// Extract media info from message content.
	var mediaURLs []string
	var mediaTypes []string
	if keys := ExtractMediaKeys(rawContent, msgType); len(keys) > 0 {
		mediaTypes = make([]string, len(keys))
		for i := range keys {
			mediaTypes[i] = "image"
		}
		// image_key values are stored in MediaURLs as-is; the agent can
		// use them to download via the Feishu resource API if needed.
		mediaURLs = make([]string, len(keys))
		for i, k := range keys {
			mediaURLs[i] = k.Key
		}
	} else if isMediaMessageType(msgType) {
		// Non-image media (file, audio, video) — set media type, URL comes from content.
		mediaTypes = []string{msgType}
		mediaURLs = []string{""}
	}

	inbound := &cobot.InboundMessage{
		Platform:    ch.platform,
		ChatID:     chatID,
		ChatType:   ptrStr(msgData.ChatType),
		SenderID:   senderID,
		Text:       text,
		MessageType: msgType,
		MessageID:  messageID,
		MediaURLs:  mediaURLs,
		MediaTypes: mediaTypes,
		Raw:        []byte(rawContent),
	}

	ch.handlerMu.RLock()
	handler := ch.handler
	ch.handlerMu.RUnlock()

	if handler != nil {
		handler(ctx, inbound)
	}
	return nil
}

// isMediaMessageType returns true for Feishu message types that carry
// binary/audio/file content rather than text.
func isMediaMessageType(t string) bool {
	switch t {
	case "image", "audio", "video", "file", "media", "sticker":
		return true
	}
	return false
}

// SendMessage sends a text or rich (post) message to a Feishu chat.
// If msg.RichContent is non-empty, it is sent as a post message;
// otherwise msg.Text is sent as plain text.
func (ch *FeishuChannel) SendMessage(ctx context.Context, msg *cobot.OutboundMessage) (*cobot.SendResult, error) {
	if !ch.IsAlive() {
		return nil, fmt.Errorf("feishu channel %s is closed", ch.ID())
	}
	if msg.ReceiveID == "" {
		return nil, fmt.Errorf("feishu SendMessage: receive_id is required")
	}

	var msgType string
	var content string
	if msg.RichContent != "" {
		msgType = "post"
		content = msg.RichContent
	} else {
		msgType = "text"
		content = formatTextContent(msg.Text)
	}

	resp, err := ch.client.Im.V1.Message.Create(ctx,
		larkim.NewCreateMessageReqBuilder().
			ReceiveIdType("chat_id").
			Body(larkim.NewCreateMessageReqBodyBuilder().
				ReceiveId(msg.ReceiveID).
				MsgType(msgType).
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
	slog.Debug("feishu: message sent", "channel", ch.ID(), "chat_id", msg.ReceiveID, "message_id", messageID, "type", msgType)
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

// buildPostPayload converts markdown text to Feishu post JSON format.
// It wraps the content in zh_cn locale and isolates fenced code blocks
// into dedicated rows to avoid Feishu renderer issues.
func buildPostPayload(content string) string {
	rows := buildMarkdownPostRows(content)
	payload := map[string]any{
		"zh_cn": map[string]any{
			"content": rows,
		},
	}
	data, _ := json.Marshal(payload) // always succeeds
	return string(data)
}

// buildMarkdownPostRows converts markdown content into Feishu post row format.
// Fenced code blocks are isolated into their own rows so the Feishu renderer
// doesn't swallow trailing content.
func buildMarkdownPostRows(content string) [][]map[string]string {
	if content == "" {
		return [][]map[string]string{{{"tag": "text", "text": ""}}}
	}

	// Fast path: no code fences.
	if !containsCodeFence(content) {
		return [][]map[string]string{{{"tag": "md", "text": content}}}
	}

	rows := make([][]map[string]string, 0)
	var current []string
	inCodeBlock := false

	flush := func() {
		if len(current) == 0 {
			return
		}
		segment := strings.Join(current, "\n")
		if strings.TrimSpace(segment) != "" {
			rows = append(rows, []map[string]string{{"tag": "md", "text": segment}})
		}
		current = nil
	}

	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		isFenceOpen := !inCodeBlock && strings.HasPrefix(trimmed, "```")
		isFenceClose := inCodeBlock && trimmed == "```"

		if isFenceOpen || isFenceClose {
			if !inCodeBlock {
				flush()
			}
			current = append(current, line)
			inCodeBlock = !inCodeBlock
			if inCodeBlock {
				continue
			}
			flush()
			continue
		}
		current = append(current, line)
	}
	flush()

	if len(rows) == 0 {
		return [][]map[string]string{{{"tag": "md", "text": content}}}
	}
	return rows
}

// containsCodeFence returns true if content contains fenced code blocks.
func containsCodeFence(content string) bool {
	return strings.Contains(content, "```")
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
