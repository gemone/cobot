package cobot

import "time"

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
	Channels     []ChannelConfig           `yaml:"channels,omitempty"`
	Gateway      GatewayConfig             `yaml:"gateway,omitempty"`
}

// ChannelConfig defines a named communication channel.
// Multiple channels of the same type can coexist as long as their names are unique.
type ChannelConfig struct {
	Name   string            `yaml:"name"`             // unique name, e.g. "feishu-group-a", "tui"
	Type   string            `yaml:"type"`             // "tui", "feishu", "reverse", etc.
	Config map[string]string `yaml:"config,omitempty"` // platform-specific config (app_id, etc.)
}

// EnsureAPIKeys initializes the APIKeys map if nil.
func (c *Config) EnsureAPIKeys() {
	if c.APIKeys == nil {
		c.APIKeys = make(map[string]string)
	}
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

// GatewayConfig holds gateway server settings.
type GatewayConfig struct {
	Addr string `yaml:"addr,omitempty"`
}

type ProviderConfig struct {
	BaseURL string            `yaml:"base_url"`
	Headers map[string]string `yaml:"headers"`
	// Timeout sets the HTTP response-header timeout for this provider.
	// nil (default) means no timeout — requests wait indefinitely.
	// A non-nil value sets the timeout duration.
	Timeout *time.Duration `yaml:"timeout,omitempty"`
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
