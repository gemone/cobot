package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/cobot-agent/cobot/internal/config"
	"github.com/cobot-agent/cobot/internal/debuglog"
	"github.com/cobot-agent/cobot/internal/sandbox"
	"github.com/cobot-agent/cobot/internal/workspace"
	cobot "github.com/cobot-agent/cobot/pkg"
)

func init() {
	// Handle sandbox child mode early, before any other initialization.
	// If this process was re-executed in Landlock child mode, detect the
	// child-mode sentinel, apply the Landlock policy, and complete the
	// child execution path before normal CLI setup continues.
	if sandbox.HandleSandboxChildMode() {
		os.Exit(0)
	}
}

var (
	cfgPath       string
	dataPath      string
	workspacePath string
	modelName     string
	debugMode     bool
)

var rootCmd = &cobra.Command{
	Use:     "cobot",
	Short:   "A personal AI agent system",
	Long:    "Cobot is a Go-based personal agent system with memory, tools, and protocols.",
	Version: "0.1.0",
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		if debugMode {
			if err := debuglog.Init(workspace.LogsDir()); err != nil {
				fmt.Fprintf(os.Stderr, "warning: debug log init failed: %v\n", err)
				slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
			}
		}
	},
	PersistentPostRun: func(cmd *cobra.Command, args []string) {
		debuglog.Close()
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		return tuiCmd.RunE(cmd, args)
	},
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&cfgPath, "config", "c", "", "config file path (default: $COBOT_CONFIG_PATH/config.yaml or $XDG_CONFIG_HOME/cobot/config.yaml)")
	rootCmd.PersistentFlags().StringVar(&dataPath, "data", "", "data directory (default: $COBOT_DATA_PATH or ~/.local/share/cobot)")
	rootCmd.PersistentFlags().StringVarP(&workspacePath, "workspace", "w", "", "workspace name or directory")
	rootCmd.PersistentFlags().StringVarP(&modelName, "model", "m", "", "LLM model (e.g. openai:gpt-4o)")
	rootCmd.PersistentFlags().BoolVarP(&debugMode, "debug", "d", false, "enable debug logging")
}

func loadConfig() (*cobot.Config, error) {
	if dataPath != "" {
		os.Setenv("COBOT_DATA_PATH", dataPath)
	}

	if cfgPath != "" {
		os.Setenv("COBOT_CONFIG_PATH", filepath.Dir(cfgPath))
	}

	cfg := cobot.DefaultConfig()

	if cfgPath != "" {
		slog.Debug("loading config from flag", "path", cfgPath)
		if _, err := os.Stat(cfgPath); err == nil {
			if err := config.LoadFromFile(cfg, cfgPath); err != nil {
				return nil, fmt.Errorf("load config: %w", err)
			}
		}
	} else {
		globalCfg := workspace.GlobalConfigPath()
		slog.Debug("loading config from global", "path", globalCfg)
		if _, err := os.Stat(globalCfg); err == nil {
			if err := config.LoadFromFile(cfg, globalCfg); err != nil {
				return nil, fmt.Errorf("load global config: %w", err)
			}
		}
	}

	config.ApplyEnvVars(cfg)

	wsName := workspacePath
	if wsName == "" {
		wsName = cfg.Workspace
	}

	m, err := workspace.NewManager()
	if err == nil {
		ws, err := m.ResolveByNameOrDiscover(wsName, ".")
		if err == nil {
			cfg.Workspace = ws.Definition.Root
			if ws.Definition.Root != "" {
				if err := config.LoadWorkspaceConfig(cfg, ws.Definition.Root); err != nil {
					fmt.Fprintf(os.Stderr, "warning: %v\n", err)
				}
			}
		}
	}

	if modelName != "" {
		cfg.Model = modelName
	}

	slog.Debug("config resolved", "model", cfg.Model, "max_turns", cfg.MaxTurns)

	return cfg, nil
}

func main() {
	cobra.CheckErr(rootCmd.Execute())
}
