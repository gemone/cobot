package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"github.com/cobot-agent/cobot/internal/config"
	"github.com/cobot-agent/cobot/internal/sandbox"
	cobot "github.com/cobot-agent/cobot/pkg"
)

type WorkspaceType string

const (
	WorkspaceTypeDefault WorkspaceType = "default"
	WorkspaceTypeProject WorkspaceType = "project"
	WorkspaceTypeCustom  WorkspaceType = "custom"
)

type WorkspaceDefinition struct {
	Name string        `yaml:"name"`
	Type WorkspaceType `yaml:"type"`
	Path string        `yaml:"path,omitempty"`
	Root string        `yaml:"root,omitempty"`
}

func (d *WorkspaceDefinition) ResolvePath(dataDir string) string {
	if d.Path != "" {
		return d.Path
	}
	return filepath.Join(dataDir, "workspace", d.Name)
}

type WorkspaceConfig struct {
	ID             string                      `yaml:"id"`
	Name           string                      `yaml:"name"`
	Type           WorkspaceType               `yaml:"type"`
	Root           string                      `yaml:"root,omitempty"`
	CreatedAt      time.Time                   `yaml:"created_at"`
	UpdatedAt      time.Time                   `yaml:"updated_at"`
	EnabledMCP     []string                    `yaml:"enabled_mcp,omitempty"`
	EnabledSkills  []string                    `yaml:"enabled_skills,omitempty"`
	Sandbox        sandbox.SandboxConfig       `yaml:"sandbox,omitempty"`
	DefaultAgent   string                      `yaml:"default_agent,omitempty"`
	ExternalAgents []cobot.ExternalAgentConfig `yaml:"external_agents,omitempty"`
}

type Workspace struct {
	Definition *WorkspaceDefinition
	Config     *WorkspaceConfig
	DataDir    string
}

func (w *Workspace) IsDefault() bool {
	return w.Definition.Type == WorkspaceTypeDefault
}

func (w *Workspace) IsProject() bool {
	return w.Definition.Type == WorkspaceTypeProject
}

func (w *Workspace) GetSoulPath() string {
	return filepath.Join(w.DataDir, "SOUL.md")
}

func (w *Workspace) GetUserPath() string {
	return filepath.Join(w.DataDir, "USER.md")
}

func (w *Workspace) GetMemoryMdPath() string {
	return filepath.Join(w.DataDir, "MEMORY.md")
}

// MemoryDir returns the directory containing the long-term memory database.
func (w *Workspace) MemoryDir() string {
	return filepath.Join(w.DataDir, "memory")
}

// GetMemoryDBPath returns the path to the long-term memory SQLite database.
func (w *Workspace) GetMemoryDBPath() string {
	return filepath.Join(w.MemoryDir(), "memory.db")
}

func (w *Workspace) SessionsDir() string {
	return filepath.Join(w.DataDir, "sessions")
}

func (w *Workspace) SkillsDir() string {
	return filepath.Join(w.DataDir, "skills")
}
func (w *Workspace) AgentsDir() string {
	return filepath.Join(w.DataDir, "agents")
}

func (w *Workspace) SpaceDir() string {
	return filepath.Join(w.DataDir, "space")
}

func (w *Workspace) MCPDir() string {
	return filepath.Join(w.DataDir, "mcp")
}

func (w *Workspace) CronDir() string {
	return filepath.Join(w.DataDir, "cron")
}

func (w *Workspace) CronRunsDir() string {
	return filepath.Join(w.CronDir(), "result")
}

func (w *Workspace) BrokerDBPath() string {
	return filepath.Join(w.DataDir, "coord.db")
}

func (w *Workspace) ConfigPath() string {
	return filepath.Join(w.DataDir, "workspace.yaml")
}

func (w *Workspace) ExternalAgent(name string) (*cobot.ExternalAgentConfig, bool) {
	for i := range w.Config.ExternalAgents {
		if w.Config.ExternalAgents[i].Name == name {
			return &w.Config.ExternalAgents[i], true
		}
	}
	return nil, false
}

// EffectiveSandbox returns the final SandboxConfig by merging workspace config
// with optional agent-level overrides.
// When no explicit sandbox root is configured, it falls back to the workspace
// definition root or the workspace config root — matching the logic used by
// resolveSandboxRoot for the shell tool — so that filesystem tools correctly
// resolve relative paths inside the workspace directory.
func (w *Workspace) EffectiveSandbox(agentSandbox *sandbox.SandboxConfig) *sandbox.SandboxConfig {
	cfg := w.Config.Sandbox
	if agentSandbox != nil {
		if agentSandbox.Root != "" {
			cfg.Root = agentSandbox.Root
		}
		if len(agentSandbox.AllowPaths) > 0 {
			cfg.AllowPaths = agentSandbox.AllowPaths
		}
		if len(agentSandbox.BlockedCommands) > 0 {
			cfg.BlockedCommands = agentSandbox.BlockedCommands
		}
	}

	// Fall back to workspace root when no explicit sandbox root is set,
	// keeping filesystem tools consistent with the shell tool's behavior.
	if cfg.Root == "" {
		if w.Config.Root != "" {
			cfg.Root = w.Config.Root
		} else if w.Definition.Root != "" {
			cfg.Root = w.Definition.Root
		}
	}

	var virtualRoot string
	if cfg.Root != "" {
		virtualRoot = sandbox.VirtualHome(w.Config.Name)
	}

	return &sandbox.SandboxConfig{
		VirtualRoot:     virtualRoot,
		Root:            cfg.Root,
		AllowPaths:      cfg.AllowPaths,
		ReadonlyPaths:   cfg.ReadonlyPaths,
		BlockedCommands: cfg.BlockedCommands,
	}
}

func (w *Workspace) EnsureDirs() error {
	dirs := []string{
		w.DataDir,
		w.SessionsDir(),
		w.SkillsDir(),
		w.AgentsDir(),
		w.SpaceDir(),
		w.MCPDir(),
		w.CronDir(),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("create directory %s: %w", dir, err)
		}
	}
	return nil
}

func (w *Workspace) SaveConfig() error {
	w.Config.UpdatedAt = time.Now()
	if err := os.MkdirAll(filepath.Dir(w.ConfigPath()), 0755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	return config.SaveYAML(w.ConfigPath(), w.Config)
}

func (w *Workspace) ValidatePath(path string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}
	absPath = sandbox.EvalSymlinks(absPath)

	dataDir, err := filepath.Abs(w.DataDir)
	if err != nil {
		return fmt.Errorf("resolve data dir: %w", err)
	}
	dataDir = sandbox.EvalSymlinks(dataDir)
	if sandbox.IsSubpath(absPath, dataDir) {
		return nil
	}

	if w.Definition.Root != "" {
		rootDir, err := filepath.Abs(w.Definition.Root)
		if err != nil {
			return fmt.Errorf("resolve root dir: %w", err)
		}
		rootDir = sandbox.EvalSymlinks(rootDir)
		if sandbox.IsSubpath(absPath, rootDir) {
			return nil
		}
	}

	return fmt.Errorf("path %s is outside of workspace boundaries", path)
}

func saveDefinition(d *WorkspaceDefinition, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create definition dir: %w", err)
	}
	return config.SaveYAML(path, d)
}

func loadDefinition(path string) (*WorkspaceDefinition, error) {
	var d WorkspaceDefinition
	if err := config.LoadYAML(path, &d); err != nil {
		return nil, fmt.Errorf("definition: %w", err)
	}
	return &d, nil
}

func loadWorkspaceConfig(path string) (*WorkspaceConfig, error) {
	var cfg WorkspaceConfig
	if err := config.LoadYAML(path, &cfg); err != nil {
		return nil, fmt.Errorf("workspace config: %w", err)
	}
	return &cfg, nil
}

func newWorkspaceConfig(name string, wsType WorkspaceType, root string) *WorkspaceConfig {
	now := time.Now()
	return &WorkspaceConfig{
		ID:        uuid.NewString(),
		Name:      name,
		Type:      wsType,
		Root:      root,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func newWorkspaceFromDefinition(def *WorkspaceDefinition, dataDir string) *Workspace {
	resolvedDataDir := def.ResolvePath(dataDir)
	cfgPath := filepath.Join(resolvedDataDir, "workspace.yaml")

	cfg, err := loadWorkspaceConfig(cfgPath)
	if err != nil {
		cfg = newWorkspaceConfig(def.Name, def.Type, def.Root)
	}

	return &Workspace{
		Definition: def,
		Config:     cfg,
		DataDir:    resolvedDataDir,
	}
}
