package cobot

import (
	"fmt"
	"path/filepath"
	"strings"
)

type Config struct {
	ConfigPath   string                    `yaml:"config_path,omitempty"`
	DataPath     string                    `yaml:"data_path,omitempty"`
	Workspace    string                    `yaml:"workspace,omitempty"`
	Model        string                    `yaml:"model"`
	Temperature  float64                   `yaml:"temperature,omitempty"`
	MaxTurns     int                       `yaml:"max_turns"`
	SystemPrompt string                    `yaml:"system_prompt,omitempty"`
	Verbose      bool                      `yaml:"verbose,omitempty"`
	APIKeys      map[string]string         `yaml:"api_keys,omitempty"`
	Providers    map[string]ProviderConfig `yaml:"providers,omitempty"`
	Memory       MemoryConfig              `yaml:"memory,omitempty"`
	Session      SessionConfig             `yaml:"session,omitempty"`
}

type SandboxConfig struct {
	VirtualRoot     string   `yaml:"virtual_root,omitempty"` // Virtual path prefix the LLM sees (e.g. "/home/myworkspace")
	Root            string   `yaml:"root"`                   // Real filesystem path
	AllowPaths      []string `yaml:"allow_paths,omitempty"`
	ReadonlyPaths   []string `yaml:"readonly_paths,omitempty"`
	AllowNetwork    bool     `yaml:"allow_network"`
	BlockedCommands []string `yaml:"blocked_commands,omitempty"`
}

func (s *SandboxConfig) IsAllowed(path string, write bool) bool {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	absPath = EvalSymlinks(absPath)

	readonlyMatched := false
	for _, rp := range s.ReadonlyPaths {
		absRP, err := filepath.Abs(rp)
		if err != nil {
			continue
		}
		absRP = EvalSymlinks(absRP)
		if IsSubpath(absPath, absRP) {
			readonlyMatched = true
			if write {
				return false
			}
		}
	}
	// If a readonly path matched and this is a read operation, allow access
	// even if the path is not under Root or AllowPaths.
	if readonlyMatched && !write {
		return true
	}

	for _, ap := range s.AllowPaths {
		absAP, err := filepath.Abs(ap)
		if err != nil {
			continue
		}
		absAP = EvalSymlinks(absAP)
		if IsSubpath(absPath, absAP) {
			return true
		}
	}

	if s.Root != "" {
		absRoot, err := filepath.Abs(s.Root)
		if err != nil {
			return false
		}
		absRoot = EvalSymlinks(absRoot)
		if IsSubpath(absPath, absRoot) {
			if readonlyMatched && write {
				return false
			}
			return true
		}
	}

	return false
}

// ResolvePath validates that path starts with VirtualRoot and translates it to the real filesystem path.
// Returns an error if the path does not start with VirtualRoot when VirtualRoot is set.
// If VirtualRoot is empty, returns the path unchanged (no sandbox enforcement).
func (s *SandboxConfig) ResolvePath(path string) (string, error) {
	if s == nil || s.VirtualRoot == "" {
		return path, nil
	}

	// Clean the path and VirtualRoot
	cleaned := filepath.Clean(path)
	vr := filepath.Clean(s.VirtualRoot)

	// Must start with VirtualRoot as a full path component
	if cleaned != vr && !strings.HasPrefix(cleaned, vr+"/") {
		return "", fmt.Errorf("path %q must start with %q (sandbox enforced)", path, s.VirtualRoot)
	}

	// Strip VirtualRoot prefix and append remainder to Root
	rel := strings.TrimPrefix(cleaned, vr)
	if rel == "" || rel == "/" {
		return s.Root, nil
	}
	// rel now starts with "/" (e.g. "/src/main.go")
	return filepath.Join(s.Root, rel[1:]), nil
}

func (s *SandboxConfig) IsBlockedCommand(cmd string) bool {
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return false
	}
	baseCmd := filepath.Base(fields[0])
	for _, blocked := range s.BlockedCommands {
		if baseCmd == blocked || cmd == blocked || strings.HasPrefix(cmd, blocked+" ") || strings.HasPrefix(cmd, blocked) {
			return true
		}
		if strings.Contains(cmd, "|"+blocked) || strings.Contains(cmd, ";"+blocked) ||
			strings.Contains(cmd, "$("+blocked) || strings.Contains(cmd, "`"+blocked) {
			return true
		}
	}
	return false
}

type MemoryConfig struct {
	Enabled bool   `yaml:"enabled"`
	DBPath  string `yaml:"db_path"`
}

// SessionConfig controls automatic summarization and compression thresholds.
// Context window size is determined by the model, not by session config.
type SessionConfig struct {
	// SummarizeThreshold triggers summarization when token usage reaches this
	// fraction of the model's context window (e.g. 0.5 = 50%). Default: 0.5.
	SummarizeThreshold float64 `yaml:"summarize_threshold,omitempty"`
	// CompressThreshold triggers aggressive compression when token usage
	// reaches this fraction (e.g. 0.7 = 70%). Default: 0.7.
	CompressThreshold float64 `yaml:"compress_threshold,omitempty"`
	// SummarizeTurns triggers summarization after this many conversation
	// turns, regardless of token usage. 0 = disabled. Default: 60.
	SummarizeTurns int `yaml:"summarize_turns,omitempty"`
	// SummaryModel specifies a dedicated model for summarization/compression.
	// Empty string means use the current conversation model.
	SummaryModel string `yaml:"summary_model,omitempty"`
}

type ProviderConfig struct {
	BaseURL string            `yaml:"base_url"`
	Headers map[string]string `yaml:"headers"`
}

func DefaultConfig() *Config {
	return &Config{
		MaxTurns: DefaultMaxTurns,
		Model:    "openai:gpt-4o",
		APIKeys:  make(map[string]string),
		Memory: MemoryConfig{
			Enabled: true,
		},
		Session: DefaultSessionConfig(),
	}
}

func DefaultSessionConfig() SessionConfig {
	return SessionConfig{
		SummarizeThreshold: 0.5,
		CompressThreshold:  0.7,
		SummarizeTurns:     60,
	}
}
