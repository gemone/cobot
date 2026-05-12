package workspace

import (
	"fmt"
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
		Sandbox: &sandbox.SandboxConfig{
			AllowNetwork:        true,
			AllowedNetworkTools: []string{"web_fetch"},
		},
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
	if loaded.Sandbox == nil {
		t.Fatal("Sandbox is nil")
	}
	if !loaded.Sandbox.AllowNetwork {
		t.Fatal("Sandbox.AllowNetwork = false, want true")
	}
	if len(loaded.Sandbox.AllowedNetworkTools) != 1 || loaded.Sandbox.AllowedNetworkTools[0] != "web_fetch" {
		t.Fatalf("Sandbox.AllowedNetworkTools = %v, want [web_fetch]", loaded.Sandbox.AllowedNetworkTools)
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

func TestEffectiveSandbox_MergesPolicyFields(t *testing.T) {
	ws := &Workspace{
		Definition: &WorkspaceDefinition{
			Name: "myproject",
			Type: WorkspaceTypeProject,
			Root: "/project/root",
			Sandbox: &sandbox.SandboxConfig{
				AllowPaths:      []string{"/definition/allow"},
				ReadonlyPaths:   []string{"/definition/readonly"},
				BlockedCommands: []string{"wget"},
			},
		},
		Config: &WorkspaceConfig{
			Name: "myproject",
			Type: WorkspaceTypeProject,
			Sandbox: sandbox.SandboxConfig{
				Root:            "/workspace/root",
				AllowPaths:      []string{"/workspace/allow"},
				ReadonlyPaths:   []string{"/workspace/readonly"},
				BlockedCommands: []string{"rm -rf"},
			},
		},
	}
	ws.Config.Sandbox.SetAllowNetwork(true)

	agentSandbox := &sandbox.SandboxConfig{
		ReadonlyPaths: []string{"/agent/readonly"},
	}
	agentSandbox.SetAllowNetwork(false)

	effective := ws.EffectiveSandbox(agentSandbox)
	if effective.Root != "/workspace/root" {
		t.Fatalf("effective.Root = %q, want /workspace/root", effective.Root)
	}
	if len(effective.AllowPaths) != 1 || effective.AllowPaths[0] != "/workspace/allow" {
		t.Fatalf("effective.AllowPaths = %v, want [/workspace/allow]", effective.AllowPaths)
	}
	if len(effective.ReadonlyPaths) != 1 || effective.ReadonlyPaths[0] != "/agent/readonly" {
		t.Fatalf("effective.ReadonlyPaths = %v, want [/agent/readonly]", effective.ReadonlyPaths)
	}
	if len(effective.BlockedCommands) != 1 || effective.BlockedCommands[0] != "rm -rf" {
		t.Fatalf("effective.BlockedCommands = %v, want [rm -rf]", effective.BlockedCommands)
	}
	if effective.AllowNetwork {
		t.Fatal("effective.AllowNetwork = true, want false")
	}
	if !effective.HasAllowNetworkOverride() {
		t.Fatal("expected effective sandbox to track allow_network override")
	}
	if effective.AllowedNetworkTools != nil {
		t.Fatalf("expected no allowed_network_tools in merged sandbox, got %v", effective.AllowedNetworkTools)
	}
	if effective.VirtualRoot == "" {
		t.Fatal("effective.VirtualRoot should not be empty")
	}
}

func TestEffectiveSandbox_DefinitionSandboxIsApplied(t *testing.T) {
	ws := &Workspace{
		Definition: &WorkspaceDefinition{
			Name: "default",
			Type: WorkspaceTypeDefault,
			Sandbox: &sandbox.SandboxConfig{
				AllowNetwork:        true,
				AllowedNetworkTools: []string{"web_fetch"},
			},
		},
		Config: &WorkspaceConfig{
			Name: "default",
			Type: WorkspaceTypeDefault,
			Sandbox: sandbox.SandboxConfig{
				AllowNetwork:        false,
				AllowedNetworkTools: []string{"web_fetch", "shell_exec"},
			},
		},
		DataDir: "/data/workspaces/default",
	}

	agentSandbox := &sandbox.SandboxConfig{
		AllowedNetworkTools: []string{"web_fetch", "shell_exec"},
	}
	sb := ws.EffectiveSandbox(agentSandbox)
	if !sb.AllowNetwork {
		t.Fatal("expected definition sandbox allow_network=true to apply")
	}
	sandboxInstance := sandbox.NewSandbox(*sb)
	if !sandboxInstance.AllowsNetworkTool("web_fetch") {
		t.Fatal("expected web_fetch to be allowed by definition sandbox")
	}
	if sandboxInstance.AllowsNetworkTool("shell_exec") {
		t.Fatal("expected shell_exec to remain blocked by default")
	}
	if !sb.AllowNetwork {
		t.Fatal("expected definition sandbox allow_network=true to win over workspace state")
	}
	if len(sb.AllowedNetworkTools) != 1 || sb.AllowedNetworkTools[0] != "web_fetch" {
		t.Fatalf("expected definition allowlist to win, got %v", sb.AllowedNetworkTools)
	}
}

func TestEffectiveSandbox_NoRootAtAll(t *testing.T) {
	// Default workspace with no root anywhere — sandbox should default to SpaceDir().
	ws := &Workspace{
		Definition: &WorkspaceDefinition{
			Name: "default",
			Type: WorkspaceTypeDefault,
		},
		Config: &WorkspaceConfig{
			Name: "default",
			Type: WorkspaceTypeDefault,
		},
		DataDir: "/data/workspaces/default",
	}

	sb := ws.EffectiveSandbox(nil)
	wantRoot := filepath.Join("/data/workspaces/default", "space")
	if sb.Root != wantRoot {
		t.Errorf("sandbox.Root = %q, want %q", sb.Root, wantRoot)
	}
	if sb.VirtualRoot == "" {
		t.Error("sandbox.VirtualRoot should not be empty")
	}
}

func TestWorkspace_SaveConfig_Concurrent(t *testing.T) {
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

	// Launch multiple goroutines that concurrently modify and save config
	done := make(chan error, 10)
	for i := 0; i < 10; i++ {
		go func(idx int) {
			// Simulate modifying config
			ws.Config.EnabledSkills = append(ws.Config.EnabledSkills, fmt.Sprintf("skill-%d", idx))

			// Save config
			if err := ws.SaveConfig(); err != nil {
				done <- err
			} else {
				done <- nil
			}
		}(i)
	}

	// Collect all results
	var errors []error
	for i := 0; i < 10; i++ {
		err := <-done
		if err != nil {
			errors = append(errors, err)
		}
	}

	// Check for errors
	if len(errors) > 0 {
		t.Fatalf("concurrent SaveConfig failed with errors: %v", errors)
	}

	// Verify config was saved and can be loaded
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

	// Verify that the YAML file is not corrupted by checking if it can be parsed
	if len(loaded.EnabledSkills) == 0 {
		t.Error("EnabledSkills should not be empty after concurrent saves")
	}
}
