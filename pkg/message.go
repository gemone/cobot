package cobot

import (
	"encoding/json"
	"time"
)

// Role represents the sender of a message in a conversation.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is a single entry in a conversation.
type Message struct {
	Role       Role           `json:"role"`
	Content    string         `json:"content"`
	ToolCalls  []ToolCall     `json:"tool_calls,omitempty"`
	ToolResult *ToolResult    `json:"tool_result,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

type AttachmentType string

const (
	AttachmentImage AttachmentType = "image"
	AttachmentFile  AttachmentType = "file"
	AttachmentAudio AttachmentType = "audio"
	AttachmentVideo AttachmentType = "video"
)

type Attachment struct {
	Type     AttachmentType `json:"type,omitempty"`
	Path     string         `json:"path,omitempty"`
	URL      string         `json:"url,omitempty"`
	Filename string         `json:"filename,omitempty"`
	Caption  string         `json:"caption,omitempty"`
}

// OutboundMessageType describes the type of outbound message.
// Used by MessageChannel implementations to dispatch to the correct API.
type OutboundMessageType string

const (
	OutboundMsgTypeText        OutboundMessageType = "text"
	OutboundMsgTypePost        OutboundMessageType = "post"
	OutboundMsgTypeImage       OutboundMessageType = "image"
	OutboundMsgTypeAudio       OutboundMessageType = "audio"
	OutboundMsgTypeVideo       OutboundMessageType = "video"
	OutboundMsgTypeFile        OutboundMessageType = "file"
	OutboundMsgTypeMedia       OutboundMessageType = "media"       // sticker, etc.
	OutboundMsgTypeInteractive OutboundMessageType = "interactive" // card/button
)

// OutboundMessage describes a single outbound IM message.
// The MsgType field determines which content field is used:
//   - text:     Text field (plain string)
//   - post:     RichContent field (Feishu post JSON)
//   - image:    ImageKey field (Feishu image key)
//   - audio:    MediaKey field (Feishu audio key)
//   - video:    MediaKey field (Feishu video key)
//   - file:     MediaKey field (Feishu file key)
//   - media:    MediaKey field (Feishu media key)
//   - interactive: RichContent field (card JSON)
type OutboundMessage struct {
	ReceiveID        string              `json:"receive_id,omitempty"`
	ReceiveType      string              `json:"receive_type,omitempty"`
	ReceiveIDType    string              `json:"receive_id_type,omitempty"`
	MsgType          OutboundMessageType `json:"msg_type,omitempty"` // defaults to "text" if empty
	Text             string              `json:"text,omitempty"`
	RichContent      string              `json:"rich_content,omitempty"` // Feishu post/card JSON
	ImageKey         string              `json:"image_key,omitempty"`    // Feishu image resource key
	MediaKey         string              `json:"media_key,omitempty"`    // audio/video/file resource key
	Attachments      []Attachment        `json:"attachments,omitempty"`
	ReplyTo          string              `json:"reply_to,omitempty"`
	ReplyToMessageID string              `json:"reply_to_message_id,omitempty"` // Feishu reply-to message ID
	UUID             string              `json:"uuid,omitempty"`
}

type InboundMessage struct {
	Platform    string          `json:"platform,omitempty"`
	ChatID      string          `json:"chat_id,omitempty"`
	ChatType    string          `json:"chat_type,omitempty"`
	SenderID    string          `json:"sender_id,omitempty"`
	SenderName  string          `json:"sender_name,omitempty"`
	Text        string          `json:"text,omitempty"`
	MessageType string          `json:"message_type,omitempty"`
	MediaURLs   []string        `json:"media_urls,omitempty"`
	MediaTypes  []string        `json:"media_types,omitempty"`
	ReplyToID   string          `json:"reply_to_id,omitempty"`
	MessageID   string          `json:"message_id,omitempty"`
	Timestamp   time.Time       `json:"timestamp,omitempty"`
	Raw         json.RawMessage `json:"raw,omitempty"`
}

type SendResult struct {
	Success   bool   `json:"success"`
	MessageID string `json:"message_id,omitempty"`
}
