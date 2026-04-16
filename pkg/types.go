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

type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type ToolResult struct {
	CallID string `json:"call_id"`
	Output string `json:"output"`
	Error  string `json:"error,omitempty"`
}

type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type StopReason string

const (
	StopEndTurn   StopReason = "end_turn"
	StopMaxTokens StopReason = "max_tokens"
)

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	ReasoningTokens  int `json:"reasoning_tokens,omitempty"`
	CacheReadTokens  int `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"`
}

type ProviderRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Tools       []ToolDef `json:"tools,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Temperature float64   `json:"temperature,omitempty"`
}

type ProviderResponse struct {
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	StopReason StopReason `json:"stop_reason"`
	Usage      Usage      `json:"usage"`
}

type ProviderChunk struct {
	Content  string    `json:"content,omitempty"`
	ToolCall *ToolCall `json:"tool_call,omitempty"`
	Done     bool      `json:"done"`
	Usage    *Usage    `json:"usage,omitempty"`
}

type Event struct {
	Type     EventType `json:"type"`
	Content  string    `json:"content,omitempty"`
	ToolCall *ToolCall `json:"tool_call,omitempty"`
	Usage    *Usage    `json:"usage,omitempty"`
	Done     bool      `json:"done"`
	Error    string    `json:"error,omitempty"`
}

type EventType string

const (
	EventText       EventType = "text"
	EventToolCall   EventType = "tool_call"
	EventToolResult EventType = "tool_result"
	EventToolStart  EventType = "tool_start"
	EventDone       EventType = "done"
	EventError      EventType = "error"
)

type Wing struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Keywords []string `json:"keywords,omitempty"`
}

type Room struct {
	ID       string `json:"id"`
	WingID   string `json:"wing_id"`
	Name     string `json:"name"`
	HallType string `json:"hall_type"`
}

type Drawer struct {
	ID        string    `json:"id"`
	RoomID    string    `json:"room_id"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

type Closet struct {
	ID        string   `json:"id"`
	RoomID    string   `json:"room_id"`
	DrawerIDs []string `json:"drawer_ids"`
	Summary   string   `json:"summary"`
}

type SearchQuery struct {
	Text  string `json:"text"`
	Tier1 string `json:"tier1,omitempty"`
	Tier2 string `json:"tier2,omitempty"`
	Tag   string `json:"tag,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

type SearchResult struct {
	ID      string  `json:"id"`
	Content string  `json:"content"`
	Tier1   string  `json:"tier1"`
	Tier2   string  `json:"tier2"`
	Score   float64 `json:"score"`
}

const (
	TagFacts = "facts"
	TagLog   = "log"
	TagCode  = "code"
)

const DefaultMaxTurns = 50

const DefaultSystemPrompt = "You are Cobot, a personal AI assistant."

const DefaultSubAgentSystemPrompt = `You are a focused sub-agent delegated to complete a specific task. Be direct and efficient. Do not call delegate_task (avoid infinite recursion). You do not have access to the main agent's persistent memory store. Use the provided tools to accomplish the task and return a concise result.`

type CobotError struct {
	Code    string
	Message string
	Cause   error
}

func (e *CobotError) Error() string {
	if e.Cause != nil {
		return e.Code + ": " + e.Message + ": " + e.Cause.Error()
	}
	return e.Code + ": " + e.Message
}

func (e *CobotError) Unwrap() error { return e.Cause }

var (
	ErrProviderNotConfigured = &CobotError{Code: "PROVIDER_NOT_CONFIGURED", Message: "LLM provider not configured"}
	ErrMaxTurnsExceeded      = &CobotError{Code: "MAX_TURNS_EXCEEDED", Message: "max turns exceeded"}
)
