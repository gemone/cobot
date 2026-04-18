package sandbox

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	s := &SandboxConfig{VirtualRoot: vr, Root: "/tmp/real"}
	input := "cat " + PathJoinVirtual(vr, "src/main.go")
	expected := "cat /tmp/real/src/main.go"
	got := s.RewritePaths(input)
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestSandboxConfig_RewritePaths_MultiplePaths(t *testing.T) {
	vr := VirtualHome("ws")
	s := &SandboxConfig{VirtualRoot: vr, Root: "/tmp/real"}
	input := "cp " + PathJoinVirtual(vr, "a.txt") + " " + PathJoinVirtual(vr, "b.txt")
	expected := "cp /tmp/real/a.txt /tmp/real/b.txt"
	got := s.RewritePaths(input)
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestSandboxConfig_RewritePaths_CommandString(t *testing.T) {
	vr := VirtualHome("myworkspace")
	s := &SandboxConfig{VirtualRoot: vr, Root: "/var/data/ws"}
	input := "grep -r 'TODO' " + PathJoinVirtual(vr, "src") + " && echo done > " + PathJoinVirtual(vr, "out.log")
	expected := "grep -r 'TODO' /var/data/ws/src && echo done > /var/data/ws/out.log"
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
	s := &SandboxConfig{VirtualRoot: vr, Root: "/tmp/real"}
	original := errors.New("open /tmp/real/secret.txt: permission denied")
	rewritten := s.RewriteError(original)

	if rewritten == nil {
		t.Fatal("expected rewritten error")
	}
	if !errors.Is(rewritten, original) {
		t.Fatal("rewritten error should preserve original cause")
	}
	if strings.Contains(rewritten.Error(), "/tmp/real") {
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
	s := &SandboxConfig{VirtualRoot: vr, Root: "/tmp/real"}
	got := s.RealToVirtual("/tmp/real/src/main.go")
	expected := PathJoinVirtual(vr, "src/main.go")
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestSandboxConfig_RealToVirtual_RootExactly(t *testing.T) {
	vr := VirtualHome("ws")
	s := &SandboxConfig{VirtualRoot: vr, Root: "/tmp/real"}
	got := s.RealToVirtual("/tmp/real")
	if got != vr {
		t.Errorf("expected %q, got %q", vr, got)
	}
}

func TestSandboxConfig_RealToVirtual_PathOutsideRoot_Sanitized(t *testing.T) {
	vr := VirtualHome("ws")
	s := &SandboxConfig{VirtualRoot: vr, Root: "/tmp/real"}
	got := s.RealToVirtual("/etc/passwd")
	if got == "/etc/passwd" {
		t.Errorf("RealToVirtual leaked real path %q", got)
	}
	expected := PathJoinVirtual(vr, "[external]", "passwd")
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestSandboxConfig_RealToVirtual_PathOutsideRoot_LongPath(t *testing.T) {
	vr := VirtualHome("ws")
	s := &SandboxConfig{VirtualRoot: vr, Root: "/tmp/real"}
	got := s.RealToVirtual("/usr/local/lib/some/data.db")
	if got == "/usr/local/lib/some/data.db" {
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
