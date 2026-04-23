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
	workdir  string
	config   *sandbox.SandboxConfig
	launcher *sandbox.Launcher
	timeout  time.Duration
}

type ShellExecToolOption func(*ShellExecTool)

func WithShellWorkdir(workdir string) ShellExecToolOption {
	return func(t *ShellExecTool) { t.workdir = workdir }
}

func WithShellSandboxConfig(config *sandbox.SandboxConfig) ShellExecToolOption {
	return func(t *ShellExecTool) { t.config = config }
}

func WithShellLauncher(launcher *sandbox.Launcher) ShellExecToolOption {
	return func(t *ShellExecTool) { t.launcher = launcher }
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
	// Only create a sandbox-aware launcher if the user didn't supply one explicitly.
	// WithShellLauncher takes precedence so tests can inject a stub backend.
	if t.launcher == nil {
		if t.config != nil {
			t.launcher = sandbox.NewLauncher(sandbox.WithSandboxConfig(t.config))
		} else {
			t.launcher = sandbox.NewLauncher()
		}
	}
	return t
}

func (t *ShellExecTool) Name() string {
	return "shell_exec"
}

func (t *ShellExecTool) Description() string {
	desc := "Execute a shell command and return its output."
	if t.config != nil && t.config.VirtualRoot != "" {
		desc += fmt.Sprintf(` Sandbox is active. All file paths are automatically resolved under "%s" — provide paths starting with "%s" for best results. Relative paths and other absolute paths are auto-mapped into the sandbox.`, t.config.VirtualRoot, t.config.VirtualRoot)
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
	cmdDir, err := resolveShellExecDir(t.config, t.workdir, a.Dir)
	if err != nil {
		return "", err
	}

	// Validate write targets are not in readonly paths.
	if t.config != nil {
		if err := validateWritePaths(t.config, cmdStr, cmdDir); err != nil {
			return "", err
		}
	}

	if t.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t.timeout)
		defer cancel()
	}
	if t.launcher == nil {
		t.launcher = sandbox.NewLauncher()
	}
	shell, shellFlag := "sh", "-c"
	if runtime.GOOS == "windows" {
		shell, shellFlag = "cmd", "/C"
	}
	var launchConfig *sandbox.SandboxConfig
	if t.config != nil {
		cfg := t.config.Clone()
		launchConfig = &cfg
	}
	out, err := t.launcher.Launch(ctx, &sandbox.LaunchRequest{
		Shell:     shell,
		ShellFlag: shellFlag,
		Command:   a.Command,
		Dir:       cmdDir,
		Config:    launchConfig,
	})
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

func resolveShellExecDir(cfg *sandbox.SandboxConfig, workdir, dir string) (string, error) {
	if dir != "" {
		if cfg != nil && cfg.VirtualRoot != "" {
			// Sandbox mode: sandboxResolvePath already resolved and validated
			// dir to a real absolute path via AutoResolvePath + ValidatePath.
			return dir, nil
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
	if workdir != "" {
		return workdir, nil
	}
	return "", nil
}

func hasActiveWritePolicy(cfg *sandbox.SandboxConfig) bool {
	return cfg != nil && (cfg.Root != "" || len(cfg.AllowPaths) > 0 || len(cfg.ReadonlyPaths) > 0)
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
func validateWritePaths(cfg *sandbox.SandboxConfig, cmdStr string, commandDir ...string) error {
	if !hasActiveWritePolicy(cfg) {
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
			if !cfg.IsAllowed(absPath, true) {
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
