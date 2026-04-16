package tools

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cobot-agent/cobot/internal/config"
	"github.com/cobot-agent/cobot/internal/workspace"
	cobot "github.com/cobot-agent/cobot/pkg"
)

//go:embed embed_workspace_config_update_params.json
var workspaceConfigUpdateParamsJSON []byte

//go:embed embed_skill_create_params.json
var skillCreateParamsJSON []byte

//go:embed embed_persona_update_params.json
var personaUpdateParamsJSON []byte

//go:embed embed_agent_config_update_params.json
var agentConfigUpdateParamsJSON []byte

//go:embed embed_skill_update_params.json
var skillUpdateParamsJSON []byte

const maxPersonaSize = 64 * 1024 // 64 KB

type WorkspaceConfigUpdateTool struct {
	workspace *workspace.Workspace
	sandbox   *cobot.SandboxConfig
}

type WorkspaceConfigUpdateToolOption func(*WorkspaceConfigUpdateTool)

func WithWorkspaceSandbox(s *cobot.SandboxConfig) WorkspaceConfigUpdateToolOption {
	return func(t *WorkspaceConfigUpdateTool) { t.sandbox = s }
}

func (t *WorkspaceConfigUpdateTool) Name() string { return "workspace_config_update" }
func (t *WorkspaceConfigUpdateTool) Description() string {
	return "Update workspace configuration. Can modify sandbox settings."
}

func (t *WorkspaceConfigUpdateTool) Parameters() json.RawMessage {
	return json.RawMessage(workspaceConfigUpdateParamsJSON)
}

func (t *WorkspaceConfigUpdateTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var params struct {
		EnabledMCP      *[]string `json:"enabled_mcp"`
		EnabledSkills   *[]string `json:"enabled_skills"`
		SandboxRoot     *string   `json:"sandbox_root"`
		AllowPaths      *[]string `json:"allow_paths"`
		BlockedCommands *[]string `json:"blocked_commands"`
	}
	if err := decodeArgs(args, &params); err != nil {
		return "", err
	}

	cfg := t.workspace.Config
	if params.EnabledMCP != nil {
		cfg.EnabledMCP = *params.EnabledMCP
	}
	if params.EnabledSkills != nil {
		cfg.EnabledSkills = *params.EnabledSkills
	}
	if params.SandboxRoot != nil {
		if t.sandbox != nil {
			return "", fmt.Errorf("cannot modify sandbox_root while sandbox is active")
		}
		cfg.Sandbox.Root = *params.SandboxRoot
	}
	if params.AllowPaths != nil {
		if t.sandbox != nil {
			return "", fmt.Errorf("cannot modify allow_paths while sandbox is active")
		}
		cfg.Sandbox.AllowPaths = *params.AllowPaths
	}
	if params.BlockedCommands != nil {
		cfg.Sandbox.BlockedCommands = *params.BlockedCommands
	}

	if err := t.workspace.SaveConfig(); err != nil {
		return "", fmt.Errorf("save config: %w", err)
	}
	return "workspace config updated", nil
}

type skillCreateArgs struct {
	Name    string `json:"name"`
	Format  string `json:"format"`
	Content string `json:"content"`
}

type SkillCreateTool struct {
	workspace *workspace.Workspace
}

func (t *SkillCreateTool) Name() string { return "skill_create" }
func (t *SkillCreateTool) Description() string {
	return "Create a new skill in the workspace skills directory"
}

func (t *SkillCreateTool) Parameters() json.RawMessage {
	return json.RawMessage(skillCreateParamsJSON)
}

func (t *SkillCreateTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a skillCreateArgs
	if err := decodeArgs(args, &a); err != nil {
		return "", err
	}

	if a.Name == "" {
		return "", fmt.Errorf("name is required")
	}
	if err := validateName(a.Name); err != nil {
		return "", err
	}
	if a.Format != "yaml" && a.Format != "markdown" {
		return "", fmt.Errorf("format must be \"yaml\" or \"markdown\"")
	}

	ext := "yaml"
	if a.Format == "markdown" {
		ext = "md"
	}

	dir := t.workspace.SkillsDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("create skills dir: %w", err)
	}

	filename := fmt.Sprintf("%s.%s", a.Name, ext)
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, []byte(a.Content), 0644); err != nil {
		return "", fmt.Errorf("write skill file: %w", err)
	}
	return fmt.Sprintf("skill created: %s", filename), nil
}

type personaUpdateArgs struct {
	File    string `json:"file"`
	Content string `json:"content"`
}

type PersonaUpdateTool struct {
	workspace *workspace.Workspace
}

func (t *PersonaUpdateTool) Name() string        { return "persona_update" }
func (t *PersonaUpdateTool) Description() string { return "Update SOUL.md or USER.md persona files" }

func (t *PersonaUpdateTool) Parameters() json.RawMessage {
	return json.RawMessage(personaUpdateParamsJSON)
}

func (t *PersonaUpdateTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a personaUpdateArgs
	if err := decodeArgs(args, &a); err != nil {
		return "", err
	}

	if len(a.Content) > maxPersonaSize {
		return "", fmt.Errorf("content exceeds maximum size of %d bytes", maxPersonaSize)
	}

	var path string
	switch strings.ToLower(a.File) {
	case "soul":
		path = t.workspace.GetSoulPath()
	case "user":
		path = t.workspace.GetUserPath()
	default:
		return "", fmt.Errorf("file must be \"soul\" or \"user\"")
	}

	if err := os.WriteFile(path, []byte(a.Content), 0644); err != nil {
		return "", fmt.Errorf("write persona file: %w", err)
	}
	return fmt.Sprintf("%s updated", strings.ToLower(a.File)), nil
}

type AgentConfigUpdateTool struct {
	workspace *workspace.Workspace
}

func (t *AgentConfigUpdateTool) Name() string { return "agent_config_update" }
func (t *AgentConfigUpdateTool) Description() string {
	return "Update an agent's configuration file in the workspace"
}

func (t *AgentConfigUpdateTool) Parameters() json.RawMessage {
	return json.RawMessage(agentConfigUpdateParamsJSON)
}

func (t *AgentConfigUpdateTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var params struct {
		Agent         string    `json:"agent"`
		Model         *string   `json:"model"`
		SystemPrompt  *string   `json:"system_prompt"`
		EnabledMCP    *[]string `json:"enabled_mcp"`
		EnabledSkills *[]string `json:"enabled_skills"`
		MaxTurns      *int      `json:"max_turns"`
	}
	if err := decodeArgs(args, &params); err != nil {
		return "", err
	}
	if err := validateName(params.Agent); err != nil {
		return "", err
	}

	path := filepath.Join(t.workspace.AgentsDir(), params.Agent+".yaml")
	cfg, err := config.LoadAgentConfig(path)
	if err != nil {
		return "", fmt.Errorf("load agent config: %w", err)
	}

	if params.Model != nil {
		cfg.Model = *params.Model
	}
	if params.SystemPrompt != nil {
		cfg.SystemPrompt = *params.SystemPrompt
	}
	if params.EnabledMCP != nil {
		cfg.EnabledMCP = *params.EnabledMCP
	}
	if params.EnabledSkills != nil {
		cfg.EnabledSkills = *params.EnabledSkills
	}
	if params.MaxTurns != nil {
		cfg.MaxTurns = *params.MaxTurns
	}

	if err := config.SaveYAML(path, cfg); err != nil {
		return "", fmt.Errorf("save agent config: %w", err)
	}
	return fmt.Sprintf("agent config updated: %s", params.Agent), nil
}

type SkillUpdateTool struct {
	workspace *workspace.Workspace
}

func (t *SkillUpdateTool) Name() string { return "skill_update" }
func (t *SkillUpdateTool) Description() string {
	return "Update an existing skill in the workspace skills directory"
}

func (t *SkillUpdateTool) Parameters() json.RawMessage {
	return json.RawMessage(skillUpdateParamsJSON)
}

func (t *SkillUpdateTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var params struct {
		Name    string `json:"name"`
		Content string `json:"content"`
	}
	if err := decodeArgs(args, &params); err != nil {
		return "", err
	}
	if err := validateName(params.Name); err != nil {
		return "", err
	}

	dir := t.workspace.SkillsDir()
	var found string
	for _, ext := range []string{".yaml", ".yml", ".md"} {
		candidate := filepath.Join(dir, params.Name+ext)
		if _, err := os.Stat(candidate); err == nil {
			found = candidate
			break
		}
	}
	if found == "" {
		return "", fmt.Errorf("skill not found: %s", params.Name)
	}

	if err := os.WriteFile(found, []byte(params.Content), 0644); err != nil {
		return "", fmt.Errorf("write skill file: %w", err)
	}
	return fmt.Sprintf("skill updated: %s", filepath.Base(found)), nil
}

func RegisterWorkspaceTools(registry cobot.ToolRegistry, ws *workspace.Workspace, sandbox *cobot.SandboxConfig) {
	registry.Register(&WorkspaceConfigUpdateTool{workspace: ws, sandbox: sandbox})
	registry.Register(&SkillCreateTool{workspace: ws})
	registry.Register(&PersonaUpdateTool{workspace: ws})
	registry.Register(&AgentConfigUpdateTool{workspace: ws})
	registry.Register(&SkillUpdateTool{workspace: ws})
}

var (
	_ cobot.Tool = (*WorkspaceConfigUpdateTool)(nil)
	_ cobot.Tool = (*SkillCreateTool)(nil)
	_ cobot.Tool = (*PersonaUpdateTool)(nil)
	_ cobot.Tool = (*AgentConfigUpdateTool)(nil)
	_ cobot.Tool = (*SkillUpdateTool)(nil)
)
