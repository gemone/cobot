package workspace

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"github.com/cobot-agent/cobot/internal/config"
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
	return filepath.Join(dataDir, d.Name)
}

type WorkspaceConfig struct {
	ID             string                     `yaml:"id"`
	Name           string                     `yaml:"name"`
	Type           WorkspaceType              `yaml:"type"`
	Root           string                     `yaml:"root,omitempty"`
	CreatedAt      time.Time                  `yaml:"created_at"`
	UpdatedAt      time.Time                  `yaml:"updated_at"`
	EnabledMCP     []string                   `yaml:"enabled_mcp,omitempty"`
	EnabledSkills  []string                   `yaml:"enabled_skills,omitempty"`
	Sandbox        cobot.SandboxConfig        `yaml:"sandbox,omitempty"`
	DefaultAgent   string                     `yaml:"default_agent,omitempty"`
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

func (w *Workspace) MemoryDir() string {
	return filepath.Join(w.DataDir, "memory")
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

func (w *Workspace) EnsureDirs() error {
	dirs := []string{
		w.DataDir,
		w.MemoryDir(),
		w.SessionsDir(),
		w.SkillsDir(),
		w.AgentsDir(),
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
	absPath = cobot.EvalSymlinks(absPath)

	dataDir, err := filepath.Abs(w.DataDir)
	if err != nil {
		return fmt.Errorf("resolve data dir: %w", err)
	}
	dataDir = cobot.EvalSymlinks(dataDir)
	if cobot.IsSubpath(absPath, dataDir) {
		return nil
	}

	if w.Definition.Root != "" {
		rootDir, err := filepath.Abs(w.Definition.Root)
		if err != nil {
			return fmt.Errorf("resolve root dir: %w", err)
		}
		rootDir = cobot.EvalSymlinks(rootDir)
		if cobot.IsSubpath(absPath, rootDir) {
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
		ID:        uuid.New().String(),
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

// --- Manager ---

type Manager struct {
	definitionsDir string
	dataDir        string
}

func NewManager() (*Manager, error) {
	m := &Manager{
		definitionsDir: WorkspaceDefinitionsDir(),
		dataDir:        DataDir(),
	}

	if err := os.MkdirAll(m.definitionsDir, 0755); err != nil {
		return nil, fmt.Errorf("create definitions dir: %w", err)
	}
	if err := os.MkdirAll(m.dataDir, 0755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	if err := m.ensureDefault(); err != nil {
		return nil, fmt.Errorf("ensure default workspace: %w", err)
	}

	return m, nil
}

func (m *Manager) ensureDefault() error {
	defPath := filepath.Join(m.definitionsDir, "default.yaml")
	if _, err := os.Stat(defPath); os.IsNotExist(err) {
		def := &WorkspaceDefinition{
			Name: "default",
			Type: WorkspaceTypeDefault,
		}
		if err := saveDefinition(def, defPath); err != nil {
			return err
		}
	}

	ws, err := m.Resolve("default")
	if err != nil {
		return err
	}
	if err := ws.EnsureDirs(); err != nil {
		return err
	}
	if _, err := os.Stat(ws.ConfigPath()); os.IsNotExist(err) {
		if err := ws.SaveConfig(); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) Resolve(name string) (*Workspace, error) {
	defPath := filepath.Join(m.definitionsDir, name+".yaml")
	def, err := loadDefinition(defPath)
	if err != nil {
		return nil, fmt.Errorf("workspace not found: %s", name)
	}
	ws := newWorkspaceFromDefinition(def, m.dataDir)
	return ws, nil
}

func (m *Manager) Create(name string, wsType WorkspaceType, root string, customPath string) (*Workspace, error) {
	if name == "" {
		return nil, fmt.Errorf("workspace name cannot be empty")
	}
	if name == "default" && wsType != WorkspaceTypeDefault {
		return nil, fmt.Errorf("name 'default' is reserved")
	}

	defPath := filepath.Join(m.definitionsDir, name+".yaml")
	if _, err := os.Stat(defPath); err == nil {
		return nil, fmt.Errorf("workspace '%s' already exists", name)
	}

	def := &WorkspaceDefinition{
		Name: name,
		Type: wsType,
		Path: customPath,
		Root: root,
	}

	if err := saveDefinition(def, defPath); err != nil {
		return nil, err
	}

	ws := newWorkspaceFromDefinition(def, m.dataDir)
	ws.Config = newWorkspaceConfig(name, wsType, root)

	if err := ws.EnsureDirs(); err != nil {
		return nil, err
	}
	if err := ws.SaveConfig(); err != nil {
		return nil, err
	}

	return ws, nil
}

func (m *Manager) List() ([]*WorkspaceDefinition, error) {
	var defs []*WorkspaceDefinition

	entries, err := os.ReadDir(m.definitionsDir)
	if err != nil {
		return nil, fmt.Errorf("read definitions dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".yaml" {
			continue
		}
		defPath := filepath.Join(m.definitionsDir, entry.Name())
		def, err := loadDefinition(defPath)
		if err != nil {
			slog.Debug("skipping invalid definition", "file", entry.Name(), "error", err)
			continue
		}
		defs = append(defs, def)
	}

	sort.Slice(defs, func(i, j int) bool {
		if defs[i].Type == WorkspaceTypeDefault {
			return true
		}
		if defs[j].Type == WorkspaceTypeDefault {
			return false
		}
		return defs[i].Name < defs[j].Name
	})

	return defs, nil
}

func (m *Manager) Delete(name string) error {
	if name == "default" {
		return fmt.Errorf("cannot delete default workspace")
	}

	defPath := filepath.Join(m.definitionsDir, name+".yaml")
	def, err := loadDefinition(defPath)
	if err != nil {
		return fmt.Errorf("workspace not found: %s", name)
	}

	if err := os.Remove(defPath); err != nil {
		return fmt.Errorf("remove definition: %w", err)
	}

	dataPath := def.ResolvePath(m.dataDir)
	if err := os.RemoveAll(dataPath); err != nil {
		return fmt.Errorf("remove workspace data: %w", err)
	}

	return nil
}

func (m *Manager) Discover(startDir string) (*Workspace, error) {
	dir := startDir
	for {
		cobotDir := filepath.Join(dir, ".cobot")
		info, err := os.Stat(cobotDir)
		if err == nil && info.IsDir() {
			projectName := filepath.Base(dir)

			workspaceYAMLPath := filepath.Join(dir, ".cobot", "workspace.yaml")
			if data, err := os.ReadFile(workspaceYAMLPath); err == nil {
				var cfg struct {
					Name string `yaml:"name"`
				}
				if err := yaml.Unmarshal(data, &cfg); err == nil && cfg.Name != "" {
					projectName = cfg.Name
				}
			}

			defPath := filepath.Join(m.definitionsDir, projectName+".yaml")
			if def, err := loadDefinition(defPath); err == nil {
				return newWorkspaceFromDefinition(def, m.dataDir), nil
			}

			return m.Create(projectName, WorkspaceTypeProject, dir, "")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, fmt.Errorf("no .cobot directory found from %s", startDir)
		}
		dir = parent
	}
}

func (m *Manager) ResolveByNameOrDiscover(name string, startDir string) (*Workspace, error) {
	if name != "" {
		ws, err := m.Resolve(name)
		if err == nil {
			return ws, nil
		}
	}

	ws, err := m.Discover(startDir)
	if err == nil {
		return ws, nil
	}

	return m.Resolve("default")
}
