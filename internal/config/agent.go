package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cobot-agent/cobot/internal/sandbox"
	cobot "github.com/cobot-agent/cobot/pkg"
)

type AgentConfig struct {
	Name          string                 `yaml:"name"`
	Model         string                 `yaml:"model"`
	SystemPrompt  string                 `yaml:"system_prompt"`
	EnabledMCP    []string               `yaml:"enabled_mcp,omitempty"`
	EnabledSkills []string               `yaml:"enabled_skills,omitempty"`
	MaxTurns      int                    `yaml:"max_turns,omitempty"`
	Sandbox       *sandbox.SandboxConfig `yaml:"sandbox,omitempty"`
	Session       *cobot.SessionConfig   `yaml:"session,omitempty"`

	// MemoryPromoteInterval controls how often (in turns) the agent triggers
	// an asynchronous STM→LTM promotion. Zero disables promotion.
	MemoryPromoteInterval int `yaml:"memory_promote_interval,omitempty"`
	// MemoryPromoteThreshold is the minimum number of STM items required
	// before a mid-session promotion is attempted. Zero defaults to 5.
	MemoryPromoteThreshold int `yaml:"memory_promote_threshold,omitempty"`
	// SkillSyncInterval is the background LTM→skill sync interval in minutes.
	// Zero defaults to 60 minutes.
	SkillSyncInterval int `yaml:"skill_sync_interval,omitempty"`
}

func LoadAgentConfig(path string) (*AgentConfig, error) {
	cfg := &AgentConfig{}
	if err := LoadYAML(path, cfg); err != nil {
		return nil, fmt.Errorf("agent config %s: %w", path, err)
	}

	if cfg.Name == "" {
		base := filepath.Base(path)
		cfg.Name = strings.TrimSuffix(base, filepath.Ext(base))
	}

	if cfg.MaxTurns == 0 {
		cfg.MaxTurns = cobot.DefaultMaxTurns
	}

	return cfg, nil
}

func LoadAgentConfigs(dir string) (map[string]*AgentConfig, error) {
	configs := make(map[string]*AgentConfig)

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return configs, nil
		}
		return nil, fmt.Errorf("read agent configs dir %s: %w", dir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		cfg, err := LoadAgentConfig(path)
		if err != nil {
			return nil, err
		}

		configs[cfg.Name] = cfg
	}

	return configs, nil
}
