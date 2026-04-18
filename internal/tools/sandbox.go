package tools

import (
	"fmt"

	"github.com/cobot-agent/cobot/internal/sandbox"
)

// sandboxResolvePath resolves and validates a path within the sandbox.
// If sandbox is nil, the path is returned unchanged.
func sandboxResolvePath(cfg *sandbox.SandboxConfig, path string, write bool) (string, error) {
	if cfg == nil {
		return path, nil
	}
	originalPath := path
	resolved, err := cfg.AutoResolvePath(path)
	if err != nil {
		return "", err
	}
	if err := cfg.ValidatePath(resolved); err != nil {
		return "", fmt.Errorf("path %q is outside allowed workspace paths", originalPath)
	}
	if write && !cfg.IsAllowed(resolved, true) {
		return "", fmt.Errorf("path %q is readonly or blocked by sandbox policy", originalPath)
	}
	return resolved, nil
}

// sandboxTool provides common sandbox functionality for tools.
type sandboxTool struct {
	sandbox *sandbox.SandboxConfig
}

// describeWithSandbox appends the sandbox notice to a tool description.
func (s *sandboxTool) describeWithSandbox(desc string) string {
	if s.sandbox != nil && s.sandbox.VirtualRoot != "" {
		return desc + fmt.Sprintf(" Sandbox is active. All file paths are automatically resolved under %q — provide paths starting with %q for best results. Relative paths and other absolute paths are auto-mapped into the sandbox.", s.sandbox.VirtualRoot, s.sandbox.VirtualRoot)
	}
	return desc
}

var sandboxRewriteErr = (*sandbox.SandboxConfig).RewriteError
