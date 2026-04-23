package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cobot-agent/cobot/internal/workspace"
	cobot "github.com/cobot-agent/cobot/pkg"
	"gopkg.in/yaml.v3"
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "First-time setup wizard",
	RunE: func(cmd *cobra.Command, args []string) error {
		reader := bufio.NewReader(os.Stdin)

		fmt.Println("Cobot Personal Agent Setup")
		fmt.Println("==========================")
		fmt.Println()

		ws, err := resolveWorkspace()
		if err != nil {
			return fmt.Errorf("resolve workspace: %w", err)
		}
		if err := ws.EnsureDirs(); err != nil {
			return fmt.Errorf("ensure workspace dirs: %w", err)
		}

		fmt.Print("LLM provider [openai]: ")
		provider, _ := reader.ReadString('\n')
		provider = strings.TrimSpace(provider)
		if provider == "" {
			provider = "openai"
		}

		fmt.Printf("%s API key: ", strings.Title(provider))
		apiKey, _ := reader.ReadString('\n')
		apiKey = strings.TrimSpace(apiKey)

		fmt.Print("Model name [gpt-4o]: ")
		model, _ := reader.ReadString('\n')
		model = strings.TrimSpace(model)
		if model == "" {
			model = "gpt-4o"
		}

		cfg := cobot.DefaultConfig()
		cfg.Model = provider + ":" + model
		cfg.APIKeys = map[string]string{provider: apiKey}

		configDir := workspace.ConfigDir()
		configPath := filepath.Join(configDir, "config.yaml")

		data, err := yaml.Marshal(cfg)
		if err != nil {
			return err
		}

		if err := os.WriteFile(configPath, data, 0600); err != nil {
			return fmt.Errorf("write config: %w", err)
		}

		fmt.Println()
		fmt.Printf("Config saved to %s\n", configPath)
		fmt.Printf("Workspace: %s (%s)\n", ws.Config.Name, ws.Config.ID[:8])
		fmt.Printf("SOUL:   %s\n", ws.GetSoulPath())
		fmt.Printf("USER:   %s\n", ws.GetUserPath())
		fmt.Printf("MEMORY: %s\n", ws.GetMemoryDBPath())
		fmt.Printf("Space:  %s\n", ws.SpaceDir())
		fmt.Printf("MCP:    %s\n", ws.MCPDir())
		fmt.Println()
		fmt.Println("You can now use cobot:")
		fmt.Println("  cobot chat \"hello\"")
		fmt.Println("  cobot tui")
		fmt.Println()
		fmt.Println("Workspace commands:")
		fmt.Println("  cobot workspace list")
		fmt.Println("  cobot workspace create <name>")
		fmt.Println("  cobot workspace show")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(setupCmd)
}
