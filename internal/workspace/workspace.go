package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
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
	Name    string                 `yaml:"name"`
	Type    WorkspaceType          `yaml:"type"`
	Path    string                 `yaml:"path,omitempty"`
	Root    string                 `yaml:"root,omitempty"`
	Sandbox *sandbox.SandboxConfig `yaml:"sandbox,omitempty"`
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
	mu         sync.Mutex // Protects concurrent modification of Config
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

// EffectiveSandbox returns the final SandboxConfig by merging definition config,
// workspace config, and optional agent-level overrides.
// Definition-layer network policy is reapplied last so workspace state cannot
// weaken it.
// When no explicit sandbox root is configured, it defaults to the workspace
// space directory — this ensures sandbox is always active, restricting writes
// to workspace/<name>/space for both filesystem and shell_exec tools.
func (w *Workspace) EffectiveSandbox(agentSandbox *sandbox.SandboxConfig) (*sandbox.SandboxConfig, error) {
	merged := sandbox.MergeConfigs(w.Definition.Sandbox, &w.Config.Sandbox)
	merged = sandbox.MergeConfigs(&merged, agentSandbox)

	// Fall back to workspace root when no explicit sandbox root is set.
	if merged.Root == "" {
		if w.Config.Root != "" {
			merged.Root = w.Config.Root
		} else if w.Definition.Root != "" {
			merged.Root = w.Definition.Root
		}
	}

	// Default: sandbox restricts writes to the workspace space directory.
	if merged.Root == "" {
		merged.Root = w.SpaceDir()
	}

	if merged.VirtualRoot == "" {
		merged.VirtualRoot = sandbox.VirtualHome(w.Config.Name)
	}

	// Allow writes to the system temp directory by default.
	// Resolved via EvalSymlinks because macOS /tmp → /private/tmp.
	// Only added when no explicit AllowPaths are configured, so user overrides
	// take precedence.
	if len(merged.AllowPaths) == 0 {
		if tmpDir := sandbox.EvalSymlinks(os.TempDir()); tmpDir != "" {
			merged.AllowPaths = []string{tmpDir}
		}
	}

	if err := w.applyDefinitionSandboxNetworkPolicy(&merged); err != nil {
		return nil, err
	}

	result := merged.Clone()
	return &result, nil
}

func (w *Workspace) applyDefinitionSandboxNetworkPolicy(merged *sandbox.SandboxConfig) error {
	if w == nil || w.Definition == nil || w.Definition.Sandbox == nil || merged == nil {
		return nil
	}

	definitionSandbox := w.Definition.Sandbox
	if definitionSandbox.HasAllowNetworkOverride() {
		merged.SetAllowNetwork(definitionSandbox.AllowNetwork)
	}

	catalogBounded := definitionSandbox.HasValidNetworkToolsOverride() || len(definitionSandbox.ValidNetworkTools) > 0
	if catalogBounded {
		merged.SetValidNetworkTools(definitionSandbox.ValidNetworkTools)
	}

	if definitionSandbox.HasAllowedNetworkToolsOverride() || len(definitionSandbox.AllowedNetworkTools) > 0 {
		if err := merged.SetAllowedNetworkTools(definitionSandbox.AllowedNetworkTools); err != nil {
			return fmt.Errorf("workspace definition sandbox.allowed_network_tools: %w", err)
		}
		return nil
	}

	if !catalogBounded || len(merged.AllowedNetworkTools) == 0 {
		return nil
	}
	if len(merged.ValidNetworkTools) == 0 {
		return fmt.Errorf("effective sandbox.allowed_network_tools must be empty because workspace definition sandbox.valid_network_tools is empty")
	}
	if err := merged.ValidateAllowedNetworkTools(); err != nil {
		return fmt.Errorf("effective sandbox.allowed_network_tools exceeds workspace definition sandbox.valid_network_tools: %w", err)
	}
	return nil
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
	w.mu.Lock()
	defer w.mu.Unlock()

	w.Config.UpdatedAt = time.Now()
	if err := os.MkdirAll(filepath.Dir(w.ConfigPath()), 0755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	return config.SaveYAML(w.ConfigPath(), w.Config)
}

// ModifyConfig applies a mutation to the config under mutex lock and saves it atomically.
func (w *Workspace) ModifyConfig(fn func(*WorkspaceConfig)) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	fn(w.Config)
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

func definitionValidNetworkTools(def *WorkspaceDefinition) []string {
	if def == nil || def.Sandbox == nil {
		return nil
	}
	return def.Sandbox.ValidNetworkTools
}

func validateAllowedNetworkTools(tools, validTools []string) ([]string, error) {
	if len(validTools) == 0 {
		return nil, fmt.Errorf("workspace definition sandbox.valid_network_tools must be configured before setting sandbox.allowed_network_tools")
	}
	return sandbox.ValidateNetworkTools(tools, validTools)
}

func validateWorkspaceSandbox(def *WorkspaceDefinition, cfg *WorkspaceConfig) error {
	if cfg == nil {
		return nil
	}
	if cfg.Sandbox.HasValidNetworkToolsOverride() || len(cfg.Sandbox.ValidNetworkTools) > 0 {
		return fmt.Errorf("workspace config cannot set sandbox.valid_network_tools; define it in the workspace definition")
	}
	if cfg.Sandbox.HasAllowedNetworkToolsOverride() || len(cfg.Sandbox.AllowedNetworkTools) > 0 {
		normalized, err := validateAllowedNetworkTools(cfg.Sandbox.AllowedNetworkTools, definitionValidNetworkTools(def))
		if err != nil {
			return err
		}
		if err := cfg.Sandbox.SetAllowedNetworkTools(normalized); err != nil {
			return err
		}
	}
	return nil
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

func (w *Workspace) ValidateAllowedNetworkTools(tools []string) ([]string, error) {
	if w == nil || w.Definition == nil {
		return nil, fmt.Errorf("workspace definition is not loaded")
	}
	return validateAllowedNetworkTools(tools, definitionValidNetworkTools(w.Definition))
}

func newWorkspaceFromDefinition(def *WorkspaceDefinition, dataDir string) (*Workspace, error) {
	resolvedDataDir := def.ResolvePath(dataDir)
	cfgPath := filepath.Join(resolvedDataDir, "workspace.yaml")

	cfg, err := loadWorkspaceConfig(cfgPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			cfg = newWorkspaceConfig(def.Name, def.Type, def.Root)
		} else {
			return nil, err
		}
	}
	if err := validateWorkspaceSandbox(def, cfg); err != nil {
		return nil, fmt.Errorf("workspace config: %w", err)
	}

	return &Workspace{
		Definition: def,
		Config:     cfg,
		DataDir:    resolvedDataDir,
	}, nil
}
