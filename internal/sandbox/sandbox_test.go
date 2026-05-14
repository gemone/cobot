package sandbox

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSandboxConfig_IsAllowed(t *testing.T) {
	root := t.TempDir()
	allowedDir := filepath.Join(root, "allowed")
	readonlyDir := filepath.Join(root, "readonly")
	os.MkdirAll(allowedDir, 0755)
	os.MkdirAll(readonlyDir, 0755)

	s := &SandboxConfig{
		Root:          root,
		AllowPaths:    []string{allowedDir},
		ReadonlyPaths: []string{readonlyDir},
	}

	allowedFile := filepath.Join(allowedDir, "file.txt")
	readonlyFile := filepath.Join(readonlyDir, "file.txt")
	rootFile := filepath.Join(root, "file.txt")
	outsideFile := filepath.Join(os.TempDir(), "outside.txt")

	if !s.IsAllowed(allowedFile, false) {
		t.Error("allowed path should be readable")
	}
	if !s.IsAllowed(allowedFile, true) {
		t.Error("allowed path should be writable")
	}
	if !s.IsAllowed(readonlyFile, false) {
		t.Error("readonly path should be readable")
	}
	if s.IsAllowed(readonlyFile, true) {
		t.Error("readonly path should not be writable")
	}
	if !s.IsAllowed(rootFile, false) {
		t.Error("root path should be readable")
	}
	if !s.IsAllowed(rootFile, true) {
		t.Error("root path should be writable")
	}
	if s.IsAllowed(outsideFile, false) {
		t.Error("path outside root should not be allowed")
	}
}

func TestSandboxConfig_IsAllowed_DevNull(t *testing.T) {
	root := t.TempDir()
	s := &SandboxConfig{Root: root}

	if !s.IsAllowed("/dev/null", false) {
		t.Error("/dev/null should be readable")
	}
	if !s.IsAllowed("/dev/null", true) {
		t.Error("/dev/null should be writable")
	}
}

func TestSandboxConfig_IsBlockedCommand(t *testing.T) {
	s := &SandboxConfig{BlockedCommands: []string{"rm -rf", "format", "dd if="}}

	if !s.IsBlockedCommand("rm -rf /") {
		t.Error("should block rm -rf")
	}
	if !s.IsBlockedCommand("format C:") {
		t.Error("should block format")
	}
	if s.IsBlockedCommand("ls -la") {
		t.Error("should not block ls")
	}
	if !s.IsBlockedCommand("dd if=/dev/zero of=/dev/sda") {
		t.Error("should block dd")
	}
	if !s.IsBlockedCommand("true&&rm -rf /") {
		t.Error("should block rm -rf after &&")
	}
	if !s.IsBlockedCommand("echo ok\nformat C:") {
		t.Error("should block format after newline")
	}
}

func TestSandboxConfig_ResolvePath_NoSandbox(t *testing.T) {
	var s *SandboxConfig
	path, err := s.ResolvePath("/any/path")
	if err != nil {
		t.Fatal(err)
	}
	if path != "/any/path" {
		t.Errorf("expected /any/path, got %q", path)
	}
}

func TestSandboxConfig_ResolvePath_EmptyVirtualRoot(t *testing.T) {
	s := &SandboxConfig{Root: "/real"}
	path, err := s.ResolvePath("/any/path")
	if err != nil {
		t.Fatal(err)
	}
	if path != "/any/path" {
		t.Errorf("expected /any/path, got %q", path)
	}
}

func TestSandboxConfig_ResolvePath_ValidPath(t *testing.T) {
	vr := VirtualHome("ws")
	s := &SandboxConfig{VirtualRoot: vr, Root: "/tmp/real"}
	path, err := s.ResolvePath(PathJoinVirtual(vr, "src/main.go"))
	if err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join("/tmp/real", "src/main.go")
	if path != expected {
		t.Errorf("expected %q, got %q", expected, path)
	}
}

func TestSandboxConfig_ResolvePath_RootExactly(t *testing.T) {
	vr := VirtualHome("ws")
	s := &SandboxConfig{VirtualRoot: vr, Root: "/tmp/real"}
	path, err := s.ResolvePath(vr)
	if err != nil {
		t.Fatal(err)
	}
	if path != "/tmp/real" {
		t.Errorf("expected /tmp/real, got %q", path)
	}
}

func TestSandboxConfig_ResolvePath_TrailingSlash(t *testing.T) {
	vr := VirtualHome("ws")
	s := &SandboxConfig{VirtualRoot: vr, Root: "/tmp/real"}
	sep := VirtualSeparator()
	path, err := s.ResolvePath(vr + sep)
	if err != nil {
		t.Fatal(err)
	}
	if path != "/tmp/real" {
		t.Errorf("expected /tmp/real, got %q", path)
	}
}

func TestSandboxConfig_ResolvePath_Rejected(t *testing.T) {
	vr := VirtualHome("ws")
	s := &SandboxConfig{VirtualRoot: vr, Root: "/tmp/real"}
	_, err := s.ResolvePath("/etc/passwd")
	if err == nil {
		t.Error("expected error for path outside virtual root")
	}
}

func TestSandboxConfig_ResolvePath_RelativeRejected(t *testing.T) {
	vr := VirtualHome("ws")
	s := &SandboxConfig{VirtualRoot: vr, Root: "/tmp/real"}
	_, err := s.ResolvePath("src/main.go")
	if err == nil {
		t.Error("expected error for relative path")
	}
}

func TestSandboxConfig_ResolvePath_DotSlashRejected(t *testing.T) {
	vr := VirtualHome("ws")
	s := &SandboxConfig{VirtualRoot: vr, Root: "/tmp/real"}
	_, err := s.ResolvePath("./config.yaml")
	if err == nil {
		t.Error("expected error for dot-prefixed path")
	}
}

func TestSandboxConfig_RewritePaths_NilReceiver(t *testing.T) {
	var s *SandboxConfig
	got := s.RewritePaths("hello /home/ws/file.txt")
	if got != "hello /home/ws/file.txt" {
		t.Errorf("expected unchanged, got %q", got)
	}
}

func TestSandboxConfig_RewritePaths_EmptyVirtualRoot(t *testing.T) {
	s := &SandboxConfig{Root: "/tmp/real"}
	got := s.RewritePaths("hello /home/ws/file.txt")
	if got != "hello /home/ws/file.txt" {
		t.Errorf("expected unchanged, got %q", got)
	}
}

func TestSandboxConfig_RewritePaths_SinglePath(t *testing.T) {
	vr := VirtualHome("ws")
	root := t.TempDir()
	s := &SandboxConfig{VirtualRoot: vr, Root: root}
	input := "cat " + PathJoinVirtual(vr, "src/main.go")
	expected := "cat " + filepath.Join(root, "src/main.go")
	got := s.RewritePaths(input)
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestSandboxConfig_RewritePaths_MultiplePaths(t *testing.T) {
	vr := VirtualHome("ws")
	root := t.TempDir()
	s := &SandboxConfig{VirtualRoot: vr, Root: root}
	input := "cp " + PathJoinVirtual(vr, "a.txt") + " " + PathJoinVirtual(vr, "b.txt")
	expected := "cp " + filepath.Join(root, "a.txt") + " " + filepath.Join(root, "b.txt")
	got := s.RewritePaths(input)
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestSandboxConfig_RewritePaths_CommandString(t *testing.T) {
	vr := VirtualHome("myworkspace")
	root := t.TempDir()
	s := &SandboxConfig{VirtualRoot: vr, Root: root}
	input := "grep -r 'TODO' " + PathJoinVirtual(vr, "src") + " && echo done > " + PathJoinVirtual(vr, "out.log")
	expected := "grep -r 'TODO' " + filepath.Join(root, "src") + " && echo done > " + filepath.Join(root, "out.log")
	got := s.RewritePaths(input)
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestSandboxConfig_RewritePaths_NoMatch(t *testing.T) {
	vr := VirtualHome("ws")
	s := &SandboxConfig{VirtualRoot: vr, Root: "/tmp/real"}
	got := s.RewritePaths("ls -la /etc/passwd")
	if got != "ls -la /etc/passwd" {
		t.Errorf("expected unchanged, got %q", got)
	}
}

func TestSandboxConfig_RewriteError_SanitizesMessage(t *testing.T) {
	vr := VirtualHome("ws")
	root := t.TempDir()
	s := &SandboxConfig{VirtualRoot: vr, Root: root}
	secretPath := filepath.Join(root, "secret.txt")
	original := errors.New("open " + secretPath + ": permission denied")
	rewritten := s.RewriteError(original)

	if rewritten == nil {
		t.Fatal("expected rewritten error")
	}
	if !errors.Is(rewritten, original) {
		t.Fatal("rewritten error should preserve original cause")
	}
	if strings.Contains(rewritten.Error(), root) {
		t.Fatalf("rewritten error leaked real path: %q", rewritten.Error())
	}
	if !strings.Contains(rewritten.Error(), PathJoinVirtual(vr, "secret.txt")) {
		t.Fatalf("rewritten error should contain virtual path, got %q", rewritten.Error())
	}
}

func TestSandboxConfig_AutoResolvePath_NoSandbox(t *testing.T) {
	var s *SandboxConfig
	path, err := s.AutoResolvePath("/any/path")
	if err != nil {
		t.Fatal(err)
	}
	if path != "/any/path" {
		t.Errorf("expected /any/path, got %q", path)
	}
}

func TestSandboxConfig_AutoResolvePath_EmptyVirtualRoot(t *testing.T) {
	s := &SandboxConfig{Root: "/real"}
	path, err := s.AutoResolvePath("/any/path")
	if err != nil {
		t.Fatal(err)
	}
	if path != "/any/path" {
		t.Errorf("expected /any/path, got %q", path)
	}
}

func TestSandboxConfig_AutoResolvePath_VirtualRootPassthrough(t *testing.T) {
	vr := VirtualHome("ws")
	s := &SandboxConfig{VirtualRoot: vr, Root: "/tmp/real"}
	path, err := s.AutoResolvePath(PathJoinVirtual(vr, "src/main.go"))
	if err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join("/tmp/real", "src/main.go")
	if path != expected {
		t.Errorf("expected %q, got %q", expected, path)
	}
}

func TestSandboxConfig_AutoResolvePath_VirtualRootExactly(t *testing.T) {
	vr := VirtualHome("ws")
	s := &SandboxConfig{VirtualRoot: vr, Root: "/tmp/real"}
	path, err := s.AutoResolvePath(vr)
	if err != nil {
		t.Fatal(err)
	}
	if path != "/tmp/real" {
		t.Errorf("expected /tmp/real, got %q", path)
	}
}

func TestSandboxConfig_AutoResolvePath_RelativeAutoResolve(t *testing.T) {
	vr := VirtualHome("ws")
	s := &SandboxConfig{VirtualRoot: vr, Root: "/tmp/real"}
	path, err := s.AutoResolvePath("src/main.go")
	if err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join("/tmp/real", "src/main.go")
	if path != expected {
		t.Errorf("expected %q, got %q", expected, path)
	}
}

func TestSandboxConfig_AutoResolvePath_DotSlashAutoResolve(t *testing.T) {
	vr := VirtualHome("ws")
	s := &SandboxConfig{VirtualRoot: vr, Root: "/tmp/real"}
	path, err := s.AutoResolvePath("./config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join("/tmp/real", "config.yaml")
	if path != expected {
		t.Errorf("expected %q, got %q", expected, path)
	}
}

func TestSandboxConfig_AutoResolvePath_AbsoluteAutoResolve(t *testing.T) {
	vr := VirtualHome("ws")
	s := &SandboxConfig{VirtualRoot: vr, Root: "/tmp/real"}
	path, err := s.AutoResolvePath("/etc/passwd")
	if err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join("/tmp/real", "etc/passwd")
	if path != expected {
		t.Errorf("expected %q, got %q", expected, path)
	}
}

func TestSandboxConfig_AutoResolvePath_RealRootMatch(t *testing.T) {
	vr := VirtualHome("ws")
	s := &SandboxConfig{VirtualRoot: vr, Root: "/tmp/real"}
	path, err := s.AutoResolvePath("/tmp/real/src/main.go")
	if err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join("/tmp/real", "src/main.go")
	if path != expected {
		t.Errorf("expected %q, got %q", expected, path)
	}
}

func TestSandboxConfig_AutoResolvePath_RealRootExactly(t *testing.T) {
	vr := VirtualHome("ws")
	s := &SandboxConfig{VirtualRoot: vr, Root: "/tmp/real"}
	path, err := s.AutoResolvePath("/tmp/real")
	if err != nil {
		t.Fatal(err)
	}
	if path != "/tmp/real" {
		t.Errorf("expected /tmp/real, got %q", path)
	}
}

func TestSandboxConfig_AutoResolvePath_PathTraversalBlocked(t *testing.T) {
	vr := VirtualHome("ws")
	s := &SandboxConfig{VirtualRoot: vr, Root: "/tmp/real"}
	_, err := s.AutoResolvePath("../../../etc/passwd")
	if err == nil {
		t.Error("expected error for path traversal escaping VirtualRoot")
	}
}

func TestSandboxConfig_AutoResolvePath_TrailingSlashVirtualRoot(t *testing.T) {
	vr := VirtualHome("ws")
	s := &SandboxConfig{VirtualRoot: vr + VirtualSeparator(), Root: "/tmp/real"}
	path, err := s.AutoResolvePath("src/main.go")
	if err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join("/tmp/real", "src/main.go")
	if path != expected {
		t.Errorf("expected %q, got %q", expected, path)
	}
}

func TestSandboxConfig_RealToVirtual_PathUnderRoot(t *testing.T) {
	vr := VirtualHome("ws")
	root := t.TempDir()
	s := &SandboxConfig{VirtualRoot: vr, Root: root}
	got := s.RealToVirtual(filepath.Join(root, "src", "main.go"))
	expected := PathJoinVirtual(vr, "src/main.go")
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestSandboxConfig_RealToVirtual_RootExactly(t *testing.T) {
	vr := VirtualHome("ws")
	root := t.TempDir()
	s := &SandboxConfig{VirtualRoot: vr, Root: root}
	got := s.RealToVirtual(root)
	if got != vr {
		t.Errorf("expected %q, got %q", vr, got)
	}
}

func TestSandboxConfig_RealToVirtual_PathOutsideRoot_Sanitized(t *testing.T) {
	vr := VirtualHome("ws")
	root := t.TempDir()
	outside := t.TempDir()
	s := &SandboxConfig{VirtualRoot: vr, Root: root}
	outsideFile := filepath.Join(outside, "passwd")
	got := s.RealToVirtual(outsideFile)
	if got == outsideFile {
		t.Errorf("RealToVirtual leaked real path %q", got)
	}
	expected := PathJoinVirtual(vr, "[external]", "passwd")
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestSandboxConfig_RealToVirtual_PathOutsideRoot_LongPath(t *testing.T) {
	vr := VirtualHome("ws")
	root := t.TempDir()
	outside := t.TempDir()
	s := &SandboxConfig{VirtualRoot: vr, Root: root}
	outsideFile := filepath.Join(outside, "some", "data.db")
	got := s.RealToVirtual(outsideFile)
	if got == outsideFile {
		t.Errorf("RealToVirtual leaked real path %q", got)
	}
	expected := PathJoinVirtual(vr, "[external]", "data.db")
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestSandboxConfig_RealToVirtual_NilReceiver(t *testing.T) {
	var s *SandboxConfig
	got := s.RealToVirtual("/any/path")
	if got != "/any/path" {
		t.Errorf("expected %q, got %q", "/any/path", got)
	}
}

func TestSandboxConfig_RealToVirtual_EmptyVirtualRoot(t *testing.T) {
	s := &SandboxConfig{Root: "/tmp/real"}
	got := s.RealToVirtual("/tmp/real/file.txt")
	if got != "/tmp/real/file.txt" {
		t.Errorf("expected unchanged, got %q", got)
	}
}

func TestSandboxConfig_ValidatePath_NilReceiver(t *testing.T) {
	var s *SandboxConfig
	if err := s.ValidatePath("/any/path"); err != nil {
		t.Errorf("nil receiver should return nil, got %v", err)
	}
}

func TestSandboxConfig_ValidatePath_EmptyRoot(t *testing.T) {
	s := &SandboxConfig{VirtualRoot: VirtualHome("ws")}
	if err := s.ValidatePath("/any/path"); err != nil {
		t.Errorf("empty Root should return nil, got %v", err)
	}
}

func TestSandboxConfig_ValidatePath_WithinRoot(t *testing.T) {
	s := &SandboxConfig{VirtualRoot: VirtualHome("ws"), Root: "/tmp/real"}
	if err := s.ValidatePath("/tmp/real/src/main.go"); err != nil {
		t.Errorf("path within root should be valid, got %v", err)
	}
}

func TestSandboxConfig_ValidatePath_RootExactly(t *testing.T) {
	s := &SandboxConfig{VirtualRoot: VirtualHome("ws"), Root: "/tmp/real"}
	if err := s.ValidatePath("/tmp/real"); err != nil {
		t.Errorf("root path itself should be valid, got %v", err)
	}
}

func TestSandboxConfig_ValidatePath_OutsideRoot(t *testing.T) {
	s := &SandboxConfig{VirtualRoot: VirtualHome("ws"), Root: "/tmp/real"}
	if err := s.ValidatePath("/etc/passwd"); err == nil {
		t.Error("path outside root should fail validation")
	}
}

func TestSandboxConfig_ValidatePath_PartialPrefixMatch(t *testing.T) {
	s := &SandboxConfig{VirtualRoot: VirtualHome("ws"), Root: "/tmp/real"}
	if err := s.ValidatePath("/tmp/realfile"); err == nil {
		t.Error("partial prefix match should fail validation")
	}
}

func TestShellCommandSegments_SplitsCorrectly(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"ls -la", []string{"ls -la"}},
		// &&, ||, |, & are binary operators — parsed as ONE statement by mvdan/sh.
		// ShellCommandSegments returns one string per statement, so these produce
		// a single segment containing the full serialized command.
		{"ls && cat file", []string{"ls && cat file"}},
		{"ls || pwd", []string{"ls || pwd"}},
		{"ls | grep foo", []string{"ls | grep foo"}},
		// & as background operator: "ls & pwd" → background ls, then run pwd (two statements).
		{"ls & pwd", []string{"ls", "pwd"}},
		// Semicolon separates into two distinct statements.
		{"ls; pwd", []string{"ls", "pwd"}},
		// Newline separates into two distinct statements.
		{"echo hello\ncat file", []string{"echo hello", "cat file"}},
		// Left-associative: ((ls && echo ok) && rm -rf /) — one statement.
		{"ls && echo ok && rm -rf /", []string{"ls && echo ok && rm -rf /"}},
	}
	for _, tt := range tests {
		got := ShellCommandSegments(tt.input)
		if len(got) != len(tt.expected) {
			t.Errorf("ShellCommandSegments(%q): got %d segments, want %d: %v", tt.input, len(got), len(tt.expected), got)
			continue
		}
		for i := range got {
			if got[i] != tt.expected[i] {
				t.Errorf("ShellCommandSegments(%q)[%d]: got %q, want %q", tt.input, i, got[i], tt.expected[i])
			}
		}
	}
}

func TestShellCommandSegments_CommandSubstitution(t *testing.T) {
	// Command substitutions are NOT split (they are part of words), so the
	// full expression stays in one segment.
	seg := ShellCommandSegments("echo $(curl localhost)")
	if len(seg) != 1 {
		t.Errorf("expected 1 segment for command substitution, got %d: %v", len(seg), seg)
	}
	if !strings.Contains(seg[0], "$(curl localhost)") {
		t.Errorf("segment should contain full cmd substitution: %q", seg[0])
	}

	// Backtick variant. Note: mvdan/sh Printer serializes all command
	// substitutions as $(...), not backticks. So the segment contains
	// "$(whoami)" not "`whoami`". This is fine for security checking since
	// both forms are detected as HasCmdSubst.
	seg2 := ShellCommandSegments("echo `whoami`")
	if len(seg2) != 1 {
		t.Errorf("expected 1 segment for backtick substitution, got %d: %v", len(seg2), seg2)
	}
	// mvdan Printer normalizes `...` to $(...), so we check for $(whoami)
	if !strings.Contains(seg2[0], "$(whoami)") {
		t.Errorf("segment should contain command substitution as $(...): got %q", seg2[0])
	}
}

func TestIsBlockedCommand_CommandSubstitution(t *testing.T) {
	cfg := &SandboxConfig{BlockedCommands: []string{"curl", "wget"}}

	// Direct curl should be blocked
	if !cfg.IsBlockedCommand("curl http://example.com") {
		t.Error("direct curl should be blocked")
	}

	// curl inside $(...) is now detected and blocked.
	// extractInnerCommands walks into CmdSubst.Stmts to find CallExpr commands.
	if !cfg.IsBlockedCommand("echo $(curl http://example.com)") {
		t.Error("curl inside command substitution should be blocked")
	}

	// Backtick form also detected.
	if !cfg.IsBlockedCommand("echo `curl http://example.com`") {
		t.Error("curl inside backtick substitution should be blocked")
	}

	// Pipeline inside substitution: both cat and grep are checked.
	// Use "cat" in BlockedCommands to test this.
	cfg2 := &SandboxConfig{BlockedCommands: []string{"cat", "grep"}}
	if !cfg2.IsBlockedCommand("echo $(cat /etc/passwd | grep root)") {
		t.Error("cat inside pipeline substitution should be blocked")
	}

	// Nested command substitution: $(curl $(wget localhost))
	if !cfg.IsBlockedCommand("echo $(curl $(wget localhost))") {
		t.Error("curl inside nested substitution should be blocked")
	}

	// Non-blocked commands should pass
	if cfg.IsBlockedCommand("echo hello") {
		t.Error("echo should not be blocked")
	}

	// wget is blocked, not curl
	if !cfg.IsBlockedCommand("echo $(wget http://example.com)") {
		t.Error("wget inside command substitution should be blocked")
	}
}

func TestIsBlockedCommand_NetworkBlocked(t *testing.T) {
	// IsBlockedCommand does NOT check AllowNetwork — that's IsBlockedNetwork.
	// It only checks BlockedCommands list. Here we test the BlockedCommands list.
	cfg := &SandboxConfig{BlockedCommands: []string{"curl", "wget", "nc", "telnet"}}

	if !cfg.IsBlockedCommand("curl http://example.com") {
		t.Error("curl should be blocked via BlockedCommands")
	}
	if !cfg.IsBlockedCommand("wget http://example.com") {
		t.Error("wget should be blocked via BlockedCommands")
	}
	if cfg.IsBlockedCommand("ls -la") {
		t.Error("ls should not be blocked")
	}
}

func TestAllowsNetworkTool(t *testing.T) {
	cfg := &SandboxConfig{AllowNetwork: true}
	sb := NewSandbox(*cfg)

	if !sb.AllowsNetworkTool("web_fetch") {
		t.Fatal("expected web_fetch to be allowed by default when network is enabled")
	}
	if sb.AllowsNetworkTool("shell_exec") {
		t.Fatal("expected shell_exec to be blocked by default")
	}

	cfg.AllowedNetworkTools = []string{"web_fetch", "shell_exec"}
	sb = NewSandbox(*cfg)
	if !sb.AllowsNetworkTool("shell_exec") {
		t.Fatal("expected shell_exec to be allowed when explicitly configured")
	}

	cfg.AllowNetwork = false
	sb = NewSandbox(*cfg)
	if sb.AllowsNetworkTool("web_fetch") {
		t.Fatal("expected all network to be blocked when allow_network is false")
	}
}

func TestMergeConfigs_ExplicitEmptyAllowedNetworkToolsOverridesBase(t *testing.T) {
	base := &SandboxConfig{
		AllowNetwork:        true,
		AllowedNetworkTools: []string{"web_fetch", "shell_exec"},
	}
	override := &SandboxConfig{}
	override.SetAllowedNetworkTools(nil)

	merged := MergeConfigs(base, override)
	if len(merged.AllowedNetworkTools) != 0 {
		t.Fatalf("expected empty allowlist to override base, got %v", merged.AllowedNetworkTools)
	}
}

func TestMergeConfigs_UnsetFlagPreservesBase(t *testing.T) {
	// When override has AllowedNetworkTools values but the flag is not set,
	// it should NOT override the base configuration.
	base := &SandboxConfig{
		AllowedNetworkTools: []string{"base_tool"},
	}
	base.allowedNetworkToolsSet = true

	override := &SandboxConfig{}
	override.AllowedNetworkTools = []string{"override_tool"}
	// NOTE: override.allowedNetworkToolsSet is false (default)

	merged := MergeConfigs(base, override)
	if len(merged.AllowedNetworkTools) != 1 || merged.AllowedNetworkTools[0] != "base_tool" {
		t.Fatalf("expected base tools [base_tool] to be preserved, got %v", merged.AllowedNetworkTools)
	}
	if !merged.allowedNetworkToolsSet {
		t.Fatalf("expected allowedNetworkToolsSet to remain true from base")
	}
}

func TestMergeConfigs_ExplicitEmptyOverridesWithFlag(t *testing.T) {
	// When override explicitly sets empty list with flag, it should override base
	base := &SandboxConfig{
		AllowedNetworkTools: []string{"web_fetch", "shell_exec"},
	}
	base.allowedNetworkToolsSet = true

	override := &SandboxConfig{}
	override.SetAllowedNetworkTools([]string{})

	merged := MergeConfigs(base, override)
	if len(merged.AllowedNetworkTools) != 0 {
		t.Fatalf("expected empty list to override base, got %v", merged.AllowedNetworkTools)
	}
	if !merged.allowedNetworkToolsSet {
		t.Fatalf("expected allowedNetworkToolsSet flag to be true after explicit empty override")
	}
}

func TestMergeConfigs_NoOverridePreservesBase(t *testing.T) {
	// When override doesn't set AllowedNetworkTools at all, base should be preserved
	base := &SandboxConfig{
		AllowedNetworkTools: []string{"base_tool"},
	}
	base.allowedNetworkToolsSet = true

	override := &SandboxConfig{}
	// No SetAllowedNetworkTools call, flag remains false

	merged := MergeConfigs(base, override)
	if len(merged.AllowedNetworkTools) != 1 || merged.AllowedNetworkTools[0] != "base_tool" {
		t.Fatalf("expected base config to be preserved, got %v", merged.AllowedNetworkTools)
	}
	if !merged.allowedNetworkToolsSet {
		t.Fatalf("expected base flag to be preserved")
	}
}

func TestMergeConfigs_ExplicitEmptyValidNetworkToolsOverridesBase(t *testing.T) {
	base := &SandboxConfig{
		ValidNetworkTools: []string{"web_fetch", "shell_exec"},
	}
	base.validNetworkToolsSet = true

	override := &SandboxConfig{}
	override.SetValidNetworkTools(nil)

	merged := MergeConfigs(base, override)
	if len(merged.ValidNetworkTools) != 0 {
		t.Fatalf("expected empty valid tool catalog to override base, got %v", merged.ValidNetworkTools)
	}
	if !merged.validNetworkToolsSet {
		t.Fatal("expected validNetworkToolsSet flag to remain true")
	}
}

func TestSandboxedCmd_NilSafety(t *testing.T) {
	// Nil SandboxedCmd.Start() should return error
	var scmd *SandboxedCmd
	if err := scmd.Start(); err == nil {
		t.Error("Start on nil SandboxedCmd should return error")
	}

	// Nil SandboxedCmd.Wait() should return error
	if err := scmd.Wait(); err == nil {
		t.Error("Wait on nil SandboxedCmd should return error")
	}

	// Nil SandboxedCmd.Cleanup() should not panic
	scmd.Cleanup() // should be safe

	// SandboxedCmd with nil Cmd
	scmd = &SandboxedCmd{}
	if err := scmd.Start(); err == nil {
		t.Error("Start on nil Cmd should return error")
	}
	if err := scmd.Wait(); err == nil {
		t.Error("Wait on nil Cmd should return error")
	}
}

func TestSandboxedCmd_CleanupIdempotent(t *testing.T) {
	cleanupCount := 0
	scmd := &SandboxedCmd{
		Cmd: exec.Command("echo", "ok"),
		cleanup: func() {
			cleanupCount++
		},
	}

	// First Cleanup() runs the func
	scmd.Cleanup()
	if cleanupCount != 1 {
		t.Errorf("first Cleanup: expected count=1, got %d", cleanupCount)
	}

	// Second Cleanup() should not run again
	scmd.Cleanup()
	if cleanupCount != 1 {
		t.Errorf("second Cleanup: expected count=1, got %d", cleanupCount)
	}

	// Cleanup after Wait() should be no-op (cleanup already ran via Wait)
	cleanupCount = 0
	scmd = &SandboxedCmd{
		Cmd:     exec.Command("echo", "ok"),
		cleanup: func() { cleanupCount++ },
	}
	scmd.Wait()
	// This call should be a no-op since Wait() already ran cleanup
	scmd.Cleanup()
	if cleanupCount != 1 {
		t.Errorf("Cleanup after Wait: expected count=1 (Wait already called cleanup), got %d", cleanupCount)
	}
	// Another call should also be no-op
	scmd.Cleanup()
	if cleanupCount != 1 {
		t.Errorf("second Cleanup after Wait: expected count=1, got %d", cleanupCount)
	}
}

func TestSandboxedCmd_WaitCallsCleanup(t *testing.T) {
	cleanupCalled := false
	scmd := &SandboxedCmd{
		Cmd:     exec.Command("echo", "ok"),
		cleanup: func() { cleanupCalled = true },
	}

	_ = scmd.Wait()
	if !cleanupCalled {
		t.Error("Wait should have called cleanup")
	}
}

func TestSandboxConfig_ValidatePath_SymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	escape := filepath.Join(root, "escape")
	if err := os.Symlink(outside, escape); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	s := &SandboxConfig{VirtualRoot: VirtualHome("ws"), Root: root}
	if err := s.ValidatePath(filepath.Join(escape, "secret.txt")); err == nil {
		t.Fatal("symlink escape should fail validation")
	}
}

func TestLaunchProcess_ReturnsNilOnError(t *testing.T) {
	// A nil Sandbox should return error from LaunchProcess, not a half-initialized object.
	var s *Sandbox
	scmd, err := s.LaunchProcess(context.Background(), "echo", []string{"ok"}, "")
	if err == nil {
		t.Error("LaunchProcess with nil Sandbox should return error")
	}
	if scmd != nil {
		t.Error("LaunchProcess with nil Sandbox should return nil SandboxedCmd")
	}

	// With a real Sandbox that has empty config, LaunchProcess should succeed
	realSandbox := NewSandbox(SandboxConfig{})
	scmd, err = realSandbox.LaunchProcess(context.Background(), "echo", []string{"ok"}, "")
	if err != nil {
		t.Errorf("LaunchProcess with empty config failed: %v", err)
	}
	if scmd == nil {
		t.Fatal("LaunchProcess should return non-nil SandboxedCmd on success")
	}
	// Clean up
	scmd.Cleanup()
}

func TestSetAllowedNetworkTools_ValidAgainstConfiguredCatalog(t *testing.T) {
	tests := []struct {
		name     string
		tools    []string
		expected []string
	}{
		{"empty list", []string{}, []string{}},
		{"single tool", []string{"web_fetch"}, []string{"web_fetch"}},
		{"multiple tools", []string{"web_fetch", "shell_exec"}, []string{"web_fetch", "shell_exec"}},
		{"uppercase normalized", []string{"WEB_FETCH"}, []string{"web_fetch"}},
		{"mixed case normalized", []string{"Web_Fetch", "SHELL_EXEC"}, []string{"web_fetch", "shell_exec"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &SandboxConfig{}
			cfg.SetValidNetworkTools([]string{"web_fetch", "shell_exec"})
			err := cfg.SetAllowedNetworkTools(tt.tools)
			if err != nil {
				t.Errorf("SetAllowedNetworkTools(%v) failed: %v", tt.tools, err)
			}
			if !slices.Equal(cfg.AllowedNetworkTools, tt.expected) {
				t.Errorf("got %v, expected %v", cfg.AllowedNetworkTools, tt.expected)
			}
			if !cfg.HasAllowedNetworkToolsOverride() {
				t.Error("flag should be set")
			}
		})
	}
}

func TestSetAllowedNetworkTools_WithoutConfiguredCatalogStillNormalizes(t *testing.T) {
	cfg := &SandboxConfig{}
	if err := cfg.SetAllowedNetworkTools([]string{"CuRL", "WEB_FETCH"}); err != nil {
		t.Fatalf("SetAllowedNetworkTools returned unexpected error: %v", err)
	}
	expected := []string{"curl", "web_fetch"}
	if !slices.Equal(cfg.AllowedNetworkTools, expected) {
		t.Fatalf("got %v, expected %v", cfg.AllowedNetworkTools, expected)
	}
	if !cfg.HasAllowedNetworkToolsOverride() {
		t.Fatal("flag should be set")
	}
}

func TestSetAllowedNetworkTools_InvalidAgainstConfiguredCatalog(t *testing.T) {
	tests := []struct {
		name    string
		tools   []string
		wantErr bool
	}{
		{"invalid tool", []string{"curl"}, true},
		{"invalid tool in list", []string{"web_fetch", "curl"}, true},
		{"typo", []string{"web-fetch"}, true},
		{"completely wrong", []string{"bad_tool"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &SandboxConfig{}
			cfg.SetValidNetworkTools([]string{"web_fetch", "shell_exec"})
			err := cfg.SetAllowedNetworkTools(tt.tools)
			if (err != nil) != tt.wantErr {
				t.Errorf("SetAllowedNetworkTools(%v) error = %v, wantErr = %v", tt.tools, err, tt.wantErr)
			}
		})
	}
}

func TestSandboxConfig_UnmarshalYAML_NetworkTools(t *testing.T) {
	var cfg SandboxConfig
	data := []byte(`
sandbox:
  allow_network: true
  valid_network_tools:
    - WEB_FETCH
    - shell_exec
  allowed_network_tools:
    - Web_Fetch
`)

	var wrapper struct {
		Sandbox SandboxConfig `yaml:"sandbox"`
	}
	if err := yaml.Unmarshal(data, &wrapper); err != nil {
		t.Fatalf("yaml.Unmarshal failed: %v", err)
	}
	cfg = wrapper.Sandbox

	if !cfg.AllowNetwork || !cfg.HasAllowNetworkOverride() {
		t.Fatal("expected allow_network override to be preserved")
	}
	if !cfg.HasValidNetworkToolsOverride() {
		t.Fatal("expected valid_network_tools override flag to be set")
	}
	if !slices.Equal(cfg.ValidNetworkTools, []string{"web_fetch", "shell_exec"}) {
		t.Fatalf("unexpected valid network tools: %v", cfg.ValidNetworkTools)
	}
	if !cfg.HasAllowedNetworkToolsOverride() {
		t.Fatal("expected allowed_network_tools override flag to be set")
	}
	if !slices.Equal(cfg.AllowedNetworkTools, []string{"web_fetch"}) {
		t.Fatalf("unexpected allowed network tools: %v", cfg.AllowedNetworkTools)
	}
}

func TestUnmarshalYAML_InvalidAllowedToolsInYAML(t *testing.T) {
	yamlData := `
allow_network: true
valid_network_tools:
  - web_fetch
allowed_network_tools:
  - web_fetch
  - invalid_tool
`
	var cfg SandboxConfig
	err := yaml.Unmarshal([]byte(yamlData), &cfg)
	if err == nil {
		t.Fatal("expected error when unmarshaling invalid allowed_network_tools")
	}
	if !strings.Contains(err.Error(), `invalid network tool "invalid_tool"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}
