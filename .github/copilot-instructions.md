# Cobot – Copilot Instructions

## Build, Test & Lint

```bash
make check          # fmt + vet + all tests (the standard pre-commit gate)
make build          # compile to ./build/cobot
make test           # go test -v ./...
make test-race      # tests with race detector

# Single package
go test ./internal/memory/...
go test ./internal/agent/...

# Single test
go test -run TestFoo ./internal/agent/...
```

`make check` enforces `go fmt`; always format before committing.

## Architecture Overview

### Public API surface (`pkg/`)
`pkg/` is the `cobot` package — the only package that crosses module boundaries. It defines all shared interfaces:
- `cobot.Tool` / `cobot.ToolRegistry` — tool system contracts
- `cobot.Provider` / `cobot.ModelResolver` — LLM provider contracts
- `cobot.Channel` / `cobot.MessageChannel` / `cobot.EditableChannel` — messaging channel hierarchy
- `cobot.MemoryStore`, `cobot.MemoryRecall`, `cobot.ShortTermMemory` — memory contracts
- `cobot.SubAgent` — sub-agent delegation contract

`internal/` packages import from `pkg/`, never the reverse.

### Composition root (`internal/bootstrap`)
`bootstrap.InitAgent` is the single wiring point for the entire agent runtime. It resolves the workspace, initialises tools/memory/sandbox/cron, and returns a `*bootstrap.Result`. CLI commands call `InitAgent`; they do not assemble components themselves.

### Startup order
1. Resolve config (defaults → config file → env → workspace overlay)
2. Discover/resolve workspace via `workspace.Manager`
3. Build `tools.Registry`, create `agent.Agent`
4. Attach `channel.Manager` to agent
5. Register sandboxed tools (filesystem, shell, memory, workspace mutation, cron, delegate)
6. Load skills → merge into system prompt
7. Open LTM `memory.db` + per-session STM stores

### Key internal packages
| Package | Responsibility |
|---------|---------------|
| `internal/agent` | Conversation loop, streaming, compaction, session mgmt, usage |
| `internal/workspace` | Workspace discovery, layout, sandbox boundary calculation |
| `internal/llm` | Provider registry; lazy init from `provider:model` spec |
| `internal/tools` | Tool registry + all built-in tool implementations |
| `internal/memory` | SQLite-backed LTM (WAL+FTS5) + per-session STM |
| `internal/sandbox` | Path resolution/rewriting, shell parsing, OS-level enforcement |
| `internal/channel` | Channel manager (Register/Unregister), Feishu, reverse channel |
| `internal/gateway` | HTTP server bridging external platforms to the agent |
| `internal/cron` | Scheduled job store with optimistic concurrency |
| `internal/bootstrap` | Composition root |

## Key Conventions

### Tool implementation pattern
Implement `cobot.Tool`. Embed `BasicTool` (which embeds `sandboxTool`) to inherit sandbox-aware description formatting and avoid boilerplate:

```go
type MyTool struct {
    tools.BasicTool
    // your fields
}
```

All filesystem and shell tools receive a `*sandbox.Sandbox` (not raw `*sandbox.SandboxConfig`). Use `sb.Resolve(path, write)` for path validation and `sb.RewriteError(err)` to translate real paths back to virtual ones in error messages.

### Model spec format
Models are always specified as `"provider:model"`, e.g. `"openai:gpt-4o"` or `"anthropic:claude-sonnet-4-20250514"`. Use `llm.Registry.ProviderForModel(spec)` to resolve.

### Channel IDs
Gateway channel IDs must be non-empty and match `[a-z0-9\-_:]` (no dots or spaces).

### Memory / SQLite conventions
- SQLite DBs use WAL mode, `busy_timeout`, `foreign_keys`, and `SetMaxOpenConns(1)`.
- All timestamps stored as UTC text. Always call `.UTC()` before formatting with `sqliteTimeFmt` to preserve lexicographic ordering.
- Memory hierarchy: **Wing → Room → Drawer** (raw entries, FTS5-indexed) **→ Closet** (summarised).

### Cron optimistic concurrency
Cron jobs use a `read_id` token (`<job_id>:<read_token>`). Every `Store.Get`/`List` refreshes the token. Mutations (`Update`, `Delete`) must pass the latest token; stale tokens are rejected.

### Channel Manager sessions
After registering a channel, call `Manager.MarkLocal(sessionID)` (or periodically `Heartbeat`) to prevent the health-check from expiring and auto-closing in-process channels.

### Two-tier immutability
- `~/.config/cobot/` — **agent-immutable**; only CLI commands modify it.
- `~/.local/share/cobot/` — **agent-mutable** at runtime via workspace/agent mutation tools.

### Unsupported platform operations
Return `cobot.ErrNotSupported` from `PlatformAdapter` methods that the platform does not implement.

### Shell sandbox
`shell_exec` applies an app-layer network blocklist only when `AllowNetwork=false` **and** `sandbox.HasOSLevelEnforcement() == false`. OS-level enforcement (Linux Landlock, macOS sandbox-exec) takes precedence.
