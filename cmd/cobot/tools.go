package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var toolsCmd = &cobra.Command{
	Use:   "tools",
	Short: "List and manage tools",
}

var toolsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available tools",
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, err := loadConfig(); err != nil {
			return err
		}

		builtinTools := []string{
			"filesystem_read", "filesystem_write", "shell_exec",
			"filesystem_grep",
			"memory_search", "memory_store", "l3_deep_search",
			"workspace_config_update", "skill_create", "persona_update",
			"agent_config_update", "skill_update", "delegate",
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Built-in tools (%d):\n", len(builtinTools))
		for _, name := range builtinTools {
			fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", name)
		}

		return nil
	},
}

func init() {
	toolsCmd.AddCommand(toolsListCmd)
	rootCmd.AddCommand(toolsCmd)
}
