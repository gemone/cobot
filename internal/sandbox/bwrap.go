package sandbox

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// BwrapArgs builds a bwrap command-line from a LaunchRequest and NamespaceConfig.
// bwrap must be installed on the system.
func BwrapArgs(req *LaunchRequest, nc NamespaceConfig) ([]string, error) {
	if req == nil || req.Config == nil {
		return nil, fmt.Errorf("bwrap requires a non-nil LaunchRequest with Config")
	}

	cfg := req.Config
	vr := cfg.VirtualRoot
	root := cfg.Root

	var args []string

	// PID namespace — mount /proc inside for pid-aware tools
	if nc.MountProc {
		args = append(args, "--unshare-pid", "--mount-proc")
	}

	// Network namespace — isolate networking when not allowed
	if nc.UnshareNet {
		args = append(args, "--unshare-net")
	}

	// /dev nodes — null, zero, random, urandom
	if nc.MountDev {
		args = append(args, "--dev", "/dev")
	}

	// Private /tmp
	if nc.TmpfsTmp {
		args = append(args, "--tmpfs", "/tmp")
	}

	// Bind-mount workspace root at virtual root (read-only)
	if nc.BindRoot && vr != "" && root != "" {
		args = append(args, "--bind", root, vr)
	} else if root != "" {
		args = append(args, "--bind", root, "/")
	}

	// Working directory
	if req.Dir != "" {
		args = append(args, "--chdir", req.Dir)
	} else if root != "" {
		args = append(args, "--chdir", root)
	}

	// Shell as final arguments
	shell := req.Shell
	if shell == "" {
		shell = "sh"
	}
	shellFlag := req.ShellFlag
	if shellFlag == "" {
		shellFlag = "-c"
	}

	args = append(args, "--", shell, shellFlag, req.Command)

	return args, nil
}

// BwrapBackend implements Backend by invoking bwrap with the constructed arguments.
type BwrapBackend struct{}

// Launch runs a command inside a bwrap sandbox.
func (BwrapBackend) Launch(ctx context.Context, req *LaunchRequest) ([]byte, error) {
	nc := DefaultNamespaceConfig()
	if req.Config != nil {
		nc.UnshareNet = !req.Config.AllowNetwork
	}

	bwrapArgs, err := BwrapArgs(req, nc)
	if err != nil {
		return nil, err
	}

	// Verify bwrap is available
	if err := checkBwrapAvailable(); err != nil {
		return nil, fmt.Errorf("bwrap not available: %w", err)
	}

	return exec.CommandContext(ctx, "bwrap", bwrapArgs...).CombinedOutput()
}

// checkBwrapAvailable returns an error if bwrap is not in PATH.
func checkBwrapAvailable() error {
	path, err := exec.LookPath("bwrap")
	if err != nil {
		return fmt.Errorf("bwrap not found in PATH: %w", err)
	}
	if path == "" {
		return fmt.Errorf("bwrap not found")
	}
	return nil
}

// BwrapVersion returns the bwrap version string for debugging.
func BwrapVersion() string {
	out, err := exec.Command("bwrap", "--version").CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
