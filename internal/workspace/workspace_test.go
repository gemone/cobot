package workspace

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	cobot "github.com/cobot-agent/cobot/pkg"
)

func TestWorkspaceDefinition_ResolvePath_Default(t *testing.T) {
	def := &WorkspaceDefinition{
		Name: "myproject",
		Type: WorkspaceTypeProject,
	}
	result := def.ResolvePath("/data")
	expected := filepath.Join("/data", "myproject")
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
		filepath.Join(dataDir, "memory"),
		filepath.Join(dataDir, "sessions"),
		filepath.Join(dataDir, "skills"),
		filepath.Join(dataDir, "agents"),
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
	if ws.MemoryDir() != filepath.Join(dataDir, "memory") {
		t.Errorf("MemoryDir() = %s", ws.MemoryDir())
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
