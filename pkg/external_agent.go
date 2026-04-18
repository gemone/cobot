package cobot

type ExternalAgentConfig struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description,omitempty"`
	Command     string   `yaml:"command"`
	Args        []string `yaml:"args,omitempty"`
	Workdir     string   `yaml:"workdir,omitempty"`
	Timeout     string   `yaml:"timeout,omitempty"`
}
