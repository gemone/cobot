//go:build !linux

package sandbox

import (
	"context"
	"os/exec"
)

func platformLaunch(ctx context.Context, req *LaunchRequest) ([]byte, error) {
	cmd := exec.CommandContext(ctx, req.Shell, req.ShellFlag, req.Command)
	if req.Dir != "" {
		cmd.Dir = req.Dir
	}
	return cmd.CombinedOutput()
}
