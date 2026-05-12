package sandbox

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"gopkg.in/yaml.v3"
)

type rewrittenError struct {
	message string
	cause   error
}

func (e *rewrittenError) Error() string { return e.message }

func (e *rewrittenError) Unwrap() error { return e.cause }

func normalizePath(path string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(EvalSymlinks(absPath)), nil
}

// trimPrefixFold removes prefix from s using case-insensitive matching.
// This is needed on Windows where drive letters and path segments may differ in case.
func trimPrefixFold(s, prefix string) string {
	if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
		return s[len(prefix):]
	}
	return s
}

func pathMatchesRoot(path, root, sep string) bool {
	if path == root || strings.HasPrefix(path, root+sep) {
		return true
	}
	if sep != `\` {
		return false
	}
	pathLower := strings.ToLower(path)
	rootLower := strings.ToLower(root)
	return pathLower == rootLower || strings.HasPrefix(pathLower, rootLower+sep)
}

type SandboxConfig struct {
	VirtualRoot         string   `yaml:"virtual_root,omitempty"`
	Root                string   `yaml:"root"`
	AllowPaths          []string `yaml:"allow_paths,omitempty"`
	ReadonlyPaths       []string `yaml:"readonly_paths,omitempty"`
	AllowNetwork        bool     `yaml:"allow_network"`
	AllowedNetworkTools []string `yaml:"allowed_network_tools,omitempty"`
	BlockedCommands     []string `yaml:"blocked_commands,omitempty"`

	allowNetworkSet        bool `yaml:"-"`
	allowedNetworkToolsSet bool `yaml:"-"`
}

func cloneStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	return append([]string(nil), values...)
}

func (s SandboxConfig) Clone() SandboxConfig {
	cloned := s
	cloned.AllowPaths = cloneStrings(s.AllowPaths)
	cloned.ReadonlyPaths = cloneStrings(s.ReadonlyPaths)
	cloned.AllowedNetworkTools = cloneStrings(s.AllowedNetworkTools)
	cloned.BlockedCommands = cloneStrings(s.BlockedCommands)
	return cloned
}

func (s *SandboxConfig) SetAllowNetwork(allow bool) {
	if s == nil {
		return
	}
	s.AllowNetwork = allow
	s.allowNetworkSet = true
}

func (s *SandboxConfig) SetAllowedNetworkTools(tools []string) {
	if s == nil {
		return
	}
	s.AllowedNetworkTools = cloneStrings(tools)
	s.allowedNetworkToolsSet = true
}

func (s *SandboxConfig) HasAllowNetworkOverride() bool {
	return s != nil && s.allowNetworkSet
}

func (s *SandboxConfig) HasAllowedNetworkToolsOverride() bool {
	return s != nil && s.allowedNetworkToolsSet
}

func MergeConfigs(base, override *SandboxConfig) SandboxConfig {
	var merged SandboxConfig
	if base != nil {
		merged = base.Clone()
	}
	if override == nil {
		return merged
	}
	if override.Root != "" {
		merged.Root = override.Root
	}
	if override.VirtualRoot != "" {
		merged.VirtualRoot = override.VirtualRoot
	}
	if len(override.AllowPaths) > 0 {
		merged.AllowPaths = cloneStrings(override.AllowPaths)
	}
	if len(override.ReadonlyPaths) > 0 {
		merged.ReadonlyPaths = cloneStrings(override.ReadonlyPaths)
	}
	if override.HasAllowedNetworkToolsOverride() {
		merged.AllowedNetworkTools = cloneStrings(override.AllowedNetworkTools)
		merged.allowedNetworkToolsSet = true
	}
	if len(override.BlockedCommands) > 0 {
		merged.BlockedCommands = cloneStrings(override.BlockedCommands)
	}
	if override.HasAllowNetworkOverride() {
		merged.SetAllowNetwork(override.AllowNetwork)
	}
	return merged
}

func (s *SandboxConfig) UnmarshalYAML(value *yaml.Node) error {
	type raw SandboxConfig
	var decoded raw
	if err := value.Decode(&decoded); err != nil {
		return err
	}
	*s = SandboxConfig(decoded)
	s.allowNetworkSet = yamlMappingHasKey(value, "allow_network")
	s.allowedNetworkToolsSet = yamlMappingHasKey(value, "allowed_network_tools")
	return nil
}

func (s SandboxConfig) MarshalYAML() (any, error) {
	type raw struct {
		VirtualRoot         string    `yaml:"virtual_root,omitempty"`
		Root                string    `yaml:"root"`
		AllowPaths          []string  `yaml:"allow_paths,omitempty"`
		ReadonlyPaths       []string  `yaml:"readonly_paths,omitempty"`
		AllowNetwork        *bool     `yaml:"allow_network,omitempty"`
		AllowedNetworkTools *[]string `yaml:"allowed_network_tools,omitempty"`
		BlockedCommands     []string  `yaml:"blocked_commands,omitempty"`
	}
	encoded := raw{
		VirtualRoot:     s.VirtualRoot,
		Root:            s.Root,
		AllowPaths:      cloneStrings(s.AllowPaths),
		ReadonlyPaths:   cloneStrings(s.ReadonlyPaths),
		BlockedCommands: cloneStrings(s.BlockedCommands),
	}
	if s.allowedNetworkToolsSet || len(s.AllowedNetworkTools) > 0 {
		tools := cloneStrings(s.AllowedNetworkTools)
		encoded.AllowedNetworkTools = &tools
	}
	if s.AllowNetwork || s.allowNetworkSet {
		allow := s.AllowNetwork
		encoded.AllowNetwork = &allow
	}
	return encoded, nil
}

func yamlMappingHasKey(node *yaml.Node, key string) bool {
	if node == nil || node.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return true
		}
	}
	return false
}

// IsEmpty reports whether the sandbox config has no restrictions configured.
func (s *SandboxConfig) IsEmpty() bool {
	return s == nil || (s.Root == "" && len(s.AllowPaths) == 0 && len(s.ReadonlyPaths) == 0)
}

func (s *SandboxConfig) IsAllowed(path string, write bool) bool {
	if s.IsEmpty() {
		return true
	}
	if path == "/dev/null" {
		return true
	}

	absPath, err := normalizePath(path)
	if err != nil {
		slog.Debug("sandbox: IsAllowed normalizePath failed", "path", path, "write", write, "error", err)
		return false
	}

	readonlyMatched := false
	for _, rp := range s.ReadonlyPaths {
		absRP, err := normalizePath(rp)
		if err != nil {
			continue
		}
		if IsSubpath(absPath, absRP) {
			readonlyMatched = true
			if write {
				slog.Debug("sandbox: IsAllowed denied — readonly path", "path", absPath, "readonly", absRP)
				return false
			}
		}
	}
	if readonlyMatched && !write {
		return true
	}

	for _, ap := range s.AllowPaths {
		absAP, err := normalizePath(ap)
		if err != nil {
			continue
		}
		if IsSubpath(absPath, absAP) {
			return true
		}
	}

	if s.Root != "" {
		absRoot, err := normalizePath(s.Root)
		if err != nil {
			slog.Debug("sandbox: IsAllowed normalizePath(root) failed", "root", s.Root, "error", err)
			return false
		}
		if IsSubpath(absPath, absRoot) {
			if readonlyMatched && write {
				slog.Debug("sandbox: IsAllowed denied — readonly match under root", "path", absPath, "root", absRoot)
				return false
			}
			return true
		}
	}

	resolvedRoot, _ := normalizePath(s.Root)
	slog.Debug("sandbox: IsAllowed denied — path outside root", "path", absPath, "root", s.Root, "resolvedRoot", resolvedRoot, "write", write)
	return false
}

// AutoResolvePath resolves any path form (virtual, real, relative, absolute) into the sandbox.
// Path traversal (../) is blocked by ResolvePath's VirtualRoot prefix validation.
func (s *SandboxConfig) AutoResolvePath(path string) (string, error) {
	if s == nil || s.VirtualRoot == "" {
		return path, nil
	}

	nativePath := PathCleanVirtual(VirtualToNative(path))
	vr := PathCleanVirtual(s.VirtualRoot)
	sep := VirtualSeparator()

	if pathMatchesRoot(nativePath, vr, sep) {
		return s.ResolvePath(path)
	}

	if s.Root != "" {
		absRoot := filepath.Clean(s.Root)
		if pathMatchesRoot(nativePath, absRoot, string(filepath.Separator)) {
			rel := trimPrefixFold(nativePath, absRoot)
			if rel == "" || rel == string(filepath.Separator) {
				return s.ResolvePath(vr)
			}
			return s.ResolvePath(vr + VirtualToNative(rel))
		}
	}

	nativePathSlashes := filepath.ToSlash(nativePath)
	if filepath.IsAbs(nativePath) {
		if volume := filepath.VolumeName(nativePath); volume != "" {
			nativePathSlashes = strings.TrimPrefix(nativePathSlashes, filepath.ToSlash(volume))
			if nativePathSlashes == "" {
				nativePathSlashes = "/"
			}
		}
		if !strings.HasPrefix(nativePathSlashes, "/") {
			nativePathSlashes = "/" + strings.TrimLeft(nativePathSlashes, "/")
		}
		virtualPath := vr + VirtualToNative(nativePathSlashes)
		return s.ResolvePath(virtualPath)
	}

	virtualPath := vr + sep + VirtualToNative(nativePathSlashes)
	return s.ResolvePath(virtualPath)
}

// ResolvePath validates that path starts with VirtualRoot and translates it to the real filesystem path.
func (s *SandboxConfig) ResolvePath(path string) (string, error) {
	if s == nil || s.VirtualRoot == "" {
		return path, nil
	}

	cleaned := PathCleanVirtual(VirtualToNative(path))
	vr := PathCleanVirtual(s.VirtualRoot)
	sep := VirtualSeparator()

	if !pathMatchesRoot(cleaned, vr, sep) {
		return "", fmt.Errorf("path %q must start with %q (sandbox enforced)", path, s.VirtualRoot)
	}

	rel := trimPrefixFold(filepath.ToSlash(cleaned), filepath.ToSlash(vr))
	if rel == "" || rel == "/" {
		return s.Root, nil
	}
	return filepath.Join(s.Root, rel[1:]), nil
}

func (s *SandboxConfig) RewritePaths(text string) string {
	if s == nil || s.VirtualRoot == "" {
		return text
	}
	return strings.ReplaceAll(text, s.VirtualRoot, s.Root)
}

func (s *SandboxConfig) RewriteOutputPaths(text string) string {
	if s == nil || s.VirtualRoot == "" || s.Root == "" {
		return text
	}
	resolvedRoot := EvalSymlinks(s.Root)
	result := strings.ReplaceAll(text, resolvedRoot, s.VirtualRoot)
	if resolvedRoot != s.Root {
		result = strings.ReplaceAll(result, s.Root, s.VirtualRoot)
	}
	return result
}

func (s *SandboxConfig) RewriteError(err error) error {
	if s == nil || s.VirtualRoot == "" || err == nil {
		return err
	}
	return &rewrittenError{message: s.RewriteOutputPaths(err.Error()), cause: err}
}

func (s *SandboxConfig) RealToVirtual(realPath string) string {
	if s == nil || s.VirtualRoot == "" || s.Root == "" {
		return realPath
	}
	absPath, err := filepath.Abs(realPath)
	if err != nil {
		return PathJoinVirtual(s.VirtualRoot, "[external]", filepath.Base(realPath))
	}
	absPath = filepath.Clean(absPath)
	absRoot, err := filepath.Abs(s.Root)
	if err != nil {
		return PathJoinVirtual(s.VirtualRoot, "[external]", filepath.Base(realPath))
	}
	absRootSep := absRoot + string(filepath.Separator)
	if strings.EqualFold(absPath, absRoot) {
		return s.VirtualRoot
	}
	if pathMatchesRoot(absPath, absRoot, string(filepath.Separator)) {
		rel := filepath.ToSlash(trimPrefixFold(absPath, absRootSep))
		return PathJoinVirtual(s.VirtualRoot, VirtualToNative(rel))
	}
	return PathJoinVirtual(s.VirtualRoot, "[external]", filepath.Base(absPath))
}

func (s *SandboxConfig) ValidatePath(resolvedPath string) error {
	if s.IsEmpty() {
		return nil
	}
	if s.IsAllowed(resolvedPath, false) {
		return nil
	}
	return fmt.Errorf("path %q is outside sandbox policy", resolvedPath)
}

// IsBlockedCommand checks if a command is blocked by sandbox policy.
// It uses proper shell parsing to correctly handle quoted strings and
// command substitutions. Commands inside $(...) or `...` are statically
// detected and checked against BlockedCommands (e.g. $(curl localhost) is blocked
// if "curl" is in BlockedCommands).
func (s *SandboxConfig) IsBlockedCommand(cmd string) bool {
	tree, err := ParseShellTree(cmd)
	if err != nil {
		// On parse error, fall back to naive parsing.
		return s.isBlockedCommandNaive(cmd)
	}
	return tree.IsBlockedBy(s.BlockedCommands)
}

// isBlockedCommandNaive is the original naive implementation for fallback.
func (s *SandboxConfig) isBlockedCommandNaive(cmd string) bool {
	for _, segment := range ShellCommandSegments(cmd) {
		trimmed := strings.TrimSpace(segment)
		if trimmed == "" {
			continue
		}

		fields := strings.Fields(trimmed)
		baseCmd := ""
		if len(fields) > 0 {
			baseCmd = filepath.Base(fields[0])
		}

		for _, blocked := range s.BlockedCommands {
			if baseCmd == blocked || trimmed == blocked || strings.HasPrefix(trimmed, blocked+" ") || strings.HasPrefix(trimmed, blocked+"\t") {
				return true
			}
			if (strings.Contains(blocked, " ") || strings.Contains(blocked, "=")) && strings.HasPrefix(trimmed, blocked) {
				return true
			}
		}
	}
	return false
}

// SandboxedCmd wraps an exec.Cmd with OS-level sandbox enforcement.
// Use Start() to launch the process (not cmd.Start() directly) so that
// sandbox cleanup runs if startup fails. Use Wait() to wait for
// process exit and perform sandbox cleanup. If you decide not to start
// the process, call Cleanup() to release sandbox resources.
//
// Safe to use even when no sandbox is active (cleanup will be nil).
type SandboxedCmd struct {
	Cmd     *exec.Cmd
	cleanup func()
	started bool
}

// Start launches the wrapped command, ensuring sandbox cleanup runs if startup fails.
// Callers should prefer Start() over calling Cmd.Start() directly.
func (s *SandboxedCmd) Start() error {
	if s == nil || s.Cmd == nil {
		return fmt.Errorf("sandbox: nil command")
	}
	if err := s.Cmd.Start(); err != nil {
		s.Cleanup()
		return err
	}
	s.started = true
	return nil
}

// Cleanup releases any sandbox resources associated with this command.
// It is safe to call Cleanup multiple times (only the first call executes it).
func (s *SandboxedCmd) Cleanup() {
	if s == nil || s.cleanup == nil {
		return
	}
	cleanup := s.cleanup
	s.cleanup = nil
	cleanup()
}

// Wait waits for the process to exit and performs sandbox cleanup.
// The cleanup func runs only on the first call to Wait.
func (s *SandboxedCmd) Wait() error {
	if s == nil || s.Cmd == nil {
		return fmt.Errorf("sandbox: nil command")
	}
	err := s.Cmd.Wait()
	s.Cleanup()
	return err
}

// ---------------------------------------------------------------------------

// Sandbox provides unified virtual path translation and OS-level command isolation.
// All tools (filesystem_read, filesystem_write, shell_exec, etc.) use Sandbox
// for path resolution, validation, command rewriting, and output sanitization,
// ensuring consistent behavior between filesystem operations and shell commands.
//
// The virtual path layer works at the upper level:
//   - LLM always sees virtual paths (e.g. /home/<workspace>/...)
//   - Resolve() translates virtual → real for local execution
//   - RewriteOutput() translates real → virtual for LLM consumption
//   - Launch() executes commands with OS-level enforcement (Seatbelt/Landlock)
type Sandbox struct {
	config   SandboxConfig
	launcher *Launcher
}

// NewSandbox creates a Sandbox with the given configuration.
func NewSandbox(config SandboxConfig) *Sandbox {
	return &Sandbox{
		config:   config,
		launcher: NewLauncher(),
	}
}

// CloneConfig returns a deep copy of the sandbox configuration.
func (s *Sandbox) CloneConfig() SandboxConfig {
	if s == nil {
		return SandboxConfig{}
	}
	return s.config.Clone()
}

// Active reports whether virtual path sandboxing is enabled.
// When active, all virtual↔real path translations are performed and real paths
// are never exposed to the LLM.
func (s *Sandbox) Active() bool {
	return s != nil && s.config.VirtualRoot != ""
}

// Root returns the sandbox root directory (real filesystem path).
func (s *Sandbox) Root() string {
	if s == nil {
		return ""
	}
	return s.config.Root
}

// VirtualRoot returns the virtual root path shown to the LLM.
func (s *Sandbox) VirtualRoot() string {
	if s == nil {
		return ""
	}
	return s.config.VirtualRoot
}

// AllowNetwork reports whether network access is permitted.
func (s *Sandbox) AllowNetwork() bool {
	return s != nil && s.config.AllowNetwork
}

// AllowsNetworkTool reports whether the given tool may access the network.
// allow_network is the global kill switch; when disabled, no tool may use network.
// When enabled, web_fetch is allowed by default and other tools require an explicit
// allowlist entry in allowed_network_tools.
func (s *Sandbox) AllowsNetworkTool(tool string) bool {
	if s == nil || tool == "" || !s.config.AllowNetwork {
		return false
	}
	if len(s.config.AllowedNetworkTools) == 0 {
		return tool == "web_fetch"
	}
	for _, allowed := range s.config.AllowedNetworkTools {
		if allowed == tool {
			return true
		}
	}
	return false
}

// HasWritePolicy reports whether the sandbox has any write restrictions configured.
func (s *Sandbox) HasWritePolicy() bool {
	return s != nil && (s.config.Root != "" || len(s.config.AllowPaths) > 0 || len(s.config.ReadonlyPaths) > 0)
}

// HasOSLevelEnforcement reports whether the current platform provides OS-level
// sandbox enforcement for filesystem isolation (Landlock on Linux, Seatbelt on
// macOS, Restricted Token on Windows).
func (s *Sandbox) HasOSLevelEnforcement() bool {
	if s == nil {
		return false
	}
	return runtime.GOOS == "linux" || runtime.GOOS == "darwin" || runtime.GOOS == "windows"
}

// HasNetworkIsolation reports whether the OS-level sandbox can enforce network
// restrictions at the kernel level (Landlock on Linux, Seatbelt on macOS).
// Windows Restricted Token only provides filesystem isolation, so the
// application-layer network command blacklist is still needed on Windows.
func (s *Sandbox) HasNetworkIsolation() bool {
	if s == nil {
		return false
	}
	return runtime.GOOS == "linux" || runtime.GOOS == "darwin"
}

// Resolve translates a path (virtual, relative, or real) to a real filesystem path,
// validates it is within sandbox boundaries, and optionally checks write permissions.
// This is the single entry point for all path resolution in tools.
func (s *Sandbox) Resolve(path string, write bool) (string, error) {
	if s == nil {
		return path, nil
	}
	originalPath := path
	resolved, err := s.config.AutoResolvePath(path)
	if err != nil {
		slog.Debug("sandbox: AutoResolvePath failed", "path", originalPath, "write", write, "virtualRoot", s.config.VirtualRoot, "root", s.config.Root, "error", err)
		return "", err
	}
	if err := s.config.ValidatePath(resolved); err != nil {
		slog.Debug("sandbox: ValidatePath failed", "path", originalPath, "resolved", resolved, "write", write, "root", s.config.Root, "error", err)
		return "", fmt.Errorf("path %q is outside allowed workspace paths", originalPath)
	}
	if write && !s.config.IsAllowed(resolved, true) {
		slog.Debug("sandbox: IsAllowed denied write", "path", originalPath, "resolved", resolved, "root", s.config.Root, "virtualRoot", s.config.VirtualRoot, "readonlyPaths", s.config.ReadonlyPaths, "allowPaths", s.config.AllowPaths)
		return "", fmt.Errorf("path %q is readonly or blocked by sandbox policy", originalPath)
	}
	return resolved, nil
}

// RewriteCommand rewrites virtual paths in a command string to real paths.
// Used by shell_exec to translate LLM-visible paths before execution.
func (s *Sandbox) RewriteCommand(cmd string) string {
	if s == nil {
		return cmd
	}
	return s.config.RewritePaths(cmd)
}

// RewriteOutput rewrites real filesystem paths in output back to virtual paths.
// All tool output visible to the LLM must go through this.
func (s *Sandbox) RewriteOutput(output string) string {
	if s == nil {
		return output
	}
	return s.config.RewriteOutputPaths(output)
}

// RewriteError rewrites real filesystem paths in an error back to virtual paths.
func (s *Sandbox) RewriteError(err error) error {
	if s == nil || err == nil {
		return err
	}
	return s.config.RewriteError(err)
}

// VirtualPath converts a real filesystem path to a virtual path for LLM output.
func (s *Sandbox) VirtualPath(realPath string) string {
	if s == nil {
		return realPath
	}
	return s.config.RealToVirtual(realPath)
}

// IsBlockedCommand checks if a command is blocked by sandbox policy.
func (s *Sandbox) IsBlockedCommand(cmd string) bool {
	if s == nil {
		return false
	}
	return s.config.IsBlockedCommand(cmd)
}

// IsWriteAllowed checks if writing to the given real path is permitted.
// Used by shell_exec to validate redirect/tee targets after path resolution.
func (s *Sandbox) IsWriteAllowed(realPath string) bool {
	if s == nil {
		return true
	}
	return s.config.IsAllowed(realPath, true)
}

// Launch executes a command with OS-level sandbox enforcement.
// On macOS this uses Seatbelt (sandbox-exec); on Linux this uses Landlock.
// Paths in the command should already be rewritten to real paths via RewriteCommand.
func (s *Sandbox) Launch(ctx context.Context, shell, shellFlag, command, dir string) ([]byte, error) {
	if s == nil {
		return nil, fmt.Errorf("sandbox: cannot launch without configuration")
	}
	cfg := s.config.Clone()
	return s.launcher.Launch(ctx, &LaunchRequest{
		Shell:     shell,
		ShellFlag: shellFlag,
		Command:   command,
		Dir:       dir,
		Config:    &cfg,
	})
}

// LaunchProcess starts a long-running process with OS-level sandbox enforcement.
// Use the returned SandboxedCmd's Start() to launch and Wait() to wait for exit.
// If you decide not to start, call Cleanup() to release sandbox resources.
// On macOS this uses Seatbelt (sandbox-exec); on Linux this uses Landlock.
func (s *Sandbox) LaunchProcess(ctx context.Context, command string, args []string, dir string) (*SandboxedCmd, error) {
	if s == nil {
		return nil, fmt.Errorf("sandbox: cannot launch without configuration")
	}
	cmd, cleanup, err := launchProcessWithSandbox(ctx, command, args, dir, &s.config)
	if err != nil {
		if cleanup != nil {
			cleanup()
		}
		return nil, err
	}
	return &SandboxedCmd{Cmd: cmd, cleanup: cleanup}, nil
}

// Describe returns a description suffix explaining the active sandbox to the LLM.
// Tools should append this to their base description so the LLM knows to use virtual paths.
func (s *Sandbox) Describe(baseDesc string) string {
	if s == nil || s.config.VirtualRoot == "" {
		return baseDesc
	}
	return baseDesc + fmt.Sprintf(` Sandbox is active. All file paths are automatically resolved under "%s" — provide paths starting with "%s" for best results. Relative paths and other absolute paths are auto-mapped into the sandbox.`, s.config.VirtualRoot, s.config.VirtualRoot)
}
