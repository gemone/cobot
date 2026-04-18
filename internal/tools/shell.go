package tools

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/cobot-agent/cobot/internal/sandbox"
	cobot "github.com/cobot-agent/cobot/pkg"
)

//go:embed schemas/embed_shell_exec_params.json
var shellExecParamsJSON []byte

type shellExecArgs struct {
	Command string `json:"command"`
	Dir     string `json:"dir,omitempty"`
}

const defaultShellTimeout = 2 * time.Minute

type ShellExecTool struct {
	workdir string
	config  *sandbox.SandboxConfig
	timeout time.Duration
}

type ShellExecToolOption func(*ShellExecTool)

func WithShellWorkdir(workdir string) ShellExecToolOption {
	return func(t *ShellExecTool) { t.workdir = workdir }
}

func WithShellSandboxConfig(config *sandbox.SandboxConfig) ShellExecToolOption {
	return func(t *ShellExecTool) { t.config = config }
}

// WithShellBlockedCommands is kept for backward compatibility.
// Prefer using WithShellSandboxConfig instead.
func WithShellBlockedCommands(blocked []string) ShellExecToolOption {
	return func(t *ShellExecTool) {
		if t.config == nil {
			t.config = &sandbox.SandboxConfig{}
		}
		t.config.BlockedCommands = blocked
	}
}

func WithShellAllowNetwork(allow bool) ShellExecToolOption {
	return func(t *ShellExecTool) {
		if t.config == nil {
			t.config = &sandbox.SandboxConfig{}
		}
		t.config.AllowNetwork = allow
	}
}

func WithShellTimeout(d time.Duration) ShellExecToolOption {
	return func(t *ShellExecTool) { t.timeout = d }
}

var networkCommands = []string{
	"curl", "wget", "ssh", "scp", "sftp", "nc", "ncat", "netcat",
	"telnet", "ftp", "rsync", "ping", "nslookup", "dig", "host",
}

func NewShellExecTool(opts ...ShellExecToolOption) *ShellExecTool {
	t := &ShellExecTool{
		timeout: defaultShellTimeout,
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

func (t *ShellExecTool) Name() string {
	return "shell_exec"
}

func (t *ShellExecTool) Description() string {
	desc := "Execute a shell command and return its output."
	if t.config != nil && t.config.VirtualRoot != "" {
		desc += fmt.Sprintf(" Sandbox is active. All file paths are automatically resolved under %q — provide paths starting with %q for best results. Relative paths and other absolute paths are auto-mapped into the sandbox.", t.config.VirtualRoot, t.config.VirtualRoot)
	} else if t.workdir != "" {
		desc += fmt.Sprintf(" Working directory: %s — all relative paths resolve from here.", t.workdir)
	}
	return desc
}

func (t *ShellExecTool) Parameters() json.RawMessage {
	return json.RawMessage(shellExecParamsJSON)
}

func (t *ShellExecTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a shellExecArgs
	if err := decodeArgs(args, &a); err != nil {
		return "", err
	}

	// Sandbox: rewrite virtual paths in command and dir to real filesystem paths.
	if t.config != nil && t.config.VirtualRoot != "" {
		a.Command = t.config.RewritePaths(a.Command)
		if a.Dir != "" {
			if resolved, err := sandboxResolvePath(t.config, a.Dir, false); err != nil {
				return "", err
			} else {
				a.Dir = resolved
			}
		}
	}
	// Security model:
	//   The shell executes with cmd.Dir set to the sandbox root (or a resolved
	//   subdirectory). The process runs as the OS user and can only access what
	//   the OS user can access — the sandbox is about path visibility to the
	//   LLM, not OS-level isolation. Command output is sanitized via
	//   RewriteOutputPaths before being returned to the LLM.

	cmdStr := a.Command

	// Check blocked commands via SandboxConfig.IsBlockedCommand if available.
	if t.config != nil && len(t.config.BlockedCommands) > 0 {
		if t.config.IsBlockedCommand(cmdStr) {
			return "", fmt.Errorf("command is blocked by sandbox policy")
		}
	}

	// Check network commands if network is not allowed.
	if t.config == nil || !t.config.AllowNetwork {
		if err := checkNetworkCommand(cmdStr); err != nil {
			return "", err
		}
	}

	if t.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t.timeout)
		defer cancel()
	}
	shell, shellFlag := "sh", "-c"
	if runtime.GOOS == "windows" {
		shell, shellFlag = "cmd", "/C"
	}
	cmd := exec.CommandContext(ctx, shell, shellFlag, a.Command)
	if a.Dir != "" {
		if t.config != nil && t.config.VirtualRoot != "" {
			// Sandbox mode: AutoResolvePath already resolved a.Dir to a real
			// absolute path within the sandbox root. No additional workdir-based
			// validation is needed — ValidatePath was handled by AutoResolvePath.
			cmd.Dir = a.Dir
		} else if t.workdir != "" {
			// Non-sandbox mode: validate that dir is within workdir boundaries.
			originalDir := a.Dir
			absWorkdir, err := filepath.Abs(t.workdir)
			if err != nil {
				return "", fmt.Errorf("resolve workdir: %w", err)
			}
			absDir := absWorkdir
			if filepath.IsAbs(a.Dir) {
				absDir = a.Dir
			} else {
				absDir = filepath.Join(absWorkdir, a.Dir)
				if absDir, err = filepath.Abs(absDir); err != nil {
					return "", fmt.Errorf("resolve dir: %w", err)
				}
			}
			absDir = sandbox.EvalSymlinks(absDir)
			absWorkdir = sandbox.EvalSymlinks(absWorkdir)
			if !sandbox.IsSubpath(absDir, absWorkdir) {
				return "", fmt.Errorf("dir %q is outside workspace boundaries", originalDir)
			}
			cmd.Dir = absDir
		} else {
			cmd.Dir = a.Dir
		}
	} else if t.workdir != "" {
		cmd.Dir = t.workdir
	}
	out, err := cmd.CombinedOutput()
	output := string(out)

	// Rewrite real filesystem paths in output back to virtual paths for LLM.
	if t.config != nil && t.config.VirtualRoot != "" {
		output = t.config.RewriteOutputPaths(output)
	}

	if ctx.Err() == context.DeadlineExceeded {
		return output, fmt.Errorf("shell command timed out after %s", t.timeout)
	}
	if err != nil {
		if t.config != nil && t.config.VirtualRoot != "" {
			return output, fmt.Errorf("%s", t.config.RewriteOutputPaths(err.Error()))
		}
		return output, err
	}
	return output, nil
}

// checkNetworkCommand validates that the command does not use network tools when networking is disabled.
func checkNetworkCommand(cmdStr string) error {
	for _, nc := range networkCommands {
		if isNetworkCommandUsed(cmdStr, nc) {
			return fmt.Errorf("network command %q is blocked (allow_network is false)", nc)
		}
	}
	return nil
}

// isNetworkCommandUsed checks if a network command is referenced in the given command string.
func isNetworkCommandUsed(cmdStr, nc string) bool {
	for _, segment := range sandbox.ShellCommandSegments(cmdStr) {
		fields := strings.Fields(strings.TrimSpace(segment))
		if len(fields) == 0 {
			continue
		}
		if filepath.Base(fields[0]) == nc {
			return true
		}
	}
	return false
}

var _ cobot.Tool = (*ShellExecTool)(nil)
