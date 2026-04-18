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
	"github.com/cobot-agent/cobot/internal/cron"
	"github.com/cobot-agent/cobot/internal/llm"
	"github.com/cobot-agent/cobot/internal/memory"
	"github.com/cobot-agent/cobot/internal/sandbox"
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

	sm := a.SessionMgr()

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
		sm.SetSessionConfig(sc)
	}

	sessionStore := agent.NewSessionStore(ws.SessionsDir())
	sm.SetSessionStore(sessionStore)

	if agentCfg != nil && agentCfg.SystemPrompt != "" {
		prompt := resolveSystemPrompt(agentCfg.SystemPrompt, ws)
		_ = sm.SetSystemPrompt(prompt)
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
	sm := a.SessionMgr()

	configureSystemPrompt(agentCfg, sm, ws)
	configureSkills(agentCfg, sm, ws)
	store := configureMemory(sm, ws)
	sandbox := configureSandboxTools(a, ws, agentCfg)
	configureMemoryTools(a, store, sandbox)
	configureDelegateTool(a, ws, registry, sandbox)
	configureCronTool(a, ws, store, registry)

	return nil
}

func configureSystemPrompt(agentCfg *config.AgentConfig, sm *agent.SessionManager, ws *workspace.Workspace) {
	if agentCfg != nil && agentCfg.SystemPrompt != "" {
		prompt := resolveSystemPrompt(agentCfg.SystemPrompt, ws)
		_ = sm.SetSystemPrompt(prompt)
	}
}

func configureSkills(agentCfg *config.AgentConfig, sm *agent.SessionManager, ws *workspace.Workspace) {
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
		currentPrompt := sm.GetSystemPrompt()
		if currentPrompt == "" {
			_ = sm.SetSystemPrompt(skillSection)
		} else {
			_ = sm.SetSystemPrompt(currentPrompt + "\n\n" + skillSection)
		}
	}
}

func configureMemory(sm *agent.SessionManager, ws *workspace.Workspace) *memory.Store {
	if old := sm.MemoryStore(); old != nil {
		if err := old.Close(); err != nil {
			slog.Warn("failed to close memory store", "err", err)
		}
	}
	store, err := memory.OpenStore(ws.DataDir, ws.SessionsDir())
	if err != nil {
		slog.Warn("failed to open memory store", "err", err)
		return nil
	}
	sm.SetMemoryStore(store)
	sm.SetMemoryRecall(store)
	return store
}

func configureSandboxTools(a *agent.Agent, ws *workspace.Workspace, agentCfg *config.AgentConfig) *sandbox.SandboxConfig {
	var agentSandbox *sandbox.SandboxConfig
	if agentCfg != nil {
		agentSandbox = agentCfg.Sandbox
	}
	sandbox := ws.EffectiveSandbox(agentSandbox)

	a.RegisterTool(tools.NewReadFileTool(sandbox))
	a.RegisterTool(tools.NewWriteFileTool(sandbox))
	a.RegisterTool(tools.NewListDirTool(sandbox))
	a.RegisterTool(tools.NewSearchFilesTool(sandbox))
	a.RegisterTool(tools.NewGrepFilesTool(sandbox))

	shellSandbox := *sandbox
	if agentCfg != nil && agentCfg.Sandbox != nil {
		shellSandbox.AllowNetwork = agentCfg.Sandbox.AllowNetwork
	}

	sandboxRoot := resolveSandboxRoot(ws)
	if agentSandbox != nil && agentSandbox.Root != "" {
		sandboxRoot = agentSandbox.Root
	}

	a.RegisterTool(tools.NewShellExecTool(
		tools.WithShellWorkdir(sandboxRoot),
		tools.WithShellSandboxConfig(&shellSandbox),
	))

	tools.RegisterWorkspaceTools(a.ToolRegistry(), ws, sandbox)
	return sandbox
}

func configureMemoryTools(a *agent.Agent, store *memory.Store, sandbox *sandbox.SandboxConfig) {
	if store != nil {
		a.RegisterTool(memory.NewMemorySearchTool(store, sandbox))
		a.RegisterTool(memory.NewMemoryStoreTool(store, sandbox))
		a.RegisterTool(memory.NewL3DeepSearchTool(store, sandbox))
	}
}

func configureDelegateTool(a *agent.Agent, ws *workspace.Workspace, registry cobot.ModelResolver, sandbox *sandbox.SandboxConfig) {
	a.RegisterTool(tools.NewDelegateTool(func() cobot.SubAgent {
		filtered := a.ToolRegistry().Clone().Without("delegate_task", "memory_store", "memory_search", "l3_deep_search")
		return newSubAgent(a, registry, filtered)
	}, tools.WithDelegateWorkdir(ws.SpaceDir()), tools.WithDelegateAgentLookup(ws), tools.WithDelegateSandbox(sandbox)))
}

func configureCronTool(a *agent.Agent, ws *workspace.Workspace, store *memory.Store, registry cobot.ModelResolver) {
	cronDir := ws.CronDir()
	cronStore := cron.NewStore(cronDir)
	cronExecutor := cron.NewAgentExecutor(func() cron.AgentRunner {
		filtered := a.ToolRegistry().Clone().Without("cron", "delegate_task")
		sub := newSubAgent(a, registry, filtered)
		_ = sub.SessionMgr().SetSystemPrompt("You are a scheduled task executor. Complete the task efficiently and output results.")
		return &cronAgentRunner{agent: sub}
	})
	if store != nil {
		cronExecutor.WithMemoryStore(func(ctx context.Context, content, wingName, roomName, hallType string) (string, error) {
			return store.StoreByName(ctx, content, wingName, roomName, hallType)
		})
	}
	cronScheduler := cron.NewScheduler(cronStore, cronExecutor)
	a.RegisterTool(tools.NewCronTool(cronScheduler))
	if err := cronScheduler.Start(); err != nil {
		slog.Warn("failed to start cron scheduler", "error", err)
	}
	a.SetCronScheduler(cronScheduler)
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

// newSubAgent creates a configured sub-agent sharing the parent's model and registry.
func newSubAgent(a *agent.Agent, registry cobot.ModelResolver, filteredTools cobot.ToolRegistry) *agent.Agent {
	cfg := *a.Config()
	sub := agent.New(&cfg, filteredTools)
	sub.SetRegistry(registry)
	if err := sub.SetModel(a.Model()); err != nil {
		sub.SetProvider(a.Provider())
	}
	return sub
}

// cronAgentRunner adapts an *agent.Agent (which implements cobot.SubAgent)
// to the cron.AgentRunner interface by calling Prompt and returning the content.
type cronAgentRunner struct {
	agent *agent.Agent
}

func (r *cronAgentRunner) Prompt(ctx context.Context, message string) (string, error) {
	resp, err := r.agent.Prompt(ctx, message)
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}

func (r *cronAgentRunner) SetModel(spec string) error {
	return r.agent.SetModel(spec)
}

func (r *cronAgentRunner) Close() error {
	return r.agent.Close()
}
