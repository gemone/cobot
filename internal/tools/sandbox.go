package tools

import (
	"fmt"

	cobot "github.com/cobot-agent/cobot/pkg"
)

// sandboxResolvePath resolves and validates a path within the sandbox.
// If sandbox is nil, the path is returned unchanged.
func sandboxResolvePath(sandbox *cobot.SandboxConfig, path string) (string, error) {
	if sandbox == nil {
		return path, nil
	}
	originalPath := path
	resolved, err := sandbox.AutoResolvePath(path)
	if err != nil {
		return "", err
	}
	if err := sandbox.ValidatePath(resolved); err != nil {
		return "", fmt.Errorf("path %q is outside allowed workspace paths", originalPath)
	}
	return resolved, nil
}

// sandboxTool provides common sandbox functionality for tools.
type sandboxTool struct {
	sandbox *cobot.SandboxConfig
}

// describeWithSandbox appends the sandbox notice to a tool description.
func (s *sandboxTool) describeWithSandbox(desc string) string {
	if s.sandbox != nil && s.sandbox.VirtualRoot != "" {
		return desc + fmt.Sprintf(" Sandbox is active. All file paths are automatically resolved under %q — provide paths starting with %q for best results. Relative paths and other absolute paths are auto-mapped into the sandbox.", s.sandbox.VirtualRoot, s.sandbox.VirtualRoot)
	}
	return desc
}

var sandboxRewriteErr = (*cobot.SandboxConfig).RewriteError
