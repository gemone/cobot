package tools

import (
	"context"
	"encoding/json"
	"os"
	"strings"
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
		"sandbox_root":          "/tmp/sandbox",
		"allow_paths":           []string{"/usr/local"},
		"readonly_paths":        []string{"/etc/ssl"},
		"allow_network":         false,
		"allowed_network_tools": []string{"web_fetch"},
		"blocked_commands":      []string{"rm -rf"},
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
	if len(ws.Config.Sandbox.ReadonlyPaths) != 1 || ws.Config.Sandbox.ReadonlyPaths[0] != "/etc/ssl" {
		t.Fatalf("unexpected readonly_paths: %v", ws.Config.Sandbox.ReadonlyPaths)
	}
	if ws.Config.Sandbox.AllowNetwork {
		t.Fatal("expected allow_network=false")
	}
	if !ws.Config.Sandbox.HasAllowNetworkOverride() {
		t.Fatal("expected allow_network override to be tracked")
	}
	if len(ws.Config.Sandbox.AllowedNetworkTools) != 1 || ws.Config.Sandbox.AllowedNetworkTools[0] != "web_fetch" {
		t.Fatalf("unexpected allowed_network_tools: %v", ws.Config.Sandbox.AllowedNetworkTools)
	}
	if len(ws.Config.Sandbox.BlockedCommands) != 1 || ws.Config.Sandbox.BlockedCommands[0] != "rm -rf" {
		t.Fatalf("unexpected blocked_commands: %v", ws.Config.Sandbox.BlockedCommands)
	}

	data, err := os.ReadFile(ws.ConfigPath())
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "allow_network: false") {
		t.Fatalf("saved config missing allow_network=false: %s", text)
	}
	if !strings.Contains(text, "allowed_network_tools:") {
		t.Fatalf("saved config missing allowed_network_tools: %s", text)
	}
	if !strings.Contains(text, "readonly_paths:") {
		t.Fatalf("saved config missing readonly_paths: %s", text)
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
	RegisterSkillsTools(registry, nil, ws, nil)

	tool, err := registry.Get("workspace_config_update")
	if err != nil {
		t.Fatal(err)
	}
	if tool.Name() != "workspace_config_update" {
		t.Fatalf("unexpected name: %s", tool.Name())
	}

	tool, err = registry.Get("skills_list")
	if err != nil {
		t.Fatal(err)
	}
	if tool.Name() != "skills_list" {
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
	sandboxCfg := sandbox.NewSandbox(sandbox.SandboxConfig{VirtualRoot: vr, Root: "/tmp/real"})
	tool := &WorkspaceConfigUpdateTool{workspace: ws, sandbox: sandboxCfg}

	args, _ := json.Marshal(map[string]interface{}{
		"sandbox_root": "/etc/evil",
	})
	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Error("expected error for sandbox_root outside virtual root")
	}
}

func TestWorkspaceConfigUpdateTool_ReadonlyPathsBlockedWhenSandboxActive(t *testing.T) {
	ws := newTestWorkspace(t)
	tool := &WorkspaceConfigUpdateTool{
		workspace: ws,
		sandbox:   sandbox.NewSandbox(sandbox.SandboxConfig{VirtualRoot: sandbox.VirtualHome("test"), Root: "/tmp/real"}),
	}

	args, _ := json.Marshal(map[string]interface{}{
		"readonly_paths": []string{"/etc"},
	})
	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Fatal("expected readonly_paths update to fail while sandbox is active")
	}
	if err != nil && err.Error() != "cannot modify readonly_paths while sandbox is active" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWorkspaceConfigUpdateTool_AllowedNetworkToolsFlag(t *testing.T) {
	tests := []struct {
		name        string
		tools       *[]string
		expectFlag  bool
		expectTools []string
		description string
	}{
		{
			name:        "Case 1: set allowed_network_tools to [web_fetch]",
			tools:       &[]string{"web_fetch"},
			expectFlag:  true,
			expectTools: []string{"web_fetch"},
			description: "Should set flag and store tool",
		},
		{
			name:        "Case 2: set allowed_network_tools to empty list",
			tools:       &[]string{},
			expectFlag:  true,
			expectTools: []string{},
			description: "Should set flag even for empty list (explicit override)",
		},
		{
			name:        "Case 3: do not modify allowed_network_tools (nil)",
			tools:       nil,
			expectFlag:  false,
			expectTools: nil,
			description: "Should not set flag when not explicitly modified",
		},
	}

	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			ws := newTestWorkspace(t)
			if err := ws.SaveConfig(); err != nil {
				t.Fatal(err)
			}

			tool := &WorkspaceConfigUpdateTool{workspace: ws}

			// Build args
			args := map[string]interface{}{}
			if tc.tools != nil {
				args["allowed_network_tools"] = *tc.tools
			}
			jsonArgs, _ := json.Marshal(args)

			result, err := tool.Execute(context.Background(), jsonArgs)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result != "workspace config updated" {
				t.Fatalf("unexpected result: %s", result)
			}

			// Verify AllowedNetworkTools list
			if tc.expectTools == nil {
				if ws.Config.Sandbox.AllowedNetworkTools != nil {
					t.Fatalf("expected AllowedNetworkTools to be nil, got %v", ws.Config.Sandbox.AllowedNetworkTools)
				}
			} else {
				if len(ws.Config.Sandbox.AllowedNetworkTools) != len(tc.expectTools) {
					t.Fatalf("expected %d tools, got %d", len(tc.expectTools), len(ws.Config.Sandbox.AllowedNetworkTools))
				}
				for i, expected := range tc.expectTools {
					if ws.Config.Sandbox.AllowedNetworkTools[i] != expected {
						t.Fatalf("expected tool %q at index %d, got %q", expected, i, ws.Config.Sandbox.AllowedNetworkTools[i])
					}
				}
			}

			// CRITICAL: Verify HasAllowedNetworkToolsOverride flag
			hasOverride := ws.Config.Sandbox.HasAllowedNetworkToolsOverride()
			if hasOverride != tc.expectFlag {
				t.Fatalf("%s: expected HasAllowedNetworkToolsOverride=%v, got %v", tc.description, tc.expectFlag, hasOverride)
			}

			// For non-empty tools, verify config is persisted with allowed_network_tools field
			if tc.tools != nil && len(*tc.tools) > 0 {
				data, err := os.ReadFile(ws.ConfigPath())
				if err != nil {
					t.Fatalf("read saved config: %v", err)
				}
				text := string(data)
				if !strings.Contains(text, "allowed_network_tools:") {
					t.Fatalf("saved config missing allowed_network_tools for %s: %s", tc.description, text)
				}
			}
		})
	}
}

func TestWorkspaceConfigUpdateTool_BlockedCommandsBlockedWhenSandboxActive(t *testing.T) {
	ws := newTestWorkspace(t)
	tool := &WorkspaceConfigUpdateTool{
		workspace: ws,
		sandbox:   sandbox.NewSandbox(sandbox.SandboxConfig{VirtualRoot: sandbox.VirtualHome("test"), Root: "/tmp/real"}),
	}

	args, _ := json.Marshal(map[string]interface{}{
		"blocked_commands": []string{"rm -rf", "dd"},
	})
	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Fatal("expected blocked_commands update to fail while sandbox is active")
	}
	if err != nil && err.Error() != "cannot modify blocked_commands while sandbox is active" {
		t.Fatalf("unexpected error: %v", err)
	}
}
