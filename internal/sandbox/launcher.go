package sandbox

import (
	"context"
	"fmt"
)

// LaunchRequest contains the parameters for launching a sandboxed command.
type LaunchRequest struct {
	Shell     string
	ShellFlag string
	Command   string
	Dir       string
	Config    *SandboxConfig
}

// Launcher runs commands in a sandbox environment.
type Launcher struct {
	sandboxConfig *SandboxConfig
	launchFunc    func(ctx context.Context, req *LaunchRequest) ([]byte, error)
}

// LauncherOption configures a Launcher.
type LauncherOption func(*Launcher)

// WithSandboxConfig sets the sandbox configuration for a Launcher.
func WithSandboxConfig(cfg *SandboxConfig) LauncherOption {
	return func(l *Launcher) {
		l.sandboxConfig = cfg
	}
}

// WithLaunchFunc sets a custom launch function for testing.
func WithLaunchFunc(fn func(ctx context.Context, req *LaunchRequest) ([]byte, error)) LauncherOption {
	return func(l *Launcher) {
		l.launchFunc = fn
	}
}

// NewLauncher creates a Launcher with the given options.
func NewLauncher(opts ...LauncherOption) *Launcher {
	launcher := &Launcher{launchFunc: platformLaunch}
	for _, opt := range opts {
		opt(launcher)
	}
	return launcher
}

// Launch runs a command in a sandboxed subprocess.
// On Linux, this uses Landlock for filesystem and network isolation.
// On other platforms, it falls back to direct command execution.
func (l *Launcher) Launch(ctx context.Context, req *LaunchRequest) ([]byte, error) {
	if req == nil {
		return nil, fmt.Errorf("launch request is required")
	}
	if l == nil {
		l = NewLauncher()
	}
	// Merge launcher-level config as default when request has no config.
	if req.Config == nil && l.sandboxConfig != nil {
		req.Config = l.sandboxConfig
	}
	if l.launchFunc != nil {
		return l.launchFunc(ctx, req)
	}
	return platformLaunch(ctx, req)
}
