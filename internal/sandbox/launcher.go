package sandbox

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
)

type LaunchRequest struct {
	Shell     string
	ShellFlag string
	Command   string
	Dir       string
	Config    *SandboxConfig
}

type Backend interface {
	Launch(context.Context, *LaunchRequest) ([]byte, error)
}

type Launcher struct {
	backend        Backend
	sandboxConfig  *SandboxConfig
}

type LauncherOption func(*Launcher)

func WithBackend(backend Backend) LauncherOption {
	return func(l *Launcher) {
		if backend != nil {
			l.backend = backend
		}
	}
}

func WithSandboxConfig(cfg *SandboxConfig) LauncherOption {
	return func(l *Launcher) {
		l.sandboxConfig = cfg
	}
}

func NewLauncher(opts ...LauncherOption) *Launcher {
	launcher := &Launcher{backend: hostBackend{}}
	for _, opt := range opts {
		opt(launcher)
	}
	if launcher.backend == nil {
		launcher.backend = hostBackend{}
	}
	return launcher
}

func (l *Launcher) Launch(ctx context.Context, req *LaunchRequest) ([]byte, error) {
	if req == nil {
		return nil, fmt.Errorf("launch request is required")
	}
	if l == nil {
		l = NewLauncher()
	}

	// Select backend: bwrap on Linux when sandbox is active, otherwise host.
	backend := l.backend
	if backend == nil {
		backend = hostBackend{}
	}
	if l.sandboxConfig != nil && l.sandboxConfig.VirtualRoot != "" && runtime.GOOS == "linux" {
		backend = BwrapBackend{}
	}

	return backend.Launch(ctx, req)
}

type hostBackend struct{}

func (hostBackend) Launch(ctx context.Context, req *LaunchRequest) ([]byte, error) {
	if req == nil {
		return nil, fmt.Errorf("launch request is required")
	}
	cmd := exec.CommandContext(ctx, req.Shell, req.ShellFlag, req.Command)
	if req.Dir != "" {
		cmd.Dir = req.Dir
	}
	return cmd.CombinedOutput()
}
