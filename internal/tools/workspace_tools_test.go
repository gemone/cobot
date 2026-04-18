package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cobot-agent/cobot/internal/sandbox"
	"github.com/cobot-agent/cobot/internal/workspace"
)

func newTestWorkspace(t *testing.T) *workspace.Workspace {
	t.Helper()
	dir := t.TempDir()
	def := &workspace.WorkspaceDefinition{
		Name: "test",
		Type: workspace.WorkspaceTypeDefault,
	}
	cfg := &workspace.WorkspaceConfig{
		ID:        "test-id",
		Name:      "test",
		Type:      workspace.WorkspaceTypeDefault,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	ws := &workspace.Workspace{
		Definition: def,
		Config:     cfg,
		DataDir:    dir,
	}
	if err := ws.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	return ws
}

func TestWorkspaceConfigUpdateTool(t *testing.T) {
	ws := newTestWorkspace(t)
	if err := ws.SaveConfig(); err != nil {
		t.Fatal(err)
	}

	tool := &WorkspaceConfigUpdateTool{workspace: ws}

	args, _ := json.Marshal(map[string]interface{}{
		"enabled_mcp": []string{"mcp-server-1", "mcp-server-2"},
	})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if result != "workspace config updated" {
		t.Fatalf("unexpected result: %s", result)
	}

	if len(ws.Config.EnabledMCP) != 2 {
		t.Fatalf("expected 2 enabled MCP, got %d", len(ws.Config.EnabledMCP))
	}
	if ws.Config.EnabledMCP[0] != "mcp-server-1" || ws.Config.EnabledMCP[1] != "mcp-server-2" {
		t.Fatalf("unexpected enabled_mcp: %v", ws.Config.EnabledMCP)
	}

	cfgPath := ws.ConfigPath()
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("saved config:\n%s", string(data))
}

func TestWorkspaceConfigUpdateTool_Sandbox(t *testing.T) {
	ws := newTestWorkspace(t)
	if err := ws.SaveConfig(); err != nil {
		t.Fatal(err)
	}

	tool := &WorkspaceConfigUpdateTool{workspace: ws}

	args, _ := json.Marshal(map[string]interface{}{
		"sandbox_root":     "/tmp/sandbox",
		"allow_paths":      []string{"/usr/local"},
		"blocked_commands": []string{"rm -rf"},
	})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if result != "workspace config updated" {
		t.Fatalf("unexpected result: %s", result)
	}

	if ws.Config.Sandbox.Root != "/tmp/sandbox" {
		t.Fatalf("expected sandbox root /tmp/sandbox, got %s", ws.Config.Sandbox.Root)
	}
	if len(ws.Config.Sandbox.AllowPaths) != 1 || ws.Config.Sandbox.AllowPaths[0] != "/usr/local" {
		t.Fatalf("unexpected allow_paths: %v", ws.Config.Sandbox.AllowPaths)
	}
	if len(ws.Config.Sandbox.BlockedCommands) != 1 || ws.Config.Sandbox.BlockedCommands[0] != "rm -rf" {
		t.Fatalf("unexpected blocked_commands: %v", ws.Config.Sandbox.BlockedCommands)
	}
}

func TestSkillCreateTool_Markdown(t *testing.T) {
	ws := newTestWorkspace(t)
	tool := &SkillCreateTool{workspace: ws}

	args, _ := json.Marshal(map[string]interface{}{
		"name":    "my-skill",
		"format":  "markdown",
		"content": "# My Skill\n\nThis is a test skill.",
	})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if result != "skill created: my-skill.md" {
		t.Fatalf("unexpected result: %s", result)
	}

	expectedPath := filepath.Join(ws.SkillsDir(), "my-skill.md")
	data, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "# My Skill\n\nThis is a test skill." {
		t.Fatalf("unexpected content: %s", string(data))
	}
}

func TestSkillCreateTool_YAML(t *testing.T) {
	ws := newTestWorkspace(t)
	tool := &SkillCreateTool{workspace: ws}

	args, _ := json.Marshal(map[string]interface{}{
		"name":    "yaml-skill",
		"format":  "yaml",
		"content": "name: yaml-skill\ndescription: test",
	})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if result != "skill created: yaml-skill.yaml" {
		t.Fatalf("unexpected result: %s", result)
	}

	expectedPath := filepath.Join(ws.SkillsDir(), "yaml-skill.yaml")
	data, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "name: yaml-skill\ndescription: test" {
		t.Fatalf("unexpected content: %s", string(data))
	}
}

func TestSkillCreateTool_InvalidFormat(t *testing.T) {
	ws := newTestWorkspace(t)
	tool := &SkillCreateTool{workspace: ws}

	args, _ := json.Marshal(map[string]interface{}{
		"name":    "bad",
		"format":  "txt",
		"content": "content",
	})
	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Fatal("expected error for invalid format")
	}
}

func TestPersonaUpdateTool_SOUL(t *testing.T) {
	ws := newTestWorkspace(t)
	tool := &PersonaUpdateTool{workspace: ws}

	args, _ := json.Marshal(map[string]interface{}{
		"file":    "soul",
		"content": "# Soul\n\nBe helpful and concise.",
	})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if result != "soul updated" {
		t.Fatalf("unexpected result: %s", result)
	}

	data, err := os.ReadFile(ws.GetSoulPath())
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "# Soul\n\nBe helpful and concise." {
		t.Fatalf("unexpected content: %s", string(data))
	}
}

func TestPersonaUpdateTool_USER(t *testing.T) {
	ws := newTestWorkspace(t)
	tool := &PersonaUpdateTool{workspace: ws}

	args, _ := json.Marshal(map[string]interface{}{
		"file":    "user",
		"content": "# User\n\nPrefers dark theme.",
	})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if result != "user updated" {
		t.Fatalf("unexpected result: %s", result)
	}

	data, err := os.ReadFile(ws.GetUserPath())
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "# User\n\nPrefers dark theme." {
		t.Fatalf("unexpected content: %s", string(data))
	}
}

func TestPersonaUpdateTool_InvalidFile(t *testing.T) {
	ws := newTestWorkspace(t)
	tool := &PersonaUpdateTool{workspace: ws}

	args, _ := json.Marshal(map[string]interface{}{
		"file":    "invalid",
		"content": "content",
	})
	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Fatal("expected error for invalid file")
	}
}

func TestRegisterWorkspaceTools(t *testing.T) {
	ws := newTestWorkspace(t)
	registry := NewRegistry()
	RegisterWorkspaceTools(registry, ws, nil)

	tool, err := registry.Get("workspace_config_update")
	if err != nil {
		t.Fatal(err)
	}
	if tool.Name() != "workspace_config_update" {
		t.Fatalf("unexpected name: %s", tool.Name())
	}

	tool, err = registry.Get("skill_create")
	if err != nil {
		t.Fatal(err)
	}
	if tool.Name() != "skill_create" {
		t.Fatalf("unexpected name: %s", tool.Name())
	}

	tool, err = registry.Get("persona_update")
	if err != nil {
		t.Fatal(err)
	}
	if tool.Name() != "persona_update" {
		t.Fatalf("unexpected name: %s", tool.Name())
	}
}

func TestWorkspaceConfigUpdateTool_SkillsUpdate(t *testing.T) {
	ws := newTestWorkspace(t)
	if err := ws.SaveConfig(); err != nil {
		t.Fatal(err)
	}

	tool := &WorkspaceConfigUpdateTool{workspace: ws}

	args, _ := json.Marshal(map[string]interface{}{
		"enabled_skills": []string{"coding", "writing"},
	})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if result != "workspace config updated" {
		t.Fatalf("unexpected result: %s", result)
	}

	if len(ws.Config.EnabledSkills) != 2 {
		t.Fatalf("expected 2 enabled skills, got %d", len(ws.Config.EnabledSkills))
	}
	if ws.Config.EnabledSkills[0] != "coding" || ws.Config.EnabledSkills[1] != "writing" {
		t.Fatalf("unexpected enabled_skills: %v", ws.Config.EnabledSkills)
	}
}

func TestWorkspaceConfigUpdateTool_SandboxRejectOutsidePath(t *testing.T) {
	ws := newTestWorkspace(t)
	if err := ws.SaveConfig(); err != nil {
		t.Fatal(err)
	}

	vr := sandbox.VirtualHome("test")
	sandboxCfg := &sandbox.SandboxConfig{VirtualRoot: vr, Root: "/tmp/real"}
	tool := &WorkspaceConfigUpdateTool{workspace: ws, sandbox: sandboxCfg}

	args, _ := json.Marshal(map[string]interface{}{
		"sandbox_root": "/etc/evil",
	})
	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Error("expected error for sandbox_root outside virtual root")
	}
}
