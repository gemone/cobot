package channel

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkhttp "github.com/larksuite/oapi-sdk-go/v3/core/httpserverext"
	larkdispatch "github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"

	cobot "github.com/cobot-agent/cobot/pkg"
)

// FeishuConfig holds Feishu/Lark app credentials and settings.
type FeishuConfig struct {
	AppID             string
	AppSecret         string
	VerificationToken string
	EncryptKey        string
	DefaultChatID     string // optional: target for notification Send()
}

// FeishuChannel implements cobot.HTTPChannel for Feishu/Lark integration.
type FeishuChannel struct {
	cobot.BaseChannel
	config      FeishuConfig
	client      *lark.Client
	handler     func(ctx context.Context, msg *cobot.InboundMessage)
	httpHandler http.HandlerFunc
}

// NewFeishuChannel creates and initializes a FeishuChannel.
func NewFeishuChannel(id string, cfg FeishuConfig) (*FeishuChannel, error) {
	fc := &FeishuChannel{
		BaseChannel: *cobot.NewBaseChannel(id),
		config:      cfg,
	}

	fc.client = lark.NewClient(cfg.AppID, cfg.AppSecret)

	dispatcher := larkdispatch.NewEventDispatcher(cfg.VerificationToken, cfg.EncryptKey).
		OnP2MessageReceiveV1(fc.handleMessage)

	fc.httpHandler = larkhttp.NewEventHandlerFunc(dispatcher)
	return fc, nil
}

func (fc *FeishuChannel) Platform() string { return "feishu" }

// HTTPHandler returns the Lark SDK webhook handler wrapped as http.Handler.
func (fc *FeishuChannel) HTTPHandler() http.Handler { return fc.httpHandler }

func (fc *FeishuChannel) OnMessage(handler func(ctx context.Context, msg *cobot.InboundMessage)) {
	fc.handler = handler
}

// Send delivers a notification message to DefaultChatID (implements Channel).
func (fc *FeishuChannel) Send(ctx context.Context, msg cobot.ChannelMessage) error {
	if fc.config.DefaultChatID == "" {
		return fmt.Errorf("feishu: no default_chat_id configured for notifications")
	}
	text := msg.Title
	if msg.Content != "" {
		if text != "" {
			text += "\n\n" + msg.Content
		} else {
			text = msg.Content
		}
	}
	_, err := fc.SendMessage(ctx, &cobot.OutboundMessage{
		ReceiveID: fc.config.DefaultChatID,
		Text:      text,
	})
	return err
}

// SendMessage sends an outbound message via Feishu API.
func (fc *FeishuChannel) SendMessage(ctx context.Context, msg *cobot.OutboundMessage) (*cobot.SendResult, error) {
	if msg.ReceiveID == "" {
		return nil, fmt.Errorf("feishu: ReceiveID is required")
	}

	content := msg.RichContent
	msgType := "post"
	if content == "" {
		content = buildTextContent(msg.Text)
		msgType = "text"
	}

	resp, err := fc.client.Im.V1.Message.Create(ctx, larkim.NewCreateMessageReqBuilder().
		ReceiveIdType("chat_id").
		Body(larkim.NewCreateMessageReqBodyBuilder().
			ReceiveId(msg.ReceiveID).
			MsgType(msgType).
			Content(content).
			Build()).
		Build())
	if err != nil {
		return nil, fmt.Errorf("feishu send: %w", err)
	}
	if !resp.Success() {
		return &cobot.SendResult{Success: false}, fmt.Errorf("feishu send failed: %s", resp.Msg)
	}

	var msgID string
	if resp.Data != nil && resp.Data.MessageId != nil {
		msgID = *resp.Data.MessageId
	}
	return &cobot.SendResult{Success: true, MessageID: msgID}, nil
}

// EditMessage updates a previously sent Feishu message.
func (fc *FeishuChannel) EditMessage(ctx context.Context, chatID, messageID, content string) (*cobot.SendResult, error) {
	textContent := buildTextContent(content)
	_, err := fc.client.Im.V1.Message.Update(ctx, larkim.NewUpdateMessageReqBuilder().
		MessageId(messageID).
		Body(larkim.NewUpdateMessageReqBodyBuilder().
			Content(textContent).
			Build()).
		Build())
	if err != nil {
		return nil, fmt.Errorf("feishu edit: %w", err)
	}
	return &cobot.SendResult{Success: true, MessageID: messageID}, nil
}

// handleMessage is the Lark SDK callback for incoming messages.
func (fc *FeishuChannel) handleMessage(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
	if event == nil || event.Event == nil {
		return nil
	}
	msgData := event.Event
	if msgData.Message == nil {
		return nil
	}
	msg := msgData.Message

	var contentStr, msgType, chatType, chatID, messageID string
	if msg.Content != nil {
		contentStr = *msg.Content
	}
	if msg.MessageType != nil {
		msgType = *msg.MessageType
	}
	if msg.ChatType != nil {
		chatType = *msg.ChatType
	}
	if msg.ChatId != nil {
		chatID = *msg.ChatId
	}
	if msg.MessageId != nil {
		messageID = *msg.MessageId
	}

	text := extractTextContent(msgType, contentStr)

	// Group chats: only respond when @mentioned.
	if chatType == "group" || chatType == "topic_group" {
		text = stripMentionPrefix(text)
		if text == "" {
			return nil
		}
	}

	var senderID string
	if msgData.Sender != nil && msgData.Sender.SenderId != nil && msgData.Sender.SenderId.OpenId != nil {
		senderID = *msgData.Sender.SenderId.OpenId
	}

	inbound := &cobot.InboundMessage{
		Platform:    "feishu",
		ChatID:      chatID,
		ChatType:    chatType,
		SenderID:    senderID,
		Text:        text,
		MessageType: msgType,
		MessageID:   messageID,
		Raw:         json.RawMessage(contentStr),
	}

	slog.Debug("feishu: inbound message",
		"chat_id", chatID, "sender", senderID, "text", text, "msg_type", msgType)

	if fc.handler != nil {
		fc.handler(ctx, inbound)
	}
	return nil
}
