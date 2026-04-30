package channel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkdispatch "github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/larksuite/oapi-sdk-go/v3/ws"

	cobot "github.com/cobot-agent/cobot/pkg"
)

// FeishuConfig holds Feishu-specific credentials and settings.
type FeishuConfig struct {
	AppID     string
	AppSecret string
	Domain    string // "feishu" or "lark"
}

// FeishuChannel implements cobot.MessageChannel and exposes an HTTP webhook handler for Feishu/Lark integration.
// It receives messages via WebSocket long connection and sends replies through the Lark IM API.
type FeishuChannel struct {
	*cobot.BaseChannel
	platform string
	config   FeishuConfig
	client   *lark.Client

	// httpClient and tokenCache bypass the SDK builder to support reply_to_message_id.
	httpClient *http.Client
	tokenCache tokenCache

	dispatcher *larkdispatch.EventDispatcher
	wsClient   *ws.Client
	bgWg       sync.WaitGroup

	handler   func(ctx context.Context, msg *cobot.InboundMessage)
	handlerMu sync.RWMutex

	eventHandler   func(ctx context.Context, event *cobot.ChannelEvent)
	eventHandlerMu sync.RWMutex

	// sentIDs records message IDs this channel sent itself. Lark redelivers
	// some bot-authored messages back through the WS event stream as
	// P2MessageReceiveV1 with sender_type values that vary across tenants
	// (observed: missing/empty in production), so the SenderType check in
	// handleReceive is unreliable on its own. Tracking our own outbound IDs
	// is the only deterministic way to suppress self-echo reactions.
	sentIDs sync.Map
	// seenIDs deduplicates inbound message IDs at channel level. Gateway
	// dedup runs inside the handler, but auto-react fires *before* the
	// handler is invoked, so WS redelivery would otherwise trigger
	// duplicate 👍 reactions on the same user message.
	seenIDs sync.Map
}

// tokenCache holds a cached tenant_access_token with its expiry time.
type tokenCache struct {
	token  string
	expire time.Time
	mu     sync.RWMutex
}

// NewFeishuChannel creates a FeishuChannel with the given ID and config.
func NewFeishuChannel(id string, cfg FeishuConfig) *FeishuChannel {
	ch := &FeishuChannel{
		BaseChannel: cobot.NewBaseChannel(id),
		platform:    "feishu",
		config:      cfg,
		client:      lark.NewClient(cfg.AppID, cfg.AppSecret),
		httpClient:  &http.Client{Timeout: 10 * time.Second},
	}
	ch.dispatcher = larkdispatch.NewEventDispatcher("", "").
		OnP2MessageReceiveV1(ch.handleReceive).
		OnP2MessageReactionCreatedV1(ch.handleReactionCreated).
		OnP2MessageReactionDeletedV1(ch.handleReactionDeleted).
		OnP2MessageRecalledV1(ch.handleMessageRecalled).
		OnP2ChatMemberUserAddedV1(ch.handleMemberAdded).
		OnP2ChatMemberUserDeletedV1(ch.handleMemberRemoved)
	ch.wsClient = ws.NewClient(cfg.AppID, cfg.AppSecret, ws.WithEventHandler(ch.dispatcher))
	return ch
}

func (ch *FeishuChannel) Platform() string { return ch.platform }

// OnMessage registers the inbound message callback.
func (ch *FeishuChannel) OnMessage(handler func(ctx context.Context, msg *cobot.InboundMessage)) {
	ch.handlerMu.Lock()
	ch.handler = handler
	ch.handlerMu.Unlock()
}

// Start implements MessageChannel. It initiates the WebSocket long connection.
func (ch *FeishuChannel) Start(ctx context.Context) error {
	slog.Info("feishu: starting websocket connection", "channel", ch.ID())
	ch.bgWg.Add(1)
	go func() {
		defer ch.bgWg.Done()
		_ = ch.wsClient.Start(ctx)
	}()
	return nil
}

// Close shuts down the FeishuChannel.
func (ch *FeishuChannel) Close() {
	if ch.BaseChannel.TryClose() {
		ch.bgWg.Wait()
		slog.Info("feishu: channel closed", "channel", ch.ID())
	}
}

// handleReceive is the Lark SDK callback for incoming messages.
func (ch *FeishuChannel) handleReceive(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
	if event == nil || event.Event == nil || event.Event.Message == nil {
		return nil
	}

	// Skip messages from the bot itself to avoid self-reactions and feedback loops.
	// Per Lark SDK docs (im.message.receive_v1), sender_type values are "user" | "bot";
	// "bot" indicates a message originated from an app/bot, including this one's own
	// outbound messages that echo back via the websocket event stream.
	if event.Event.Sender != nil {
		if st := ptrStr(event.Event.Sender.SenderType); st == "bot" || st == "app" {
			return nil
		}
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

	if messageID != "" {
		if _, ours := ch.sentIDs.Load(messageID); ours {
			slog.Debug("feishu: skip self-echo by id", "message_id", messageID)
			return nil
		}
		if _, dup := ch.seenIDs.LoadOrStore(messageID, time.Now()); dup {
			slog.Debug("feishu: skip duplicate inbound", "message_id", messageID)
			return nil
		}
	}

	if event.Event.Sender != nil {
		slog.Debug("feishu: inbound sender",
			"sender_type", ptrStr(event.Event.Sender.SenderType),
			"tenant_key", ptrStr(event.Event.Sender.TenantKey),
			"message_id", messageID)
	}

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
		ChatID:      chatID,
		ChatType:    ptrStr(msgData.ChatType),
		SenderID:    senderID,
		Text:        text,
		MessageType: msgType,
		MessageID:   messageID,
		MediaURLs:   mediaURLs,
		MediaTypes:  mediaTypes,
		Raw:         []byte(rawContent),
	}

	// Auto-react synchronously BEFORE invoking the handler. This guarantees the
	// 👍 reaction appears before any reply text, since reply is dispatched by
	// the handler. An async goroutine would race the reply HTTP call.
	_ = ch.ReactMessage(ctx, messageID, "👍")

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

// Send dispatches to the correct Feishu IM API based on MsgType.
// If MsgType is empty, defaults to "text".
func (ch *FeishuChannel) Send(ctx context.Context, msg *cobot.OutboundMessage) (*cobot.SendResult, error) {
	if !ch.IsAlive() {
		return nil, fmt.Errorf("feishu channel %s is closed", ch.ID())
	}
	if msg.ReceiveID == "" {
		return nil, fmt.Errorf("feishu Send: receive_id is required")
	}

	msgType := cobot.OutboundMessageType(msg.MsgType)
	if msgType == "" {
		msgType = cobot.OutboundMsgTypeText
	}

	var content string
	switch msgType {
	case cobot.OutboundMsgTypePost, cobot.OutboundMsgTypeInteractive:
		content = msg.RichContent
		if content == "" {
			return nil, fmt.Errorf("feishu Send: rich_content required for %s", msgType)
		}
	case cobot.OutboundMsgTypeImage:
		return ch.sendImageKey(ctx, msg.ReceiveID, msg.ImageKey)
	case cobot.OutboundMsgTypeAudio, cobot.OutboundMsgTypeVideo, cobot.OutboundMsgTypeFile, cobot.OutboundMsgTypeMedia:
		return ch.sendMediaKey(ctx, msg.ReceiveID, msg.MediaKey, string(msgType))
	case cobot.OutboundMsgTypeText:
		content = buildPostPayload(msg.Text)
	default:
		content = buildPostPayload(msg.Text)
	}

	slog.Info("feishu: Send dispatch", "channel", ch.ID(), "chat_id", msg.ReceiveID, "msg_type", msgType, "reply_to", msg.ReplyToMessageID, "text_len", len(msg.Text))

	// Use direct HTTP for reply_to_message_id support (SDK builder doesn't support it).
	if msg.ReplyToMessageID != "" {
		return ch.sendReplyTo(ctx, msg)
	}

	resp, err := ch.client.Im.V1.Message.Create(ctx,
		larkim.NewCreateMessageReqBuilder().
			ReceiveIdType("chat_id").
			Body(larkim.NewCreateMessageReqBodyBuilder().
				ReceiveId(msg.ReceiveID).
				MsgType(string(msgType)).
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
	if messageID != "" {
		ch.sentIDs.Store(messageID, time.Now())
	}
	slog.Debug("feishu: message sent", "channel", ch.ID(), "chat_id", msg.ReceiveID, "message_id", messageID, "type", msgType)
	return &cobot.SendResult{Success: true, MessageID: messageID}, nil
}

// sendReplyTo sends a message with reply_to_message_id via direct HTTP.
// This bypasses the SDK builder which doesn't support the reply_to query param.
func (ch *FeishuChannel) sendReplyTo(ctx context.Context, msg *cobot.OutboundMessage) (*cobot.SendResult, error) {
	msgType := msg.MsgType
	if msgType == "" {
		msgType = cobot.OutboundMsgTypeText
	}

	var content string
	switch msgType {
	case cobot.OutboundMsgTypePost, cobot.OutboundMsgTypeInteractive:
		content = msg.RichContent
	default:
		content = buildPostPayload(msg.Text)
		msgType = cobot.OutboundMsgTypePost
	}

	body := map[string]any{
		"msg_type":        msgType,
		"content":         content,
		"reply_in_thread": false,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("feishu reply marshal: %w", err)
	}

	token, err := ch.getTenantToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("feishu reply: get token: %w", err)
	}

	url := fmt.Sprintf("https://open.%s.cn/open-apis/im/v1/messages/%s/reply", ch.config.Domain, msg.ReplyToMessageID)
	slog.Info("feishu: sendReplyTo", "url", url, "reply_to", msg.ReplyToMessageID, "msg_type", msgType, "payload", string(payload))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("feishu reply request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := ch.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("feishu reply: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	slog.Info("feishu: reply API response", "status", resp.StatusCode, "body", string(respBody))
	if resp.StatusCode >= 300 {
		slog.Warn("feishu: reply failed", "status", resp.StatusCode, "body", string(respBody))
		return &cobot.SendResult{Success: false}, fmt.Errorf("feishu reply returned %d: %s", resp.StatusCode, respBody)
	}

	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			MessageID string `json:"message_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return &cobot.SendResult{Success: false}, fmt.Errorf("feishu reply parse: %w", err)
	}
	if result.Code != 0 {
		return &cobot.SendResult{Success: false}, fmt.Errorf("feishu reply API error %d: %s", result.Code, result.Msg)
	}

	if result.Data.MessageID != "" {
		ch.sentIDs.Store(result.Data.MessageID, time.Now())
	}

	slog.Debug("feishu: reply sent", "channel", ch.ID(), "message_id", result.Data.MessageID, "reply_to", msg.ReplyToMessageID)
	return &cobot.SendResult{Success: true, MessageID: result.Data.MessageID}, nil
}

// getTenantToken returns a cached tenant_access_token, refreshing if expired.
func (ch *FeishuChannel) getTenantToken(ctx context.Context) (string, error) {
	ch.tokenCache.mu.RLock()
	token := ch.tokenCache.token
	expire := ch.tokenCache.expire
	ch.tokenCache.mu.RUnlock()

	if token != "" && time.Now().Before(expire.Add(-30*time.Second)) {
		return token, nil
	}

	// Refresh token.
	ch.tokenCache.mu.Lock()
	defer ch.tokenCache.mu.Unlock()
	// Re-check after acquiring write lock.
	if time.Now().Before(ch.tokenCache.expire.Add(-30 * time.Second)) {
		return ch.tokenCache.token, nil
	}

	body := map[string]string{
		"app_id":     ch.config.AppID,
		"app_secret": ch.config.AppSecret,
	}
	payload, _ := json.Marshal(body)
	resp, err := ch.httpClient.Post(
		fmt.Sprintf("https://open.%s.cn/open-apis/auth/v3/tenant_access_token/internal", ch.config.Domain),
		"application/json", bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("feishu token request: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Code     int    `json:"code"`
		Token    string `json:"tenant_access_token"`
		ExpireIn int    `json:"expire"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("feishu token parse: %w", err)
	}
	if result.Code != 0 || result.Token == "" {
		return "", fmt.Errorf("feishu token API error %d", result.Code)
	}

	ch.tokenCache.token = result.Token
	ch.tokenCache.expire = time.Now().Add(time.Duration(result.ExpireIn) * time.Second)
	return ch.tokenCache.token, nil
}

// sendImageKey sends an image by Feishu resource key.
func (ch *FeishuChannel) sendImageKey(ctx context.Context, chatID, imageKey string) (*cobot.SendResult, error) {
	if imageKey == "" {
		return nil, fmt.Errorf("feishu sendImageKey: image_key is required")
	}
	payload := map[string]string{"image_key": imageKey}
	content, _ := json.Marshal(payload)
	resp, err := ch.client.Im.V1.Message.Create(ctx,
		larkim.NewCreateMessageReqBuilder().
			ReceiveIdType("chat_id").
			Body(larkim.NewCreateMessageReqBodyBuilder().
				ReceiveId(chatID).
				MsgType("image").
				Content(string(content)).
				Build()).
			Build(),
	)
	if err != nil {
		return &cobot.SendResult{Success: false}, fmt.Errorf("feishu send image: %w", err)
	}
	messageID := ""
	if resp != nil && resp.Data != nil {
		messageID = ptrStr(resp.Data.MessageId)
	}
	if messageID != "" {
		ch.sentIDs.Store(messageID, time.Now())
	}
	return &cobot.SendResult{Success: true, MessageID: messageID}, nil
}

// ReactMessage implements cobot.Reactioner. It adds an emoji reaction to a message.
func (ch *FeishuChannel) ReactMessage(ctx context.Context, messageID, reactionType string) error {
	if messageID == "" {
		return fmt.Errorf("feishu ReactMessage: message_id is required")
	}
	emojiType := reactionType
	// Map common Unicode emoji to Feishu shortcodes if needed.
	shortcode := unicodeToFeishuEmoji(reactionType)
	if shortcode != "" {
		emojiType = shortcode
	}
	resp, err := ch.client.Im.V1.MessageReaction.Create(ctx,
		larkim.NewCreateMessageReactionReqBuilder().
			MessageId(messageID).
			Body(larkim.NewCreateMessageReactionReqBodyBuilder().
				ReactionType(&larkim.Emoji{EmojiType: &emojiType}).
				Build()).
			Build(),
	)
	if err != nil {
		return fmt.Errorf("feishu add reaction: %w", err)
	}
	slog.Debug("feishu: reaction added", "message_id", messageID, "type", emojiType, "reaction_id", ptrStr(resp.Data.ReactionId))
	return nil
}

// unicodeToFeishuEmoji maps common Unicode emoji to Feishu emoji shortcodes.
// Returns "" if no mapping exists (pass-through).
func unicodeToFeishuEmoji(unicode string) string {
	switch unicode {
	case "👍":
		return "OK"
	case "❤️", "💗", "💖":
		return "Heart"
	case "😂":
		return "emoji_ laugh"
	case "😮":
		return "Scream"
	case "😢":
		return "Bawl"
	case "😠":
		return "Rage"
	case "🎉":
		return "tada"
	case "👀":
		return "Eyes"
	}
	return ""
}
func (ch *FeishuChannel) sendMediaKey(ctx context.Context, chatID, mediaKey, msgType string) (*cobot.SendResult, error) {
	if mediaKey == "" {
		return nil, fmt.Errorf("feishu sendMediaKey: media_key is required")
	}
	payload := map[string]string{"file_key": mediaKey}
	content, _ := json.Marshal(payload)
	resp, err := ch.client.Im.V1.Message.Create(ctx,
		larkim.NewCreateMessageReqBuilder().
			ReceiveIdType("chat_id").
			Body(larkim.NewCreateMessageReqBodyBuilder().
				ReceiveId(chatID).
				MsgType(msgType).
				Content(string(content)).
				Build()).
			Build(),
	)
	if err != nil {
		return &cobot.SendResult{Success: false}, fmt.Errorf("feishu send %s: %w", msgType, err)
	}
	messageID := ""
	if resp != nil && resp.Data != nil {
		messageID = ptrStr(resp.Data.MessageId)
	}
	if messageID != "" {
		ch.sentIDs.Store(messageID, time.Now())
	}
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
				MsgType(string(cobot.OutboundMsgTypePost)).
				Content(buildPostPayload(content)).
				Build()).
			Build(),
	)
	if err != nil {
		return &cobot.SendResult{Success: false}, fmt.Errorf("feishu edit message: %w", err)
	}
	return &cobot.SendResult{Success: true, MessageID: messageID}, nil
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

// OnEvent registers a callback for Feishu system events.
func (ch *FeishuChannel) OnEvent(handler func(ctx context.Context, event *cobot.ChannelEvent)) {
	ch.eventHandlerMu.Lock()
	ch.eventHandler = handler
	ch.eventHandlerMu.Unlock()
}

func (ch *FeishuChannel) dispatchEvent(ctx context.Context, event *cobot.ChannelEvent) {
	ch.eventHandlerMu.RLock()
	h := ch.eventHandler
	ch.eventHandlerMu.RUnlock()
	if h != nil {
		h(ctx, event)
	}
}

func (ch *FeishuChannel) handleReactionCreated(ctx context.Context, event *larkim.P2MessageReactionCreatedV1) error {
	if event == nil || event.Event == nil {
		return nil
	}
	e := event.Event
	ch.dispatchEvent(ctx, &cobot.ChannelEvent{
		Type:         cobot.ChannelEventMessageReaction,
		Platform:     "feishu",
		Timestamp:    ptrStr(e.ActionTime),
		ChatID:       "", // reaction events don't include ChatId in this SDK version
		MessageID:    ptrStr(e.MessageId),
		UserID:       ptrStrUserId(e.UserId),
		ReactionType: ptrStrEmoji(e.ReactionType),
	})
	return nil
}

func (ch *FeishuChannel) handleReactionDeleted(ctx context.Context, event *larkim.P2MessageReactionDeletedV1) error {
	if event == nil || event.Event == nil {
		return nil
	}
	e := event.Event
	ch.dispatchEvent(ctx, &cobot.ChannelEvent{
		Type:         cobot.ChannelEventMessageReaction,
		Platform:     "feishu",
		Timestamp:    ptrStr(e.ActionTime),
		ChatID:       "",
		MessageID:    ptrStr(e.MessageId),
		UserID:       ptrStrUserId(e.UserId),
		ReactionType: ptrStrEmoji(e.ReactionType),
	})
	return nil
}

func (ch *FeishuChannel) handleMessageRecalled(ctx context.Context, event *larkim.P2MessageRecalledV1) error {
	if event == nil || event.Event == nil {
		return nil
	}
	e := event.Event
	ch.dispatchEvent(ctx, &cobot.ChannelEvent{
		Type:      cobot.ChannelEventMessageRecalled,
		Platform:  "feishu",
		Timestamp: ptrStr(e.RecallTime),
		ChatID:    ptrStr(e.ChatId),
		MessageID: ptrStr(e.MessageId),
		UserID:    "", // OperatorId not available in this event data
	})
	return nil
}

func (ch *FeishuChannel) handleMemberAdded(ctx context.Context, event *larkim.P2ChatMemberUserAddedV1) error {
	if event == nil || event.Event == nil {
		return nil
	}
	e := event.Event
	memberID := ptrStrUserId(e.OperatorId)
	ch.dispatchEvent(ctx, &cobot.ChannelEvent{
		Type:      cobot.ChannelEventMemberJoined,
		Platform:  "feishu",
		Timestamp: "",
		ChatID:    ptrStr(e.ChatId),
		UserID:    memberID,
		MemberID:  memberID,
	})
	return nil
}

func (ch *FeishuChannel) handleMemberRemoved(ctx context.Context, event *larkim.P2ChatMemberUserDeletedV1) error {
	if event == nil || event.Event == nil {
		return nil
	}
	e := event.Event
	memberID := ptrStrUserId(e.OperatorId)
	ch.dispatchEvent(ctx, &cobot.ChannelEvent{
		Type:      cobot.ChannelEventMemberLeft,
		Platform:  "feishu",
		Timestamp: "",
		ChatID:    ptrStr(e.ChatId),
		UserID:    memberID,
		MemberID:  memberID,
	})
	return nil
}

// ptrStr safely dereferences a *string, returning "" for nil.
func ptrStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ptrStrUserId extracts the user_id string from a *UserId pointer.
// Prefers OpenId if available, falls back to UserId.
func ptrStrUserId(u *larkim.UserId) string {
	if u == nil {
		return ""
	}
	if u.OpenId != nil && *u.OpenId != "" {
		return *u.OpenId
	}
	if u.UserId != nil {
		return *u.UserId
	}
	if u.UnionId != nil {
		return *u.UnionId
	}
	return ""
}

// ptrStrEmoji extracts the emoji type string from an *Emoji.
func ptrStrEmoji(e *larkim.Emoji) string {
	if e == nil || e.EmojiType == nil {
		return ""
	}
	return *e.EmojiType
}
