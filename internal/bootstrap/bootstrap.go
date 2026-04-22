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
	"time"

	"github.com/cobot-agent/cobot/internal/agent"
	"github.com/cobot-agent/cobot/internal/channel"
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
	Agent      *agent.Agent
	Workspace  *workspace.Workspace
	ChannelMgr *channel.Manager
	Cleanup    func()
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

	channelMgr := channel.NewManager()
	a.SetChannelManager(channelMgr)

	// Start channel health check (30 second interval).
	hcCtx, hcCancel := context.WithCancel(context.Background())
	channelMgr.StartHealthCheck(hcCtx, 30*time.Second)

	sm := a.SessionMgr()

	if agentCfg != nil {
		sm.SetSTMPromoteInterval(agentCfg.MemoryPromoteInterval)
	}

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

	// Create LLM registry for multi-provider model switching.
	registry := llm.NewRegistry(cfg)
	a.SetRegistry(registry)

	// SetModel resolves the "provider:model" spec and initializes the provider.
	if err := a.SetModel(cfg.Model); err != nil {
		if requireProvider {
			hcCancel()
			channelMgr.StopHealthCheck()
			a.Close()
			return nil, err
		}
		slog.Warn("provider init failed", "err", err)
	}

	if err := ConfigureAgentForWorkspace(a, ws, registry); err != nil {
		hcCancel()
		channelMgr.StopHealthCheck()
		a.Close()
		return nil, err
	}

	// a.Close() already closes the memory store; no need for separate cleanup.
	cleanup := func() {
		hcCancel()
		channelMgr.StopHealthCheck()
		a.Close()
	}
	return &Result{Agent: a, Workspace: ws, ChannelMgr: channelMgr, Cleanup: cleanup}, nil
}

// ConfigureAgentForWorkspace (re)configures an existing agent for a workspace:
// memory store, sandbox-scoped tools, workspace tools, and delegate tool.
// It is called once during InitAgent and again when the TUI switches workspaces.
func ConfigureAgentForWorkspace(a *agent.Agent, ws *workspace.Workspace, registry cobot.ModelResolver) error {
	agentCfg, _ := resolveAgentConfig(ws)
	sm := a.SessionMgr()

	configureSystemPrompt(agentCfg, sm, ws)
	configureSkills(agentCfg, sm, ws)
	store := configureMemory(sm, ws, a.Provider(), a.Model())
	if store != nil && agentCfg != nil && agentCfg.MemoryPromoteThreshold > 0 {
		store.SetSTMPromoteThreshold(agentCfg.MemoryPromoteThreshold)
	}
	sandbox := configureSandboxTools(a, ws, agentCfg, sm)
	configureMemoryTools(a, store, sandbox)
	configureDelegateTool(a, ws, registry, sandbox)
	configureCronTool(a, ws, registry)
	configureSkillSyncer(a, store, ws, agentCfg)

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
	loadedSkills, err := skills.LoadCatalog(context.Background(), skillDirs, enabledSkills)
	if err != nil {
		slog.Warn("failed to load skills, leaving existing skills section", "error", err)
		return
	}
	skillSection := skills.SkillsToPrompt(loadedSkills)
	currentPrompt := sm.GetSystemPrompt()
	_ = sm.SetSystemPrompt(replaceSkillsSection(currentPrompt, skillSection))
}

// skillsRefresher implements cobot.SkillsPromptRefresher by reloading the
// skills catalog and replacing the skills section of the system prompt.
type skillsRefresher struct {
	sm     *agent.SessionManager
	ws     *workspace.Workspace
	filter []string
}

func (r *skillsRefresher) RefreshSkillsPrompt(ctx context.Context) error {
	skillDirs := []string{workspace.GlobalSkillsDir(), r.ws.SkillsDir()}
	catalog, err := skills.LoadCatalog(ctx, skillDirs, r.filter)
	if err != nil {
		return err
	}
	skillSection := skills.SkillsToPrompt(catalog)
	currentPrompt := r.sm.GetSystemPrompt()
	newPrompt := replaceSkillsSection(currentPrompt, skillSection)
	return r.sm.SetSystemPrompt(newPrompt)
}

// replaceSkillsSection replaces the existing skills section in the system
// prompt, or appends a new one if none exists. It locates the section by
// looking for the "## Skills (mandatory)" header marker.
func replaceSkillsSection(current, newSection string) string {
	marker := skills.SkillsSectionMarker
	// Search for the marker as a line start: either at position 0 or preceded by \n
	idx := 0
	for {
		i := strings.Index(current[idx:], marker)
		if i < 0 {
			break
		}
		pos := idx + i
		if (pos == 0 || current[pos-1] == '\n' || current[pos-1] == '\r') && !insideCodeBlock(current[:pos]) {
			idx = pos
			goto found
		}
		idx = pos + 1
	}
	// Marker not found — append
	return current + "\n\n" + newSection
found:
	end := idx + len(marker)
	if end < len(current) && current[end] == '\n' {
		end++
	}
	// Find next ## heading or end of string, respecting code blocks.
	for i := end; i < len(current); i++ {
		if i+4 < len(current) && current[i] == '\n' && current[i+1] == '#' && current[i+2] == '#' && current[i+3] == ' ' {
			if !insideCodeBlock(current[:i]) {
				return current[:idx] + newSection + current[i:]
			}
		}
	}
	return current[:idx] + newSection
}

// insideCodeBlock returns true if the text ends inside a fenced code block.
// Only counts ``` at the start of a line (after optional whitespace).
func insideCodeBlock(text string) bool {
	inBlock := false
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inBlock = !inBlock
		}
	}
	return inBlock
}

func configureMemory(sm *agent.SessionManager, ws *workspace.Workspace, provider cobot.Provider, model string) *memory.Store {
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
	if provider != nil && model != "" {
		summarizer := memory.NewSummarizer(provider, model)
		store.SetSummarizer(summarizer)
	}
	sm.SetMemoryStore(store)
	sm.SetMemoryRecall(store)
	return store
}

func configureSandboxTools(a *agent.Agent, ws *workspace.Workspace, agentCfg *config.AgentConfig, sm *agent.SessionManager) *sandbox.SandboxConfig {
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
	var enabledSkills []string
	if agentCfg != nil {
		enabledSkills = agentCfg.EnabledSkills
	}
	refresher := &skillsRefresher{sm: sm, ws: ws, filter: enabledSkills}
	tools.RegisterSkillsTools(a.ToolRegistry(), ws, refresher)
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

func configureCronTool(a *agent.Agent, ws *workspace.Workspace, registry cobot.ModelResolver) {
	// Stop previous scheduler if switching workspaces.
	if a.CronScheduler() != nil {
		a.CronScheduler().Stop()
	}

	channelMgr := a.ChannelManager()
	scheduler, brokerDB, err := cron.Setup(a.Context(), cron.SetupConfig{
		BrokerDBPath: ws.BrokerDBPath(),
		CronDir:      ws.CronDir(),
		RunsDir:      ws.CronRunsDir(),
		Notifier:     channelMgr,
		NewAgent: func() *agent.Agent {
			filtered := a.ToolRegistry().Clone().Without("cron", "delegate_task")
			sub := newSubAgent(a, registry, filtered)
			_ = sub.SessionMgr().SetSystemPrompt("You are a scheduled task executor. Complete the task efficiently and output results.")
			return sub
		},
	})
	if err != nil {
		slog.Error("cron setup failed", "error", err, "workspace", ws.SpaceDir())
		return
	}

	a.SetBroker(brokerDB)
	a.SetCronScheduler(scheduler)
	a.RegisterTool(tools.NewCronTool(scheduler,
		tools.WithCronChannelIDFn(func() string {
			ids := channelMgr.AllAliveIDs()
			if len(ids) > 0 {
				return ids[0]
			}
			return ""
		}),
	))
}

func configureSkillSyncer(a *agent.Agent, store *memory.Store, ws *workspace.Workspace, agentCfg *config.AgentConfig) {
	if store == nil || a.Provider() == nil || a.Model() == "" {
		return
	}
	interval := 1 * time.Hour
	if agentCfg != nil && agentCfg.SkillSyncInterval > 0 {
		interval = time.Duration(agentCfg.SkillSyncInterval) * time.Minute
	}
	analyzer := memory.NewWorkflowAnalyzer(store, a.Provider(), a.Model(), ws.SkillsDir())
	syncer := agent.NewBackgroundSkillSyncer(analyzer, interval)
	a.SetSkillSyncer(syncer)
	syncer.Start()
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
		slog.Warn("failed to load agent configs", "error", err, "path", ws.AgentsDir())
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
