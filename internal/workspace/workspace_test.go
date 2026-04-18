package workspace

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cobot-agent/cobot/internal/sandbox"
	cobot "github.com/cobot-agent/cobot/pkg"
)

func TestWorkspaceDefinition_ResolvePath_Default(t *testing.T) {
	def := &WorkspaceDefinition{
		Name: "myproject",
		Type: WorkspaceTypeProject,
	}
	result := def.ResolvePath("/data")
	expected := filepath.Join("/data", "workspace", "myproject")
	if result != expected {
		t.Errorf("ResolvePath() = %s, want %s", result, expected)
	}
}

func TestWorkspaceDefinition_ResolvePath_Custom(t *testing.T) {
	def := &WorkspaceDefinition{
		Name: "myproject",
		Type: WorkspaceTypeProject,
		Path: "/custom/path",
	}
	result := def.ResolvePath("/data")
	if result != "/custom/path" {
		t.Errorf("ResolvePath() = %s, want /custom/path", result)
	}
}

func TestSaveLoadDefinition_RoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	defPath := filepath.Join(tmpDir, "test.yaml")

	original := &WorkspaceDefinition{
		Name: "myworkspace",
		Type: WorkspaceTypeCustom,
		Path: "/some/custom/path",
		Root: "/project/root",
	}

	if err := saveDefinition(original, defPath); err != nil {
		t.Fatalf("saveDefinition failed: %v", err)
	}

	loaded, err := loadDefinition(defPath)
	if err != nil {
		t.Fatalf("loadDefinition failed: %v", err)
	}

	if loaded.Name != original.Name {
		t.Errorf("Name = %s, want %s", loaded.Name, original.Name)
	}
	if loaded.Type != original.Type {
		t.Errorf("Type = %s, want %s", loaded.Type, original.Type)
	}
	if loaded.Path != original.Path {
		t.Errorf("Path = %s, want %s", loaded.Path, original.Path)
	}
	if loaded.Root != original.Root {
		t.Errorf("Root = %s, want %s", loaded.Root, original.Root)
	}
}

func TestNewWorkspaceConfig_Defaults(t *testing.T) {
	cfg := newWorkspaceConfig("test", WorkspaceTypeCustom, "/root")

	if cfg.Name != "test" {
		t.Errorf("Name = %s, want test", cfg.Name)
	}
	if cfg.Type != WorkspaceTypeCustom {
		t.Errorf("Type = %s, want custom", cfg.Type)
	}
	if cfg.Root != "/root" {
		t.Errorf("Root = %s, want /root", cfg.Root)
	}
	if cfg.ID == "" {
		t.Error("ID should not be empty")
	}
	if cfg.CreatedAt.IsZero() {
		t.Error("CreatedAt should not be zero")
	}
	if cfg.UpdatedAt.IsZero() {
		t.Error("UpdatedAt should not be zero")
	}
	if time.Since(cfg.CreatedAt) > time.Second {
		t.Error("CreatedAt should be recent")
	}
}

func TestWorkspace_EnsureDirs(t *testing.T) {
	tmpDir := t.TempDir()
	dataDir := filepath.Join(tmpDir, "ws-data")

	ws := &Workspace{
		Definition: &WorkspaceDefinition{
			Name: "test",
			Type: WorkspaceTypeCustom,
		},
		Config: &WorkspaceConfig{
			ID:   "test-id",
			Name: "test",
			Type: WorkspaceTypeCustom,
		},
		DataDir: dataDir,
	}

	if err := ws.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs failed: %v", err)
	}

	expectedDirs := []string{
		dataDir,
		filepath.Join(dataDir, "sessions"),
		filepath.Join(dataDir, "skills"),
		filepath.Join(dataDir, "agents"),
		filepath.Join(dataDir, "space"),
		filepath.Join(dataDir, "mcp"),
	}

	for _, dir := range expectedDirs {
		info, err := os.Stat(dir)
		if err != nil {
			t.Errorf("directory %s was not created: %v", dir, err)
		} else if !info.IsDir() {
			t.Errorf("%s is not a directory", dir)
		}
	}
}

func TestWorkspace_SaveConfig(t *testing.T) {
	tmpDir := t.TempDir()
	dataDir := filepath.Join(tmpDir, "ws-data")
	os.MkdirAll(dataDir, 0755)

	ws := &Workspace{
		Definition: &WorkspaceDefinition{
			Name: "test",
			Type: WorkspaceTypeCustom,
		},
		Config: &WorkspaceConfig{
			ID:   "test-id",
			Name: "test",
			Type: WorkspaceTypeCustom,
		},
		DataDir: dataDir,
	}

	if err := ws.SaveConfig(); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}

	cfgPath := ws.ConfigPath()
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		t.Fatalf("config file was not created at %s", cfgPath)
	}

	loaded, err := loadWorkspaceConfig(cfgPath)
	if err != nil {
		t.Fatalf("loadWorkspaceConfig failed: %v", err)
	}

	if loaded.ID != ws.Config.ID {
		t.Errorf("loaded ID = %s, want %s", loaded.ID, ws.Config.ID)
	}
	if loaded.Name != ws.Config.Name {
		t.Errorf("loaded Name = %s, want %s", loaded.Name, ws.Config.Name)
	}
}

func TestWorkspace_Accessors(t *testing.T) {
	dataDir := "/data/ws"
	ws := &Workspace{
		Definition: &WorkspaceDefinition{
			Name: "proj",
			Type: WorkspaceTypeProject,
			Root: "/home/user/project",
		},
		Config: &WorkspaceConfig{
			ID:   "id-123",
			Name: "proj",
			Type: WorkspaceTypeProject,
		},
		DataDir: dataDir,
	}

	if !ws.IsProject() {
		t.Error("IsProject() should be true")
	}
	if ws.IsDefault() {
		t.Error("IsDefault() should be false")
	}

	if ws.GetSoulPath() != filepath.Join(dataDir, "SOUL.md") {
		t.Errorf("GetSoulPath() = %s", ws.GetSoulPath())
	}
	if ws.GetUserPath() != filepath.Join(dataDir, "USER.md") {
		t.Errorf("GetUserPath() = %s", ws.GetUserPath())
	}
	if ws.GetMemoryMdPath() != filepath.Join(dataDir, "MEMORY.md") {
		t.Errorf("GetMemoryMdPath() = %s", ws.GetMemoryMdPath())
	}
	if ws.SessionsDir() != filepath.Join(dataDir, "sessions") {
		t.Errorf("SessionsDir() = %s", ws.SessionsDir())
	}
	if ws.SkillsDir() != filepath.Join(dataDir, "skills") {
		t.Errorf("SkillsDir() = %s", ws.SkillsDir())
	}
	if ws.AgentsDir() != filepath.Join(dataDir, "agents") {
		t.Errorf("AgentsDir() = %s", ws.AgentsDir())
	}
	if ws.SpaceDir() != filepath.Join(dataDir, "space") {
		t.Errorf("SpaceDir() = %s", ws.SpaceDir())
	}
	if ws.MCPDir() != filepath.Join(dataDir, "mcp") {
		t.Errorf("MCPDir() = %s", ws.MCPDir())
	}
	if ws.ConfigPath() != filepath.Join(dataDir, "workspace.yaml") {
		t.Errorf("ConfigPath() = %s", ws.ConfigPath())
	}
}

func TestWorkspace_ExternalAgent(t *testing.T) {
	ws := &Workspace{
		Config: &WorkspaceConfig{
			ExternalAgents: []cobot.ExternalAgentConfig{
				{Name: "alpha", Command: "cmd1"},
				{Name: "beta", Command: "cmd2"},
			},
		},
	}

	cfg, ok := ws.ExternalAgent("alpha")
	if !ok {
		t.Fatal("expected to find alpha")
	}
	if cfg.Command != "cmd1" {
		t.Errorf("Command = %q, want cmd1", cfg.Command)
	}

	_, ok = ws.ExternalAgent("gamma")
	if ok {
		t.Error("expected not to find gamma")
	}

	// modifying returned pointer should affect original
	cfg.Command = "cmd1-modified"
	if ws.Config.ExternalAgents[0].Command != "cmd1-modified" {
		t.Error("modifying returned config did not affect original")
	}
}

func TestEffectiveSandbox_FallbackToWorkspaceRoot(t *testing.T) {
	// When no explicit sandbox.root is set, EffectiveSandbox should fall back
	// to the workspace config root so that filesystem tools resolve relative
	// paths inside the workspace directory instead of the process CWD.
	ws := &Workspace{
		Definition: &WorkspaceDefinition{
			Name: "myproject",
			Type: WorkspaceTypeProject,
			Root: "/project/root",
		},
		Config: &WorkspaceConfig{
			Name: "myproject",
			Type: WorkspaceTypeProject,
			Root: "/project/root",
		},
	}

	sandbox := ws.EffectiveSandbox(nil)
	if sandbox.Root != "/project/root" {
		t.Errorf("sandbox.Root = %q, want /project/root", sandbox.Root)
	}
	if sandbox.VirtualRoot == "" {
		t.Error("sandbox.VirtualRoot should not be empty when Root is set")
	}
}

func TestEffectiveSandbox_FallbackToDefinitionRoot(t *testing.T) {
	// When Config.Root is also empty, fall back to Definition.Root.
	ws := &Workspace{
		Definition: &WorkspaceDefinition{
			Name: "myproject",
			Type: WorkspaceTypeProject,
			Root: "/def/root",
		},
		Config: &WorkspaceConfig{
			Name: "myproject",
			Type: WorkspaceTypeProject,
		},
	}

	sandbox := ws.EffectiveSandbox(nil)
	if sandbox.Root != "/def/root" {
		t.Errorf("sandbox.Root = %q, want /def/root", sandbox.Root)
	}
}

func TestEffectiveSandbox_ExplicitRootWins(t *testing.T) {
	// An explicit sandbox root should take priority over workspace roots.
	ws := &Workspace{
		Definition: &WorkspaceDefinition{
			Name: "myproject",
			Type: WorkspaceTypeProject,
			Root: "/project/root",
		},
		Config: &WorkspaceConfig{
			Name: "myproject",
			Type: WorkspaceTypeProject,
			Root: "/project/root",
			Sandbox: sandbox.SandboxConfig{
				Root: "/explicit/sandbox",
			},
		},
	}

	sandbox := ws.EffectiveSandbox(nil)
	if sandbox.Root != "/explicit/sandbox" {
		t.Errorf("sandbox.Root = %q, want /explicit/sandbox", sandbox.Root)
	}
}

func TestEffectiveSandbox_AgentOverrideWins(t *testing.T) {
	// Agent-level sandbox root should override everything.
	ws := &Workspace{
		Definition: &WorkspaceDefinition{
			Name: "myproject",
			Type: WorkspaceTypeProject,
			Root: "/project/root",
		},
		Config: &WorkspaceConfig{
			Name: "myproject",
			Type: WorkspaceTypeProject,
			Root: "/project/root",
		},
	}

	agentSandbox := &sandbox.SandboxConfig{Root: "/agent/root"}
	sandbox := ws.EffectiveSandbox(agentSandbox)
	if sandbox.Root != "/agent/root" {
		t.Errorf("sandbox.Root = %q, want /agent/root", sandbox.Root)
	}
}

func TestEffectiveSandbox_NoRootAtAll(t *testing.T) {
	// Default workspace with no root anywhere — sandbox should be inactive.
	ws := &Workspace{
		Definition: &WorkspaceDefinition{
			Name: "default",
			Type: WorkspaceTypeDefault,
		},
		Config: &WorkspaceConfig{
			Name: "default",
			Type: WorkspaceTypeDefault,
		},
	}

	sandbox := ws.EffectiveSandbox(nil)
	if sandbox.Root != "" {
		t.Errorf("sandbox.Root = %q, want empty", sandbox.Root)
	}
	if sandbox.VirtualRoot != "" {
		t.Errorf("sandbox.VirtualRoot = %q, want empty", sandbox.VirtualRoot)
	}
}
