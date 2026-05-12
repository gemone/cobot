package tools

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
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
	sandbox    *sandbox.Sandbox
	timeout    time.Duration
	launchFunc func(ctx context.Context, req *sandbox.LaunchRequest) ([]byte, error)
}

type ShellExecToolOption func(*ShellExecTool)

func WithShellSandbox(sb *sandbox.Sandbox) ShellExecToolOption {
	return func(t *ShellExecTool) { t.sandbox = sb }
}

// WithShellLaunchFunc sets a custom launch function for testing.
// When set, it is used instead of sandbox.Launch or the default launcher.
func WithShellLaunchFunc(fn func(ctx context.Context, req *sandbox.LaunchRequest) ([]byte, error)) ShellExecToolOption {
	return func(t *ShellExecTool) { t.launchFunc = fn }
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
	return t.sandbox.Describe("Execute a shell command and return its output.")
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
	if t.sandbox.Active() {
		a.Command = t.sandbox.RewriteCommand(a.Command)
		if a.Dir != "" {
			if resolved, err := t.sandbox.Resolve(a.Dir, false); err != nil {
				return "", err
			} else {
				a.Dir = resolved
			}
		}
	}
	// Security model (dual layer):
	//   1. Virtual path layer: the LLM sees virtual paths (e.g. /home/<workspace>/...),
	//      which are translated to real paths before execution. Output is sanitized to
	//      hide real filesystem paths from the LLM.
	//   2. OS-level enforcement: when a Sandbox is configured, commands run under
	//      Seatbelt (macOS) or Landlock (Linux), which restrict filesystem writes and
	//      network access at the kernel level. This prevents the LLM from bypassing
	//      path restrictions via shell commands.

	cmdStr := a.Command

	// Check blocked commands via Sandbox.IsBlockedCommand.
	if t.sandbox.IsBlockedCommand(cmdStr) {
		return "", fmt.Errorf("command is blocked by sandbox policy")
	}

	// Apply the app-layer network blacklist unless the sandbox explicitly allows
	// network access for shell_exec. web_fetch has its own network policy and is
	// not governed by this command blacklist.
	needAppBlacklist := t.sandbox == nil || !t.sandbox.AllowsNetworkTool("shell_exec")
	if needAppBlacklist {
		if err := checkNetworkCommand(cmdStr); err != nil {
			return "", err
		}
	}
	cmdDir, err := resolveShellExecDir(t.sandbox, a.Dir)
	if err != nil {
		return "", err
	}

	// Validate write targets are not in readonly paths.
	if err := validateWritePaths(t.sandbox, cmdStr, cmdDir); err != nil {
		return "", err
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

	var out []byte
	if t.launchFunc != nil {
		req := &sandbox.LaunchRequest{
			Shell: shell, ShellFlag: shellFlag,
			Command: a.Command, Dir: cmdDir,
		}
		if t.sandbox != nil {
			cfg := t.sandbox.CloneConfig()
			req.Config = &cfg
		}
		out, err = t.launchFunc(ctx, req)
	} else if t.sandbox != nil {
		out, err = t.sandbox.Launch(ctx, shell, shellFlag, a.Command, cmdDir)
	} else {
		out, err = sandbox.NewLauncher().Launch(ctx, &sandbox.LaunchRequest{
			Shell: shell, ShellFlag: shellFlag,
			Command: a.Command, Dir: cmdDir,
		})
	}
	output := string(out)

	// Rewrite real filesystem paths in output back to virtual paths for LLM.
	output = t.sandbox.RewriteOutput(output)

	if ctx.Err() == context.DeadlineExceeded {
		return output, fmt.Errorf("shell command timed out after %s", t.timeout)
	}
	if err != nil {
		return output, fmt.Errorf("%s", t.sandbox.RewriteOutput(err.Error()))
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
	tree, err := sandbox.ParseShellTree(cmdStr)
	if err != nil {
		return false
	}
	for _, node := range tree.AllCmds() {
		if node.Cmd == nc {
			return true
		}
	}
	return false
}

func resolveShellExecDir(sb *sandbox.Sandbox, dir string) (string, error) {
	if dir != "" {
		if sb != nil && sb.Active() {
			// Sandbox mode: Sandbox.Resolve already resolved and validated
			// dir to a real absolute path.
			return dir, nil
		}
		workdir := ""
		if sb != nil {
			workdir = sb.Root()
		}
		if workdir != "" {
			// Non-sandbox mode: validate that dir is within workdir boundaries.
			originalDir := dir
			absWorkdir, err := filepath.Abs(workdir)
			if err != nil {
				return "", fmt.Errorf("resolve workdir: %w", err)
			}
			absDir := absWorkdir
			if filepath.IsAbs(dir) {
				absDir = dir
			} else {
				absDir = filepath.Join(absWorkdir, dir)
				if absDir, err = filepath.Abs(absDir); err != nil {
					return "", fmt.Errorf("resolve dir: %w", err)
				}
			}
			absDir = sandbox.EvalSymlinks(absDir)
			absWorkdir = sandbox.EvalSymlinks(absWorkdir)
			if !sandbox.IsSubpath(absDir, absWorkdir) {
				return "", fmt.Errorf("dir %q is outside workspace boundaries", originalDir)
			}
			return absDir, nil
		}
		return dir, nil
	}
	if sb != nil {
		return sb.Root(), nil
	}
	return "", nil
}

func hasActiveWritePolicy(sb *sandbox.Sandbox) bool {
	return sb.HasWritePolicy()
}

type shellWriteTarget struct {
	path   string
	append bool
}

func isShellSpace(ch byte) bool {
	switch ch {
	case ' ', '\t', '\n', '\r':
		return true
	default:
		return false
	}
}

func shellWordBackslashEscapes() bool {
	return runtime.GOOS != "windows"
}

func nextShellWordWithBackslashEscapes(segment string, start int, backslashEscapes bool) (string, int) {
	var builder strings.Builder
	quote := byte(0)
	escaped := false

	for i := start; i < len(segment); i++ {
		ch := segment[i]
		if quote == 0 && !escaped && isShellSpace(ch) {
			return builder.String(), i
		}
		if escaped {
			builder.WriteByte(ch)
			escaped = false
			continue
		}
		switch ch {
		case '\\':
			if backslashEscapes {
				escaped = true
				continue
			}
			builder.WriteByte(ch)
		case '\'', '"':
			if quote == 0 {
				quote = ch
			} else if quote == ch {
				quote = 0
			} else {
				builder.WriteByte(ch)
			}
		default:
			builder.WriteByte(ch)
		}
	}
	if escaped {
		builder.WriteByte('\\')
	}
	return builder.String(), len(segment)
}

func nextShellWord(segment string, start int) (string, int) {
	return nextShellWordWithBackslashEscapes(segment, start, shellWordBackslashEscapes())
}

func splitShellWordsWithBackslashEscapes(segment string, backslashEscapes bool) []string {
	words := make([]string, 0)
	for i := 0; i < len(segment); {
		for i < len(segment) && isShellSpace(segment[i]) {
			i++
		}
		if i >= len(segment) {
			break
		}
		word, next := nextShellWordWithBackslashEscapes(segment, i, backslashEscapes)
		if word == "" && next <= i {
			break
		}
		if word != "" {
			words = append(words, word)
		}
		i = next
	}
	return words
}

func splitShellWords(segment string) []string {
	return splitShellWordsWithBackslashEscapes(segment, shellWordBackslashEscapes())
}

func extractTeeWriteTargetsWithBackslashEscapes(segment string, backslashEscapes bool) []shellWriteTarget {
	words := splitShellWordsWithBackslashEscapes(segment, backslashEscapes)
	if len(words) == 0 || filepath.Base(words[0]) != "tee" {
		return nil
	}

	appendMode := false
	parsingOptions := true
	targets := make([]shellWriteTarget, 0)
	for _, word := range words[1:] {
		if parsingOptions {
			switch {
			case word == "--":
				parsingOptions = false
				continue
			case strings.HasPrefix(word, "--"):
				if word == "--append" {
					appendMode = true
				}
				continue
			case strings.HasPrefix(word, "-") && word != "-":
				if strings.Contains(word[1:], "a") {
					appendMode = true
				}
				continue
			default:
				parsingOptions = false
			}
		}
		targets = append(targets, shellWriteTarget{path: word, append: appendMode})
	}
	return targets
}

func extractTeeWriteTargets(segment string) []shellWriteTarget {
	return extractTeeWriteTargetsWithBackslashEscapes(segment, shellWordBackslashEscapes())
}

func extractRedirectWriteTargetsWithBackslashEscapes(segment string, backslashEscapes bool) []shellWriteTarget {
	targets := make([]shellWriteTarget, 0)
	quote := byte(0)
	escaped := false

	for i := 0; i < len(segment); i++ {
		ch := segment[i]
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if backslashEscapes && ch == '\\' && quote == '"' {
				escaped = true
				continue
			}
			if ch == quote {
				quote = 0
			}
			continue
		}
		if escaped {
			escaped = false
			continue
		}
		switch ch {
		case '\\':
			if backslashEscapes {
				escaped = true
			}
			continue
		case '\'', '"':
			quote = ch
			continue
		case '>':
		default:
			continue
		}

		appendMode := false
		if i+1 < len(segment) {
			switch segment[i+1] {
			case '>':
				appendMode = true
				i++
			case '|':
				i++
			}
		}

		start := i + 1
		for start < len(segment) && isShellSpace(segment[start]) {
			start++
		}
		if start >= len(segment) || segment[start] == '&' || segment[start] == '>' || segment[start] == '<' {
			continue
		}

		path, next := nextShellWordWithBackslashEscapes(segment, start, backslashEscapes)
		if path == "" {
			continue
		}
		targets = append(targets, shellWriteTarget{path: path, append: appendMode})
		i = next - 1
	}

	return targets
}

func extractRedirectWriteTargets(segment string) []shellWriteTarget {
	return extractRedirectWriteTargetsWithBackslashEscapes(segment, shellWordBackslashEscapes())
}

func resolveWriteTargetPath(path, commandDir string) (string, error) {
	resolvedPath := path
	if !filepath.IsAbs(resolvedPath) {
		baseDir := commandDir
		if baseDir == "" {
			var err error
			baseDir, err = os.Getwd()
			if err != nil {
				return "", err
			}
		}
		resolvedPath = filepath.Join(baseDir, resolvedPath)
	}
	return filepath.Abs(resolvedPath)
}

func shellWriteValidationSegments(cmd string) []string {
	const clobberPlaceholder = "__COBOT_SHELL_CLOBBER__"
	replacer := strings.NewReplacer(
		"\r\n", "\n",
		">|", clobberPlaceholder,
		"&&", "\n",
		"||", "\n",
		"&", "\n",
		";", "\n",
		"|", "\n",
		"$(", "\n",
		"`", "\n",
	)
	segments := strings.Split(replacer.Replace(cmd), "\n")
	for i := range segments {
		segments[i] = strings.ReplaceAll(segments[i], clobberPlaceholder, ">|")
	}
	return segments
}

// validateWritePaths checks that any file paths the command writes to are not readonly.
func validateWritePaths(sb *sandbox.Sandbox, cmdStr string, commandDir ...string) error {
	if !hasActiveWritePolicy(sb) {
		return nil
	}

	resolvedCommandDir := ""
	if len(commandDir) > 0 {
		resolvedCommandDir = commandDir[0]
	}

	for _, segment := range shellWriteValidationSegments(cmdStr) {
		trimmed := strings.TrimSpace(segment)
		if trimmed == "" {
			continue
		}

		writeTargets := append(extractTeeWriteTargets(trimmed), extractRedirectWriteTargets(trimmed)...)
		for _, target := range writeTargets {
			absPath, err := resolveWriteTargetPath(target.path, resolvedCommandDir)
			if err != nil {
				continue
			}
			if !sb.IsWriteAllowed(absPath) {
				op := "write"
				if target.append {
					op = "append"
				}
				return fmt.Errorf("%s target %q is readonly or outside sandbox", op, target.path)
			}
		}
	}
	return nil
}

var _ cobot.Tool = (*ShellExecTool)(nil)
