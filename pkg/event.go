package cobot

// Event represents a streaming event emitted during agent execution.
type Event struct {
	Type     EventType `json:"type"`
	Content  string    `json:"content,omitempty"`
	ToolCall *ToolCall `json:"tool_call,omitempty"`
	Usage    *Usage    `json:"usage,omitempty"`
	Done     bool      `json:"done"`
	Error    string    `json:"error,omitempty"`
}

// EventType classifies a streaming event.
type EventType string

const (
	EventText       EventType = "text"
	EventToolCall   EventType = "tool_call"
	EventToolResult EventType = "tool_result"
	EventToolStart  EventType = "tool_start"
	EventDone       EventType = "done"
	EventError      EventType = "error"
)
