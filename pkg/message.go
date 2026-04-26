package cobot

import (
	"encoding/json"
	"time"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

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
	Type     AttachmentType
	Path     string
	URL      string
	Filename string
	Caption  string
}

type OutboundMessage struct {
	ReceiveID   string
	ReceiveType string
	Text        string
	RichContent string
	Attachments []Attachment
	ReplyTo     string
	UUID        string
}

type InboundMessage struct {
	Platform    string
	ChatID      string
	ChatType    string
	SenderID    string
	SenderName  string
	Text        string
	MessageType string
	MediaURLs   []string
	MediaTypes  []string
	ReplyToID   string
	MessageID   string
	Timestamp   time.Time
	Raw         json.RawMessage
}

type SendResult struct {
	Success   bool
	MessageID string
	Error     error
}