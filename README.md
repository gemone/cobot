# Cobot

[![Go Version](https://img.shields.io/badge/go-1.26.2-blue.svg)](https://golang.org/)
[![License](https://img.shields.io/badge/license-BSD%203--Clause-green.svg)](LICENSE)

Cobot is a multi-workspace AI agent framework with isolated per-workspace memory, persona, and skill sets. Each workspace maintains its own agents, sandbox configuration, and evolving state — enabling project-specific AI workflows that are fully isolated from one another.

## Features

- **Multi-workspace**: Each workspace has isolated memory, persona files, skills, agents, cron jobs, and sessions
- **Two-tier mutability**: System-level config (`~/.config/cobot/`) is agent-immutable; workspace data (`~/.local/share/cobot/`) is agent-mutable at runtime
- **Runtime composition root**: `internal/bootstrap` wires workspace resolution, provider registry, tool registration, memory, sandboxing, and cron into one agent runtime
- **Multi-agent per workspace**: Each workspace defines multiple agents with different models, prompts, tool sets, and session policies
- **Workspace self-evolution**: Agents can update workspace config, agent config, persona files, and workspace-private skills through dedicated tools
- **MemPalace memory**: Hierarchical memory storage (Wings -> Rooms -> Drawers -> Closets) backed by SQLite (WAL mode) with FTS5 full-text search plus per-session STM databases
- **Sandbox enforcement**: Per-workspace and per-agent filesystem and shell sandboxing
- **Scheduled automation**: Per-workspace cron jobs can run recurring or one-shot prompts and write results back into memory
- **Project discovery**: Place a `.cobot/` directory in any project root to bind it to a workspace
- **Multi-provider and sub-agent support**: OpenAI and Anthropic providers, plus internal or external delegated sub-agents

## Directory Structure

```text
~/.config/cobot/                         # System-level, agent-immutable
├── config.yaml                          # Global settings (API keys, default model, provider overrides)
└── workspaces/                          # Workspace definitions (name -> root/path mapping)
  └── <name>.yaml

~/.local/share/cobot/                    # Agent-mutable runtime data
├── logs/                                # Debug logs when --debug is enabled
├── skills/                              # Global shared skills loaded into any workspace
│   ├── <name>.yaml
│   ├── <name>.md
│   └── <name>/
│       ├── SKILL.md
│       └── scripts/
└── workspace/
  └── <workspace-name>/
    ├── workspace.yaml               # Workspace config (sandbox, enabled skills/MCP, default agent)
    ├── SOUL.md                      # Workspace persona prompt
    ├── USER.md                      # User profile / preferences
    ├── MEMORY.md                    # Optional human-authored notes alongside structured memory
    ├── memory/
    │   └── memory.db            # Long-term memory SQLite database
    ├── agents/
    │   └── <agent-name>.yaml        # Per-agent config (model, prompt, skills, session overrides)
    ├── skills/                      # Workspace-private skills
    ├── sessions/                    # Session store + per-session STM SQLite databases
    ├── cron/                        # Persisted scheduled jobs
    ├── space/                       # Scratch space / delegated workdir
    └── mcp/                         # Workspace-local MCP data
```

### Project Discovery

Add a `.cobot/` directory to any project root to bind it to a workspace:

```text
<project-root>/.cobot/
├── workspace.yaml      # Points to workspace name
└── AGENTS.md           # Project-level agent instructions
```

When running `cobot` from inside a project, it automatically detects the workspace via this file.

## Runtime Architecture

The runtime is assembled around a small set of internal packages:

- **`cmd/cobot`**: CLI entrypoints and the Bubble Tea based TUI
- **`internal/bootstrap`**: Composition root that resolves the workspace and wires the agent, providers, tools, memory, sandbox, and cron
- **`internal/agent`**: Conversation loop, event streaming, session management, compaction, and usage accounting
- **`internal/workspace`**: Workspace discovery, definitions, layout, and sandbox boundary calculation
- **`internal/llm`**: Provider registry and lazy provider initialization from `provider:model` specs
- **`internal/tools`**: Tool registry plus filesystem, shell, delegate, cron, and workspace mutation tools
- **`internal/memory`**: SQLite-backed long-term and short-term memory, search, extraction, and deep search
- **`internal/skills`**: Skill loading from the global data dir and the active workspace
- **`internal/debuglog`**: Optional session-scoped request/response logging for debugging provider traffic

Startup flow for `cobot chat` and the TUI is:

1. Resolve config from defaults, config file, environment, and workspace selection.
2. Resolve or discover the active workspace.
3. Create the agent and attach its session store.
4. Initialize the model registry and active provider.
5. Register sandboxed filesystem/shell tools, workspace tools, memory tools, delegate tooling, and cron.
6. Load enabled skills and merge them into the effective system prompt.
7. Open workspace memory (`memory/memory.db`) plus per-session STM storage under `sessions/`.

## Installation

### Prerequisites

- Go 1.26 or later
- Git

### From Source

```bash
git clone https://github.com/cobot-agent/cobot.git
cd cobot
go build -o cobot ./cmd/cobot
```

### Install to $GOPATH/bin

```bash
go install github.com/cobot-agent/cobot/cmd/cobot@latest
```

## Quick Start

### 1. Configure API Keys

```bash
cobot config set apikey.openai sk-xxx

# Or via environment variables
export OPENAI_API_KEY=sk-xxx
export ANTHROPIC_API_KEY=sk-xxx
```

### 2. Create a Workspace

```bash
cobot workspace create my-project
```

### 3. Start Chatting

```bash
# Use the default workspace
cobot chat "Explain Go interfaces"

# Target a specific workspace
cobot chat -w my-project "Review recent changes"

# Workspace auto-detected from project directory
cd /path/to/my-project
cobot chat "What tests are failing?"
```

## Workspace Selection

Workspace is resolved at runtime — there is no persistent "current workspace" state:

| Method | Example |
| ------ | ------- |
| CLI flag | `cobot chat -w my-project "hello"` |
| Environment variable | `COBOT_WORKSPACE=my-project cobot chat "hello"` |
| Project discovery | Walk up from CWD, find `.cobot/workspace.yaml` |
| Default | `default` workspace if nothing else matches |

Priority: CLI flag > environment variable > project discovery > default.

## CLI Reference

### Setup & Health

```bash
cobot setup                       # Initialize config, data dir, and default workspace
cobot doctor                      # Check configuration health
```

### Chat

```bash
cobot chat "message"              # Chat using resolved workspace
cobot chat -w my-project "message"  # Explicit workspace
cobot tui                         # Launch interactive TUI
```

Running `cobot` with no subcommand also launches the TUI.

### Workspace Management

```bash
cobot workspace list              # List workspaces
cobot workspace create <name>     # Create workspace
cobot workspace delete <name>     # Delete workspace
cobot workspace show [name]       # Show workspace config
cobot workspace project [path]    # Bind project directory to workspace
```

### Agent Management

```bash
cobot agent list                  # List agents in current workspace
cobot agent show <name>           # Show agent config
```

### Memory

```bash
cobot memory search "query"       # Full-text search across MemPalace
cobot memory store <content> <wing> <room>  # Store content in memory
cobot memory status               # Show memory storage stats
```

### Configuration

```bash
cobot config show                 # Print resolved config
cobot config set <key> <value>    # Set a config value (e.g. apikey.openai)
cobot config set-auth <provider>  # Configure API key for a provider
cobot config init                 # Initialize config file
cobot config edit                 # Open config in editor
```

### Tools & Models

```bash
cobot tools list                  # List available tools
cobot model list                  # List available models
```

## Configuration Files

### Global Config (`~/.config/cobot/config.yaml`)

```yaml
model: openai:gpt-4o
api_keys:
  openai: ${OPENAI_API_KEY}
  anthropic: ${ANTHROPIC_API_KEY}
max_turns: 50
memory_enabled: true
```

Environment variable expansion: `${VAR_NAME}` patterns are resolved at load time.

### Workspace State (`~/.local/share/cobot/workspace/<name>/workspace.yaml`)

```yaml
name: my-project
type: project
root: /path/to/project

enabled_mcp:
  - github
  - filesystem

enabled_skills:
  - code-review
  - debugging

sandbox:
  root: /path/to/project
  allow_paths:
    - /tmp
  allow_network: true

default_agent: main
external_agents:
  - name: helper
    command: /usr/local/bin/my-agent
    args: ["acp", "--port", "0"]
    workdir: /path/to/project
    timeout: 5m
```

### Agent Config (`~/.local/share/cobot/workspace/<name>/agents/<agent>.yaml`)

```yaml
name: main
model: openai:gpt-4o
system_prompt: SOUL.md
enabled_mcp:
  - github
  - filesystem
enabled_skills:
  - code-review
max_turns: 50
sandbox: {}    # Empty = inherit workspace sandbox
session:
  summarize_threshold: 0.5
  compress_threshold: 0.7
  summarize_turns: 60
  summary_model: openai:gpt-4o-mini
```

### Project Overlay Config (`<project-root>/.cobot/config.yaml`)

When a resolved workspace has a project root, Cobot also loads an optional project-local config overlay from `.cobot/config.yaml` inside that root. This affects runtime settings such as model selection without mutating the workspace state file under the data directory.

```yaml
model: anthropic:claude-sonnet-4-20250514
max_turns: 80
system_prompt: |
  Prefer concise answers for this repository.
```

## Memory Architecture (MemPalace)

Each workspace maintains an independent MemPalace backed by SQLite (WAL mode) with FTS5 full-text search. The long-term store lives in `memory.db` at the workspace root, while per-session short-term memory databases live under `sessions/`.

- **Wings**: Top-level domains (e.g., `golang`, `architecture`)
- **Rooms**: Contextual spaces within a wing, each with a tag (`facts`, `log`, or `code`)
- **Drawers**: Raw content entries within a room (indexed via FTS5 for full-text search)
- **Closets**: Summarized or aggregated content generated from drawers via `AutoSummarizeRoom`

### Search Interface (Tiered)

The search API uses generic tiered fields to decouple from the internal storage model, enabling pluggable backends:

- **Tier1**: Top-level grouping (maps to Wing in the default backend)
- **Tier2**: Second-level grouping (maps to Room)
- **Tag**: Classification tag (`facts`, `log`, `code`)
- **ID**: Entry identifier

### Dual Interface Design

Memory is split into two interfaces for flexibility:

- **MemoryStore**: Persistence — `Store(content, tier1, tier2)` and `Search(query)`
- **MemoryRecall**: Prompt assembly — `WakeUp()` builds the system prompt from stored memories

`WakeUp` collects facts from closets and recent drawer content per room. The optional deep-search mode (`WakeUpWithDeepSearch`) adds semantic search results across all memory using the L3 deep search layer.

## Sandbox Enforcement

### Filesystem

`filesystem_read` and `filesystem_write` tools enforce:

- Allowed paths: `sandbox.root` + `sandbox.allow_paths` + workspace data directory
- Readonly paths: allow read, block write
- Symlink resolution and `..` traversal prevention

### Shell

`shell_exec` tool enforces:

- Working directory forced to `sandbox.root`
- Command blocklist checked by substring match
- Network commands blocked if `sandbox.allow_network` is false

### Per-Agent Override

Agents inherit their workspace's sandbox by default. Non-empty `sandbox` fields in an agent config override the corresponding workspace settings.

## Workspace Self-Evolution

Agents can update their own workspace state through dedicated tools:

| File | Tool |
| ---- | ---- |
| `SOUL.md`, `USER.md` | `persona_update` |
| `workspace.yaml` | `workspace_config_update` |
| `agents/<name>.yaml` | `agent_config_update` |
| `skills/` | `skill_create`, `skill_update` |

Additional agent tools:

| Tool | Description |
| ---- | ----------- |
| `filesystem_read` | Read files within sandbox |
| `filesystem_write` | Write files within sandbox |
| `shell_exec` | Execute shell commands within sandbox |
| `memory_search` | Full-text search across MemPalace |
| `memory_store` | Store content in MemPalace |
| `l3_deep_search` | Deep semantic search across memory |
| `cron` | Schedule recurring or one-shot prompts |
| `delegate_task` | Spawn an internal or external sub-agent |

Config-dir files (`~/.config/cobot/`) are never modified by agents — only by CLI commands. `MEMORY.md` may exist in a workspace, but the structured memory tools operate on the SQLite-backed memory store rather than editing that markdown file directly.

## Testing

```bash
# Run all tests
go test ./...

# With coverage
go test -cover ./...

# Specific package
go test ./internal/memory/...
```

## Contributing

1. Fork the repository
2. Create a feature branch: `git checkout -b feature/amazing-feature`
3. Write tests for new behavior
4. Ensure all tests pass: `go test ./...`
5. Commit: `git commit -m 'feat: add amazing feature'`
6. Push: `git push origin feature/amazing-feature`
7. Open a Pull Request

## License

BSD 3-Clause License — see [LICENSE](LICENSE) for details.

## Acknowledgments

- [modernc.org/sqlite](https://gitlab.com/cznic/sqlite) — pure-Go SQLite driver for MemPalace (WAL mode + FTS5)

## References

- [OpenCode Documentation](https://opencode.ai/docs/zh-cn/) — architecture and agent workflow reference
- [Hermes Documentation](https://hermes-agent.nousresearch.com/docs/) — agent runtime and orchestration reference
- [MemPalace](https://github.com/mempalace/mempalace) — memory architecture reference
- [NanoBot](https://github.com/HKUDS/nanobot) — agent system reference
- [OpenClaw](https://openclaw.ai/) — agent platform reference
