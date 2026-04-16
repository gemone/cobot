package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cobot-agent/cobot/internal/workspace"
	cobot "github.com/cobot-agent/cobot/pkg"
)

var workspaceCmd = &cobra.Command{
	Use:   "workspace",
	Short: "Manage workspaces",
}

var workspaceListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List all workspaces",
	Aliases: []string{"ls"},
	RunE: func(cmd *cobra.Command, args []string) error {
		manager, err := workspace.NewManager()
		if err != nil {
			return err
		}

		defs, err := manager.List()
		if err != nil {
			return err
		}

		fmt.Fprintln(cmd.OutOrStdout(), "Workspaces:")
		for _, def := range defs {
			fmt.Fprintf(cmd.OutOrStdout(), "  %s (%s)\n", def.Name, def.Type)
			if def.Root != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "     Root: %s\n", def.Root)
			}
			if def.Path != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "     Path: %s\n", def.Path)
			}
		}
		return nil
	},
}

var workspaceCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new custom workspace",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		manager, err := workspace.NewManager()
		if err != nil {
			return err
		}

		name := args[0]
		root, _ := cmd.Flags().GetString("root")
		customPath, _ := cmd.Flags().GetString("path")
		ws, err := manager.Create(name, workspace.WorkspaceTypeCustom, root, customPath)
		if err != nil {
			return err
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Created workspace '%s' (%s)\n", ws.Config.Name, ws.Config.ID[:8])
		fmt.Fprintf(cmd.OutOrStdout(), "  Data: %s\n", ws.DataDir)
		return nil
	},
}

var workspaceProjectCmd = &cobra.Command{
	Use:   "project [path]",
	Short: "Create a workspace from a project directory",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		manager, err := workspace.NewManager()
		if err != nil {
			return err
		}

		projectDir := "."
		if len(args) > 0 {
			projectDir = args[0]
		}

		absPath, err := filepath.Abs(projectDir)
		if err != nil {
			return fmt.Errorf("resolve path: %w", err)
		}

		cobotDir := filepath.Join(absPath, ".cobot")
		if err := os.MkdirAll(cobotDir, 0755); err != nil {
			return fmt.Errorf("create .cobot directory: %w", err)
		}

		ws, err := manager.Create(filepath.Base(absPath), workspace.WorkspaceTypeProject, absPath, "")
		if err != nil {
			return err
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Created project workspace '%s' (%s)\n", ws.Config.Name, ws.Config.ID[:8])
		fmt.Fprintf(cmd.OutOrStdout(), "  Root: %s\n", ws.Definition.Root)
		fmt.Fprintf(cmd.OutOrStdout(), "  Data: %s\n", ws.DataDir)
		return nil
	},
}

var workspaceDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete a workspace",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		force, _ := cmd.Flags().GetBool("force")
		if !force {
			return fmt.Errorf("use --force to confirm deletion of workspace '%s'", args[0])
		}

		manager, err := workspace.NewManager()
		if err != nil {
			return err
		}

		if err := manager.Delete(args[0]); err != nil {
			return err
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Deleted workspace '%s'\n", args[0])
		return nil
	},
}

var workspaceShowCmd = &cobra.Command{
	Use:   "show [name]",
	Short: "Show workspace details",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		manager, err := workspace.NewManager()
		if err != nil {
			return err
		}

		name := ""
		if len(args) > 0 {
			name = args[0]
		}

		ws, err := manager.ResolveByNameOrDiscover(name, ".")
		if err != nil {
			return err
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Workspace: %s (%s)\n", ws.Config.Name, ws.Config.ID[:8])
		fmt.Fprintf(cmd.OutOrStdout(), "  Type:   %s\n", ws.Definition.Type)
		if ws.Definition.Root != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "  Root:   %s\n", ws.Definition.Root)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "  Data:   %s\n", ws.DataDir)
		return nil
	},
}

var workspaceExternalAgentListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List configured external agents",
	Aliases: []string{"ls"},
	RunE: func(cmd *cobra.Command, args []string) error {
		manager, err := workspace.NewManager()
		if err != nil {
			return err
		}
		ws, err := manager.ResolveByNameOrDiscover("", ".")
		if err != nil {
			return err
		}
		if len(ws.Config.ExternalAgents) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No external agents configured")
			return nil
		}
		fmt.Fprintln(cmd.OutOrStdout(), "External agents:")
		for _, a := range ws.Config.ExternalAgents {
			fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", a.Name)
			if a.Description != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "    Description: %s\n", a.Description)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "    Command: %s\n", a.Command)
			if len(a.Args) > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "    Args: %s\n", strings.Join(a.Args, " "))
			}
			if a.Workdir != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "    Workdir: %s\n", a.Workdir)
			}
			if a.Timeout != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "    Timeout: %s\n", a.Timeout)
			}
		}
		return nil
	},
}

var workspaceExternalAgentAddCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "Add or update an external agent",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		manager, err := workspace.NewManager()
		if err != nil {
			return err
		}
		ws, err := manager.ResolveByNameOrDiscover("", ".")
		if err != nil {
			return err
		}

		name := args[0]
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("agent name cannot be empty")
		}
		if strings.ContainsAny(name, `/\:*?"<>|`) {
			return fmt.Errorf("agent name contains invalid characters")
		}
		command, _ := cmd.Flags().GetString("command")
		if command == "" {
			return fmt.Errorf("--command is required")
		}
		description, _ := cmd.Flags().GetString("description")
		argsFlag, _ := cmd.Flags().GetString("args")
		workdir, _ := cmd.Flags().GetString("workdir")
		timeout, _ := cmd.Flags().GetString("timeout")

		var agentArgs []string
		if argsFlag != "" {
			parts := strings.Split(argsFlag, ",")
			for i := range parts {
				parts[i] = strings.TrimSpace(parts[i])
			}
			agentArgs = parts
		}

		cfg := cobot.ExternalAgentConfig{
			Name:        name,
			Description: description,
			Command:     command,
			Args:        agentArgs,
			Workdir:     workdir,
			Timeout:     timeout,
		}

		found := false
		for i := range ws.Config.ExternalAgents {
			if ws.Config.ExternalAgents[i].Name == name {
				ws.Config.ExternalAgents[i] = cfg
				found = true
				break
			}
		}
		if !found {
			ws.Config.ExternalAgents = append(ws.Config.ExternalAgents, cfg)
		}

		if err := ws.SaveConfig(); err != nil {
			return fmt.Errorf("save config: %w", err)
		}
		if found {
			fmt.Fprintf(cmd.OutOrStdout(), "Updated external agent '%s'\n", name)
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "Added external agent '%s'\n", name)
		}
		return nil
	},
}

var workspaceExternalAgentRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove an external agent",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		manager, err := workspace.NewManager()
		if err != nil {
			return err
		}
		ws, err := manager.ResolveByNameOrDiscover("", ".")
		if err != nil {
			return err
		}

		name := args[0]
		found := false
		var updated []cobot.ExternalAgentConfig
		for _, a := range ws.Config.ExternalAgents {
			if a.Name == name {
				found = true
				continue
			}
			updated = append(updated, a)
		}
		if !found {
			return fmt.Errorf("external agent '%s' not found", name)
		}
		ws.Config.ExternalAgents = updated
		if err := ws.SaveConfig(); err != nil {
			return fmt.Errorf("save config: %w", err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Removed external agent '%s'\n", name)
		return nil
	},
}

var workspaceExternalAgentShowCmd = &cobra.Command{
	Use:   "show <name>",
	Short: "Show an external agent details",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		manager, err := workspace.NewManager()
		if err != nil {
			return err
		}
		ws, err := manager.ResolveByNameOrDiscover("", ".")
		if err != nil {
			return err
		}

		name := args[0]
		cfg, ok := ws.ExternalAgent(name)
		if !ok {
			return fmt.Errorf("external agent '%s' not found", name)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "External agent: %s\n", cfg.Name)
		if cfg.Description != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "  Description: %s\n", cfg.Description)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "  Command: %s\n", cfg.Command)
		if len(cfg.Args) > 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "  Args: %s\n", strings.Join(cfg.Args, " "))
		}
		if cfg.Workdir != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "  Workdir: %s\n", cfg.Workdir)
		}
		if cfg.Timeout != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "  Timeout: %s\n", cfg.Timeout)
		}
		return nil
	},
}

func init() {
	workspaceDeleteCmd.Flags().Bool("force", false, "Force deletion without confirmation")
	workspaceCreateCmd.Flags().String("root", "", "Project root directory")
	workspaceCreateCmd.Flags().String("path", "", "Custom data directory path")

	workspaceExternalAgentAddCmd.Flags().String("command", "", "Command to run")
	workspaceExternalAgentAddCmd.Flags().String("description", "", "Agent description")
	workspaceExternalAgentAddCmd.Flags().String("args", "", "Comma-separated arguments")
	workspaceExternalAgentAddCmd.Flags().String("workdir", "", "Working directory")
	workspaceExternalAgentAddCmd.Flags().String("timeout", "", "Timeout duration")

	workspaceCmd.AddCommand(workspaceListCmd)
	workspaceCmd.AddCommand(workspaceCreateCmd)
	workspaceCmd.AddCommand(workspaceProjectCmd)
	workspaceCmd.AddCommand(workspaceDeleteCmd)
	workspaceCmd.AddCommand(workspaceShowCmd)

	var externalAgentCmd = &cobra.Command{
		Use:   "external-agent",
		Short: "Manage external agents",
	}
	externalAgentCmd.AddCommand(workspaceExternalAgentListCmd)
	externalAgentCmd.AddCommand(workspaceExternalAgentAddCmd)
	externalAgentCmd.AddCommand(workspaceExternalAgentRemoveCmd)
	externalAgentCmd.AddCommand(workspaceExternalAgentShowCmd)
	workspaceCmd.AddCommand(externalAgentCmd)

	rootCmd.AddCommand(workspaceCmd)
}
