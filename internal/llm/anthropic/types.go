package anthropic

import "encoding/json"

type messagesRequest struct {
	Model     string    `json:"model"`
	MaxTokens int       `json:"max_tokens"`
	Messages  []message `json:"messages"`
	System    string    `json:"system,omitempty"`
	Tools     []toolDef `json:"tools,omitempty"`
	Stream    bool      `json:"stream"`
}

type message struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type textBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type toolUseBlock struct {
	Type  string          `json:"type"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

type toolResultBlock struct {
	Type      string `json:"type"`
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error,omitempty"`
}

type toolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type messagesResponse struct {
	Content    []contentBlock `json:"content"`
	StopReason string         `json:"stop_reason"`
	Usage      usage          `json:"usage"`
}

type contentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type usage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
}

// --- Stream event types ---

// streamEvent represents an Anthropic SSE event.
// The fields present depend on evt.Type:
//   - "content_block_start"  → ContentBlock is set (index + tool_use or text block)
//   - "content_block_delta"  → Delta is set (text_delta or input_json_delta)
//   - "message_delta"        → MessageDelta is set (stop_reason + usage)
//   - "message_start"        → Message is set (contains usage)
//   - "message_stop"         → none
type streamEvent struct {
	Type         string        `json:"type"`
	Index        int           `json:"index,omitempty"`
	ContentBlock *contentBlock `json:"content_block,omitempty"`
	Delta        *streamDelta  `json:"delta,omitempty"`
	MessageDelta *messageDelta `json:"message_delta,omitempty"`
	Message      *messageStart `json:"message,omitempty"`
}

type streamDelta struct {
	Type string `json:"type,omitempty"`
	Text string `json:"text,omitempty"`
	// For tool_use: partial JSON input string
	PartialJSON string `json:"partial_json,omitempty"`
}

type messageDelta struct {
	StopReason string     `json:"stop_reason,omitempty"`
	Usage      deltaUsage `json:"usage,omitempty"`
}

type deltaUsage struct {
	OutputTokens int `json:"output_tokens"`
}

type messageStart struct {
	ID    string `json:"id"`
	Model string `json:"model"`
	Usage usage  `json:"usage"`
}
