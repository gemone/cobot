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

// AutoResolvePath attempts to resolve a path to a real filesystem path within the sandbox.
// Unlike ResolvePath (which is strict), AutoResolvePath is lenient:
//   - If path starts with VirtualRoot: validates and translates (same as ResolvePath)
//   - If path is relative: auto-prepends VirtualRoot, then translates
//   - If path is absolute but NOT VirtualRoot: rejects with clear error message
//
// This allows tools to accept relative paths from the LLM and auto-resolve them,
// while still rejecting absolute paths that fall outside the sandbox virtual root.
// Returns an error only if the resulting path escapes the sandbox root.
func (s *SandboxConfig) AutoResolvePath(path string) (string, error) {
	if s == nil || s.VirtualRoot == "" {
		return path, nil
	}

	cleaned := filepath.Clean(path)
	vr := filepath.Clean(s.VirtualRoot)

	// Case 1: Already starts with VirtualRoot — delegate to strict ResolvePath
	if cleaned == vr || strings.HasPrefix(cleaned, vr+"/") {
		return s.ResolvePath(path)
	}

	// Case 2: Relative path — auto-prepend VirtualRoot
	if !strings.HasPrefix(cleaned, "/") {
		virtualPath := vr + "/" + cleaned
		return s.ResolvePath(virtualPath)
	}

	// Case 3: Absolute path outside VirtualRoot — reject with clear message
	return "", fmt.Errorf("path %q is absolute but does not start with %q; use a path starting with %q or a relative path (relative paths are auto-resolved under %q)", path, s.VirtualRoot, s.VirtualRoot, s.VirtualRoot)
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

// RewritePaths scans text for occurrences of VirtualRoot as a path prefix and replaces
// them with Root. This is used to rewrite command strings and other text that may contain
// virtual paths. If VirtualRoot is empty, returns text unchanged.
func (s *SandboxConfig) RewritePaths(text string) string {
	if s == nil || s.VirtualRoot == "" {
		return text
	}
	return strings.ReplaceAll(text, s.VirtualRoot, s.Root)
}

// RewriteOutputPaths replaces real filesystem paths in text with virtual paths.
// This is the reverse of RewritePaths. Used to sanitize tool output before sending to LLM.
// Handles symlink differences (e.g. /var → /private/var on macOS) by resolving both.
func (s *SandboxConfig) RewriteOutputPaths(text string) string {
	if s == nil || s.VirtualRoot == "" || s.Root == "" {
		return text
	}
	// Try resolved (symlink-expanded) root first — it's typically longer and more specific.
	resolvedRoot := EvalSymlinks(s.Root)
	result := strings.ReplaceAll(text, resolvedRoot, s.VirtualRoot)
	// Also try the original (unresolved) root in case the output uses it directly.
	if resolvedRoot != s.Root {
		result = strings.ReplaceAll(result, s.Root, s.VirtualRoot)
	}
	return result
}

// RealToVirtual converts a real filesystem path back to the virtual path
// that the LLM sees. If sandbox is not configured, returns the path unchanged.
func (s *SandboxConfig) RealToVirtual(realPath string) string {
	if s == nil || s.VirtualRoot == "" || s.Root == "" {
		return realPath
	}
	absPath, err := filepath.Abs(realPath)
	if err != nil {
		return realPath
	}
	absRoot := filepath.Clean(s.Root)
	if absPath == absRoot {
		return s.VirtualRoot
	}
	if strings.HasPrefix(absPath, absRoot+string(filepath.Separator)) {
		rel := strings.TrimPrefix(absPath, absRoot+string(filepath.Separator))
		return filepath.Join(s.VirtualRoot, rel)
	}
	return realPath
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

type ExternalAgentConfig struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description,omitempty"`
	Command     string   `yaml:"command"`
	Args        []string `yaml:"args,omitempty"`
	Workdir     string   `yaml:"workdir,omitempty"`
	Timeout     string   `yaml:"timeout,omitempty"`
}
