// Package bootstrap wires together the agent runtime: workspace resolution,
// provider registry, tool registration, memory store, and sandbox configuration.
// It is the single composition root that cmd/ packages call instead of doing
// ad-hoc assembly themselves.
package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/cobot-agent/cobot/internal/agent"
	"github.com/cobot-agent/cobot/internal/config"
	"github.com/cobot-agent/cobot/internal/llm"
	"github.com/cobot-agent/cobot/internal/memory"
	"github.com/cobot-agent/cobot/internal/skills"
	"github.com/cobot-agent/cobot/internal/tools"
	"github.com/cobot-agent/cobot/internal/workspace"
	cobot "github.com/cobot-agent/cobot/pkg"
)

// Result bundles everything InitAgent produces so callers don't juggle
// multiple return values.
type Result struct {
	Agent     *agent.Agent
	Workspace *workspace.Workspace
	Cleanup   func()
}

// InitAgent creates a fully-wired Agent for the given Config. When
// requireProvider is true an error is returned if the LLM provider cannot
// be initialised (CLI chat mode); when false a warning is printed instead
// (TUI mode where the user can switch models later).
func InitAgent(cfg *cobot.Config, requireProvider bool) (*Result, error) {
	wsMgr, err := workspace.NewManager()
	if err != nil {
		return nil, fmt.Errorf("create workspace manager: %w", err)
	}

	ws, err := wsMgr.ResolveByNameOrDiscover(cfg.Workspace, ".")
	if err != nil {
		return nil, fmt.Errorf("resolve workspace: %w", err)
	}
	if err := ws.EnsureDirs(); err != nil {
		return nil, fmt.Errorf("ensure workspace dirs: %w", err)
	}

	agentCfg, _ := resolveAgentConfig(ws)
	if agentCfg != nil && agentCfg.Model != "" {
		cfg.Model = agentCfg.Model
	}
	if agentCfg != nil && agentCfg.MaxTurns > 0 {
		cfg.MaxTurns = agentCfg.MaxTurns
	}

	// Create tool registry externally and inject it into the agent.
	toolReg := tools.NewRegistry()
	a := agent.New(cfg, toolReg)

	if agentCfg != nil && agentCfg.Session != nil {
		sc := cfg.Session
		if agentCfg.Session.SummarizeThreshold > 0 {
			sc.SummarizeThreshold = agentCfg.Session.SummarizeThreshold
		}
		if agentCfg.Session.CompressThreshold > 0 {
			sc.CompressThreshold = agentCfg.Session.CompressThreshold
		}
		if agentCfg.Session.SummarizeTurns > 0 {
			sc.SummarizeTurns = agentCfg.Session.SummarizeTurns
		}
		if agentCfg.Session.SummaryModel != "" {
			sc.SummaryModel = agentCfg.Session.SummaryModel
		}
		a.SetSessionConfig(sc)
	}

	sessionStore := agent.NewSessionStore(ws.SessionsDir())
	a.SetSessionStore(sessionStore)

	if agentCfg != nil && agentCfg.SystemPrompt != "" {
		prompt := resolveSystemPrompt(agentCfg.SystemPrompt, ws)
		_ = a.SetSystemPrompt(prompt)
	}

	// Create LLM registry for multi-provider model switching.
	registry := llm.NewRegistry(cfg)
	a.SetRegistry(registry)

	// SetModel resolves the "provider:model" spec and initializes the provider.
	if err := a.SetModel(cfg.Model); err != nil {
		if requireProvider {
			return nil, err
		}
		slog.Warn("provider init failed", "err", err)
	}

	if err := ConfigureAgentForWorkspace(a, ws, registry); err != nil {
		return nil, err
	}

	// a.Close() already closes the memory store; no need for separate cleanup.
	cleanup := func() { a.Close() }
	return &Result{Agent: a, Workspace: ws, Cleanup: cleanup}, nil
}

// ConfigureAgentForWorkspace (re)configures an existing agent for a workspace:
// memory store, sandbox-scoped tools, workspace tools, and delegate tool.
// It is called once during InitAgent and again when the TUI switches workspaces.
func ConfigureAgentForWorkspace(a *agent.Agent, ws *workspace.Workspace, registry cobot.ModelResolver) error {
	agentCfg, _ := resolveAgentConfig(ws)

	if agentCfg != nil && agentCfg.SystemPrompt != "" {
		prompt := resolveSystemPrompt(agentCfg.SystemPrompt, ws)
		_ = a.SetSystemPrompt(prompt)
	}

	// --- skills ---
	var enabledSkills []string
	if agentCfg != nil {
		enabledSkills = agentCfg.EnabledSkills
	}
	skillDirs := []string{workspace.GlobalSkillsDir(), ws.SkillsDir()}
	loadedSkills, err := skills.LoadSkills(context.Background(), skillDirs, enabledSkills)
	if err != nil {
		slog.Warn("failed to load skills", "error", err)
	}
	if len(loadedSkills) > 0 {
		skillSection := skills.SkillsToPrompt(loadedSkills)
		currentPrompt := a.GetSystemPrompt()
		if currentPrompt == "" {
			_ = a.SetSystemPrompt(skillSection)
		} else {
			_ = a.SetSystemPrompt(currentPrompt + "\n\n" + skillSection)
		}
	}

	// --- memory ---
	if old := a.MemoryStore(); old != nil {
		if err := old.Close(); err != nil {
			slog.Warn("failed to close memory store", "err", err)
		}
	}
	dataDir := ws.MemoryDir()
	store, err := memory.OpenStore(dataDir, ws.STMDir())
	if err != nil {
		slog.Warn("failed to open memory store", "err", err)
	} else {
		a.SetMemoryStore(store)
		a.SetMemoryRecall(store)
	}

	// --- sandbox ---
	sandboxRoot := resolveSandboxRoot(ws)
	sandboxCfg := ws.Config.Sandbox
	if agentCfg != nil && agentCfg.Sandbox != nil {
		if agentCfg.Sandbox.Root != "" {
			sandboxCfg.Root = agentCfg.Sandbox.Root
			sandboxRoot = agentCfg.Sandbox.Root
		}
		if len(agentCfg.Sandbox.AllowPaths) > 0 {
			sandboxCfg.AllowPaths = agentCfg.Sandbox.AllowPaths
		}
		if len(agentCfg.Sandbox.BlockedCommands) > 0 {
			sandboxCfg.BlockedCommands = agentCfg.Sandbox.BlockedCommands
		}
	}
	var virtualRoot string
	if sandboxCfg.Root != "" {
		virtualRoot = "/home/" + ws.Config.Name
	}
	sandbox := &cobot.SandboxConfig{
		VirtualRoot:   virtualRoot,
		Root:          sandboxCfg.Root,
		AllowPaths:    sandboxCfg.AllowPaths,
		ReadonlyPaths: sandboxCfg.ReadonlyPaths,
	}
	a.RegisterTool(tools.NewReadFileTool(tools.WithReadSandbox(sandbox)))
	a.RegisterTool(tools.NewWriteFileTool(tools.WithWriteSandbox(sandbox)))
	a.RegisterTool(tools.NewListDirTool(tools.WithListSandbox(sandbox)))
	a.RegisterTool(tools.NewSearchFilesTool(tools.WithSearchSandbox(sandbox)))

	// Shell tool gets the full sandbox config so it can rewrite virtual paths.
	shellSandbox := &cobot.SandboxConfig{
		VirtualRoot:     virtualRoot,
		Root:            sandboxCfg.Root,
		AllowPaths:      sandboxCfg.AllowPaths,
		ReadonlyPaths:   sandboxCfg.ReadonlyPaths,
		AllowNetwork:    sandboxCfg.AllowNetwork,
		BlockedCommands: sandboxCfg.BlockedCommands,
	}
	a.RegisterTool(tools.NewShellExecTool(
		tools.WithShellWorkdir(sandboxRoot),
		tools.WithShellSandboxConfig(shellSandbox),
	))

	// --- workspace tools ---
	tools.RegisterWorkspaceTools(a.ToolRegistry(), ws, sandbox)

	// Re-register memory tools with sandbox config for path sanitization in errors.
	if store != nil {
		a.RegisterTool(memory.NewMemorySearchTool(store, memory.WithMemorySearchSandbox(sandbox)))
		a.RegisterTool(memory.NewMemoryStoreTool(store, memory.WithMemoryStoreSandbox(sandbox)))
		a.RegisterTool(memory.NewL3DeepSearchTool(store, memory.WithL3DeepSearchSandbox(sandbox)))
	}

	// --- delegate tool ---
	a.RegisterTool(tools.NewDelegateTool(func() cobot.SubAgent {
		cfg := *a.Config() // value copy to avoid mutating parent's config
		filtered := a.ToolRegistry().Clone().Without("delegate_task", "memory_store", "memory_search", "l3_deep_search")
		sub := agent.New(&cfg, filtered)
		sub.SetRegistry(registry)
		if err := sub.SetModel(a.Model()); err != nil {
			sub.SetProvider(a.Provider())
		}
		return sub
	}, tools.WithDelegateWorkdir(ws.SpaceDir()), tools.WithDelegateAgentLookup(ws), tools.WithDelegateSandbox(sandbox)))

	return nil
}

// --- private helpers (moved from cmd/cobot/helpers.go) ---

func resolveSandboxRoot(ws *workspace.Workspace) string {
	if ws.Config.Sandbox.Root != "" {
		return ws.Config.Sandbox.Root
	}
	if ws.Definition.Root != "" {
		return ws.Definition.Root
	}
	return ws.SpaceDir()
}

func resolveSystemPrompt(value string, ws *workspace.Workspace) string {
	if strings.HasSuffix(value, ".md") {
		path := filepath.Join(ws.DataDir, value)
		if data, err := os.ReadFile(path); err == nil {
			return string(data)
		}
	}
	return value
}

func resolveAgentConfig(ws *workspace.Workspace) (*config.AgentConfig, error) {
	configs, err := config.LoadAgentConfigs(ws.AgentsDir())
	if err != nil {
		return nil, nil
	}

	name := ws.Config.DefaultAgent
	if name == "" {
		name = "main"
	}

	if cfg, ok := configs[name]; ok {
		return cfg, nil
	}
	return nil, nil
}
