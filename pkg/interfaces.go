package cobot

import (
	"context"
	"encoding/json"
)

type Provider interface {
	Name() string
	Complete(ctx context.Context, req *ProviderRequest) (*ProviderResponse, error)
	Stream(ctx context.Context, req *ProviderRequest) (<-chan ProviderChunk, error)
}

// ModelValidator is an optional interface that Providers can implement to
// verify that a model name is valid before accepting a model switch.
type ModelValidator interface {
	ValidateModel(ctx context.Context, model string) error
}

// ModelResolver resolves a "provider:model" spec into a Provider and model name.
// Implemented by llm.Registry; used by Agent for multi-provider model switching.
type ModelResolver interface {
	ProviderForModel(modelSpec string) (Provider, string, error)
	ValidateModel(ctx context.Context, modelSpec string) error
}

type Tool interface {
	Name() string
	Description() string
	Parameters() json.RawMessage
	Execute(ctx context.Context, args json.RawMessage) (string, error)
}

// ToolRegistry manages available tools. Implemented by tools.Registry.
type ToolRegistry interface {
	Register(tool Tool)
	Get(name string) (Tool, error)
	ToolDefs() []ToolDef
	Execute(ctx context.Context, call ToolCall) (*ToolResult, error)
	ExecuteParallel(ctx context.Context, calls []ToolCall) []*ToolResult
	Clone() ToolRegistry
	IsStreamingTool(name string) bool
	Without(names ...string) ToolRegistry
}

// SubAgent is a minimal interface for agent delegation, allowing tools to
// invoke sub-agents without importing the agent package directly.
type SubAgent interface {
	SetModel(spec string) error
	SetSystemPrompt(prompt string) error
	Prompt(ctx context.Context, message string) (*ProviderResponse, error)
	// Stream runs the agent in streaming mode and returns an event channel.
	// The channel is closed when the agent loop finishes.
	Stream(ctx context.Context, message string) (<-chan Event, error)
}

// StreamingTool extends Tool with streaming execution support.
// Tools that implement this interface can push intermediate events
// (e.g. sub-agent text chunks) to the caller during execution,
// rather than blocking until a final result is available.
type StreamingTool interface {
	Tool
	// ExecuteStream runs the tool and sends intermediate events on eventCh.
	// The final tool result is still returned as (string, error).
	// eventCh is caller-owned; the tool MUST NOT close it.
	ExecuteStream(ctx context.Context, args json.RawMessage, eventCh chan<- Event) (string, error)
}

// MemoryStore handles persistence: storing content and searching it.
type MemoryStore interface {
	Store(ctx context.Context, content, tier1, tier2 string) (string, error)
	StoreByName(ctx context.Context, content, wingName, roomName, hallType string) (string, error)
	Search(ctx context.Context, query *SearchQuery) ([]*SearchResult, error)
	// ConsolidateByName summarizes drawers in the named room into a closet.
	// It resolves wing/room by name. No-op if the wing or room does not exist.
	ConsolidateByName(ctx context.Context, wingName, roomName string) error
	Close() error
}

// MemoryRecall handles prompt assembly from stored memories. Implementations
// can be swapped independently from the storage backend, allowing different
// recall strategies (e.g. facts-only, deep-search, RAG) without changing
// how content is persisted.
type MemoryRecall interface {
	WakeUp(ctx context.Context) (string, error)
}
