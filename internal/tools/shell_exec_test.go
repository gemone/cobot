package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	sandboxpkg "github.com/cobot-agent/cobot/internal/sandbox"
)

type stubLauncher struct {
	request *sandboxpkg.LaunchRequest
	output  []byte
	err     error
}

func (s *stubLauncher) Launch(_ context.Context, req *sandboxpkg.LaunchRequest) ([]byte, error) {
	s.request = req
	return s.output, s.err
}

// --- validateWritePaths unit tests ---

func TestValidateWritePaths_AllowsReadOnlyCmd(t *testing.T) {
	cfg := &sandboxpkg.SandboxConfig{
		Root:          t.TempDir(),
		VirtualRoot:   sandboxpkg.VirtualHome("ws"),
		ReadonlyPaths: []string{t.TempDir() + "/readonly"},
	}
	err := validateWritePaths(cfg, "cat /etc/hostname")
	if err != nil {
		t.Errorf("read-only command should be allowed, got: %v", err)
	}
}

func TestValidateWritePaths_AllowsAllowedPath(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "allowed"), 0755)
	cfg := &sandboxpkg.SandboxConfig{
		Root:        dir,
		AllowPaths:  []string{dir + "/allowed"},
		VirtualRoot: sandboxpkg.VirtualHome("ws"),
	}
	err := validateWritePaths(cfg, "echo hello > "+dir+"/allowed/out.txt")
	if err != nil {
		t.Errorf("write to allowed path should be allowed, got: %v", err)
	}
}

func TestValidateWritePaths_AllowsRootPath(t *testing.T) {
	dir := t.TempDir()
	cfg := &sandboxpkg.SandboxConfig{
		Root:        dir,
		VirtualRoot: sandboxpkg.VirtualHome("ws"),
	}
	err := validateWritePaths(cfg, "echo hello > "+dir+"/out.txt")
	if err != nil {
		t.Errorf("write to root path should be allowed, got: %v", err)
	}
}

func TestValidateWritePaths_RejectsReadonlyPath(t *testing.T) {
	dir := t.TempDir()
	readonlyDir := filepath.Join(dir, "readonly")
	os.MkdirAll(readonlyDir, 0755)
	// Resolve symlinks so the stored path matches what IsAllowed computes internally
	// (IsAllowed calls EvalSymlinks on both the checked path and the pattern).
	readonlyDir, _ = filepath.EvalSymlinks(readonlyDir)
	cfg := &sandboxpkg.SandboxConfig{
		Root:          dir,
		VirtualRoot:   sandboxpkg.VirtualHome("ws"),
		ReadonlyPaths: []string{readonlyDir},
	}
	err := validateWritePaths(cfg, "echo hello > "+readonlyDir+"/out.txt")
	if err == nil {
		t.Error("write to readonly path should be rejected")
	}
	if !strings.Contains(err.Error(), "readonly") {
		t.Errorf("error should mention readonly, got: %v", err)
	}
}

func TestValidateWritePaths_RejectsOutsideSandbox(t *testing.T) {
	dir := t.TempDir()
	cfg := &sandboxpkg.SandboxConfig{
		Root:        dir,
		VirtualRoot: sandboxpkg.VirtualHome("ws"),
	}
	err := validateWritePaths(cfg, "echo hello > /tmp/outside.txt")
	if err == nil {
		t.Error("write outside sandbox should be rejected")
	}
	if !strings.Contains(err.Error(), "outside sandbox") {
		t.Errorf("error should mention outside sandbox, got: %v", err)
	}
}

func TestValidateWritePaths_AllowsAppend(t *testing.T) {
	dir := t.TempDir()
	cfg := &sandboxpkg.SandboxConfig{
		Root:        dir,
		VirtualRoot: sandboxpkg.VirtualHome("ws"),
	}
	err := validateWritePaths(cfg, "echo hello >> "+dir+"/out.txt")
	if err != nil {
		t.Errorf("append to root path should be allowed, got: %v", err)
	}
}

func TestValidateWritePaths_RejectsReadonlyAppend(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "readonly"), 0755)
	cfg := &sandboxpkg.SandboxConfig{
		Root:          dir,
		VirtualRoot:   sandboxpkg.VirtualHome("ws"),
		ReadonlyPaths: []string{dir + "/readonly"},
	}
	err := validateWritePaths(cfg, "echo hello >> "+dir+"/readonly/out.txt")
	if err == nil {
		t.Error("append to readonly path should be rejected")
	}
}

func TestValidateWritePaths_HandlesTee(t *testing.T) {
	dir := t.TempDir()
	cfg := &sandboxpkg.SandboxConfig{
		Root:        dir,
		VirtualRoot: sandboxpkg.VirtualHome("ws"),
	}
	err := validateWritePaths(cfg, "echo hello | tee "+dir+"/out.txt")
	if err != nil {
		t.Errorf("tee to root path should be allowed, got: %v", err)
	}
}

func TestValidateWritePaths_RejectsTeeToReadonly(t *testing.T) {
	dir := t.TempDir()
	readonlyDir := filepath.Join(dir, "readonly")
	os.MkdirAll(readonlyDir, 0755)
	readonlyDir, _ = filepath.EvalSymlinks(readonlyDir)
	cfg := &sandboxpkg.SandboxConfig{
		Root:          dir,
		VirtualRoot:   sandboxpkg.VirtualHome("ws"),
		ReadonlyPaths: []string{readonlyDir},
	}
	err := validateWritePaths(cfg, "echo hello | tee "+readonlyDir+"/out.txt")
	if err == nil {
		t.Error("tee to readonly path should be rejected")
	}
}

func TestValidateWritePaths_IgnoresInputRedirection(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "in.txt"), []byte("input"), 0644)
	cfg := &sandboxpkg.SandboxConfig{
		Root:        dir,
		VirtualRoot: sandboxpkg.VirtualHome("ws"),
	}
	// Input redirection < should not be treated as a write.
	err := validateWritePaths(cfg, "cat < "+dir+"/in.txt")
	if err != nil {
		t.Errorf("input redirection should not be treated as write, got: %v", err)
	}
}

func TestValidateWritePaths_HandlesStderrRedirection(t *testing.T) {
	dir := t.TempDir()
	cfg := &sandboxpkg.SandboxConfig{
		Root:        dir,
		VirtualRoot: sandboxpkg.VirtualHome("ws"),
	}
	err := validateWritePaths(cfg, "cat /nonexistent 2> "+dir+"/err.txt")
	if err != nil {
		t.Errorf("stderr redirection should be allowed, got: %v", err)
	}
}

func TestValidateWritePaths_RejectsStderrToReadonly(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "readonly"), 0755)
	cfg := &sandboxpkg.SandboxConfig{
		Root:          dir,
		VirtualRoot:   sandboxpkg.VirtualHome("ws"),
		ReadonlyPaths: []string{dir + "/readonly"},
	}
	err := validateWritePaths(cfg, "cat /nonexistent 2> "+dir+"/readonly/err.txt")
	if err == nil {
		t.Error("stderr write to readonly path should be rejected")
	}
}

func TestValidateWritePaths_NilConfig(t *testing.T) {
	err := validateWritePaths(nil, "echo hello > /tmp/out.txt")
	if err != nil {
		t.Errorf("nil config should allow all writes, got: %v", err)
	}
}

func TestValidateWritePaths_InactiveSandboxPolicy_AllowsWrites(t *testing.T) {
	cfg := &sandboxpkg.SandboxConfig{
		VirtualRoot: sandboxpkg.VirtualHome("ws"),
	}
	if err := validateWritePaths(cfg, "echo hello > /tmp/out.txt"); err != nil {
		t.Errorf("inactive sandbox policy should not block writes, got: %v", err)
	}
}

func TestValidateWritePaths_RelativeRedirectUsesCommandDir(t *testing.T) {
	root := t.TempDir()
	commandDir := filepath.Join(root, "subdir")
	if err := os.MkdirAll(filepath.Join(commandDir, "nested"), 0755); err != nil {
		t.Fatalf("mkdir command dir: %v", err)
	}
	cfg := &sandboxpkg.SandboxConfig{
		Root:        root,
		VirtualRoot: sandboxpkg.VirtualHome("ws"),
	}
	if err := validateWritePaths(cfg, "echo hello > nested/out.txt", commandDir); err != nil {
		t.Errorf("relative redirect should resolve from command dir, got: %v", err)
	}
}

func TestValidateWritePaths_RejectsCommonRedirectWriteSyntaxes(t *testing.T) {
	root := t.TempDir()
	readonlyDir := filepath.Join(root, "readonly")
	if err := os.MkdirAll(readonlyDir, 0755); err != nil {
		t.Fatalf("mkdir readonly dir: %v", err)
	}
	readonlyDir, _ = filepath.EvalSymlinks(readonlyDir)
	cfg := &sandboxpkg.SandboxConfig{
		Root:          root,
		VirtualRoot:   sandboxpkg.VirtualHome("ws"),
		ReadonlyPaths: []string{readonlyDir},
	}

	tests := []struct {
		name string
		cmd  string
	}{
		{name: "space_after_operator", cmd: "echo hello > readonly/out.txt"},
		{name: "no_space_after_operator", cmd: "echo hello >readonly/out.txt"},
		{name: "append_with_space", cmd: "echo hello >> readonly/out.txt"},
		{name: "append_without_space", cmd: "echo hello >>readonly/out.txt"},
		{name: "stderr_without_space", cmd: "echo hello 2>readonly/err.txt"},
		{name: "fd3_without_space", cmd: "echo hello 3>readonly/fd3.txt"},
		{name: "fd9_append_without_space", cmd: "echo hello 9>>readonly/fd9.txt"},
		{name: "clobber_without_space", cmd: "echo hello >|readonly/forced.txt"},
		{name: "clobber_with_space", cmd: "echo hello >| readonly/forced.txt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateWritePaths(cfg, tt.cmd, root)
			if err == nil {
				t.Fatalf("expected %q to be rejected", tt.cmd)
			}
			if !strings.Contains(err.Error(), "readonly") {
				t.Fatalf("expected readonly error for %q, got: %v", tt.cmd, err)
			}
		})
	}
}

func TestExtractRedirectWriteTargets_PreservesWindowsAbsolutePath(t *testing.T) {
	windowsPath := `C:\Users\runner\AppData\Local\Temp\out.txt`
	targets := extractRedirectWriteTargetsWithBackslashEscapes("echo hello > "+windowsPath, false)
	if len(targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(targets))
	}
	if targets[0].path != windowsPath {
		t.Fatalf("redirect path = %q, want %q", targets[0].path, windowsPath)
	}
}

func TestExtractTeeWriteTargets_PreservesWindowsAbsolutePath(t *testing.T) {
	windowsPath := `C:\Users\runner\AppData\Local\Temp\tee-out.txt`
	targets := extractTeeWriteTargetsWithBackslashEscapes("tee -a "+windowsPath, false)
	if len(targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(targets))
	}
	if targets[0].path != windowsPath {
		t.Fatalf("tee path = %q, want %q", targets[0].path, windowsPath)
	}
	if !targets[0].append {
		t.Fatal("expected tee append mode to be preserved")
	}
}

func TestExtractRedirectWriteTargets_PreservesUnixEscapedSpaces(t *testing.T) {
	targets := extractRedirectWriteTargetsWithBackslashEscapes(`echo hello > out\ file.txt`, true)
	if len(targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(targets))
	}
	if targets[0].path != "out file.txt" {
		t.Fatalf("redirect path = %q, want %q", targets[0].path, "out file.txt")
	}
}

// --- ShellExecTool integration tests ---

func TestShellExecTool_NoSandbox(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "in.txt"), []byte("hello world"), 0644)

	tool := NewShellExecTool(WithShellWorkdir(dir))
	args, _ := json.Marshal(map[string]any{"command": "cat in.txt"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "hello world") {
		t.Errorf("expected 'hello world', got: %q", result)
	}
}

func TestShellExecTool_SandboxVirtualRootRewrite(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("sandbox content"), 0644)
	vr := sandboxpkg.VirtualHome("myws")
	cfg := &sandboxpkg.SandboxConfig{VirtualRoot: vr, Root: dir}

	tool := NewShellExecTool(WithShellSandboxConfig(cfg))
	// LLM uses the virtual path; command should be rewritten to real path before execution.
	vp := sandboxpkg.PathJoinVirtual(vr, "hello.txt")
	args, _ := json.Marshal(map[string]any{"command": "cat " + vp})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "sandbox content") {
		t.Errorf("expected sandbox content, got: %q", result)
	}
}

func TestShellExecTool_SandboxOutputRewrite(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("content"), 0644)
	vr := sandboxpkg.VirtualHome("myws")
	cfg := &sandboxpkg.SandboxConfig{VirtualRoot: vr, Root: dir}

	tool := NewShellExecTool(WithShellSandboxConfig(cfg))
	// LLM uses the virtual path; command should be rewritten to real path before execution.
	vp := sandboxpkg.PathJoinVirtual(vr, "hello.txt")
	args, _ := json.Marshal(map[string]any{"command": "cat " + vp})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Output should contain the file content.
	if !strings.Contains(result, "content") {
		t.Errorf("expected file content 'content', got: %q", result)
	}
	// Verify real path does NOT appear in output (only virtual path in the "cat" command itself is rewritten).
	if strings.Contains(result, dir) {
		t.Errorf("output should NOT expose real path %q, got: %q", dir, result)
	}
}

func TestShellExecTool_SandboxBlockedCommand(t *testing.T) {
	dir := t.TempDir()
	vr := sandboxpkg.VirtualHome("myws")
	cfg := &sandboxpkg.SandboxConfig{
		VirtualRoot:     vr,
		Root:            dir,
		BlockedCommands: []string{"rm -rf /"},
	}

	tool := NewShellExecTool(WithShellSandboxConfig(cfg))
	args, _ := json.Marshal(map[string]any{"command": "rm -rf /"})
	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Error("blocked command should be rejected")
	}
	if !strings.Contains(err.Error(), "blocked") {
		t.Errorf("error should mention 'blocked', got: %v", err)
	}
}

func TestShellExecTool_NetworkBlocked(t *testing.T) {
	dir := t.TempDir()
	vr := sandboxpkg.VirtualHome("myws")
	cfg := &sandboxpkg.SandboxConfig{
		VirtualRoot:  vr,
		Root:         dir,
		AllowNetwork: false,
	}

	tool := NewShellExecTool(WithShellSandboxConfig(cfg))
	args, _ := json.Marshal(map[string]any{"command": "curl https://example.com"})
	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Error("network command should be blocked when AllowNetwork=false")
	}
	if !strings.Contains(err.Error(), "network") {
		t.Errorf("error should mention 'network', got: %v", err)
	}
}

func TestShellExecTool_NetworkAllowed(t *testing.T) {
	dir := t.TempDir()
	vr := sandboxpkg.VirtualHome("myws")
	cfg := &sandboxpkg.SandboxConfig{
		VirtualRoot:  vr,
		Root:         dir,
		AllowNetwork: true,
	}

	tool := NewShellExecTool(WithShellSandboxConfig(cfg))
	// curl will fail network-wise but should not be rejected by policy.
	args, _ := json.Marshal(map[string]any{"command": "curl --max-time 1 https://127.0.0.1:1"})
	_, err := tool.Execute(context.Background(), args)
	// We expect an error (connection refused) but NOT a "network command blocked" error.
	if err != nil && strings.Contains(err.Error(), "network") {
		t.Errorf("network command should be allowed, got: %v", err)
	}
}

func TestShellExecTool_WriteToReadonlyRejected(t *testing.T) {
	dir := t.TempDir()
	readonlyDir := filepath.Join(dir, "readonly")
	os.MkdirAll(readonlyDir, 0755)
	vr := sandboxpkg.VirtualHome("myws")
	cfg := &sandboxpkg.SandboxConfig{
		VirtualRoot:   vr,
		Root:          dir,
		ReadonlyPaths: []string{readonlyDir}, // absolute path — must match how config is stored
		AllowNetwork:  false,
	}

	tool := NewShellExecTool(WithShellSandboxConfig(cfg))
	args, _ := json.Marshal(map[string]any{"command": "echo test > " + readonlyDir + "/out.txt"})
	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Error("write to readonly path should be rejected before execution")
	}
	if !strings.Contains(err.Error(), "readonly") && !strings.Contains(err.Error(), "sandbox") {
		t.Errorf("error should mention readonly or sandbox, got: %v", err)
	}
}

func TestShellExecTool_WriteToOutsideSandboxRejected(t *testing.T) {
	dir := t.TempDir()
	vr := sandboxpkg.VirtualHome("myws")
	cfg := &sandboxpkg.SandboxConfig{
		VirtualRoot:  vr,
		Root:         dir,
		AllowNetwork: false,
	}

	tool := NewShellExecTool(WithShellSandboxConfig(cfg))
	args, _ := json.Marshal(map[string]any{"command": "echo test > /tmp/outside_sandbox.txt"})
	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Error("write outside sandbox should be rejected before execution")
	}
}

func TestShellExecTool_ValidWriteAllowed(t *testing.T) {
	dir := t.TempDir()
	vr := sandboxpkg.VirtualHome("myws")
	cfg := &sandboxpkg.SandboxConfig{
		VirtualRoot:  vr,
		Root:         dir,
		AllowNetwork: false,
	}

	tool := NewShellExecTool(WithShellSandboxConfig(cfg))
	args, _ := json.Marshal(map[string]any{"command": "echo valid > " + dir + "/out.txt"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Errorf("write to sandbox root should be allowed, got: %v", err)
	}
	// Verify file was created with correct content.
	data, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	if err != nil {
		t.Fatalf("file should exist: %v", err)
	}
	if strings.TrimSpace(string(data)) != "valid" {
		t.Errorf("file content mismatch, got: %q", string(data))
	}
	// Result should not expose the real path.
	if strings.Contains(result, dir) {
		t.Errorf("result should not expose real path %q, got: %q", dir, result)
	}
}

func TestShellExecTool_UsesLauncher(t *testing.T) {
	dir := t.TempDir()
	cfg := &sandboxpkg.SandboxConfig{
		VirtualRoot:     sandboxpkg.VirtualHome("myws"),
		Root:            dir,
		AllowPaths:      []string{"/allowed"},
		ReadonlyPaths:   []string{"/readonly"},
		BlockedCommands: []string{"rm -rf"},
	}
	cfg.SetAllowNetwork(false)

	backend := &stubLauncher{output: []byte("launcher ok")}
	tool := NewShellExecTool(
		WithShellWorkdir(dir),
		WithShellSandboxConfig(cfg),
		WithShellLauncher(sandboxpkg.NewLauncher(sandboxpkg.WithLaunchFunc(backend.Launch))),
	)

	args, _ := json.Marshal(map[string]any{"command": "echo ok"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "launcher ok" {
		t.Fatalf("result = %q, want launcher ok", result)
	}
	if backend.request == nil {
		t.Fatal("expected launcher backend to receive request")
	}
	if backend.request.Command != "echo ok" {
		t.Fatalf("request.Command = %q, want echo ok", backend.request.Command)
	}
	if backend.request.Dir != dir {
		t.Fatalf("request.Dir = %q, want %q", backend.request.Dir, dir)
	}
	if backend.request.Config == nil {
		t.Fatal("expected launcher request to include sandbox config")
	}
	if backend.request.Config.Root != dir {
		t.Fatalf("config.Root = %q, want %q", backend.request.Config.Root, dir)
	}
	if backend.request.Config.VirtualRoot != cfg.VirtualRoot {
		t.Fatalf("config.VirtualRoot = %q, want %q", backend.request.Config.VirtualRoot, cfg.VirtualRoot)
	}
	if backend.request.Config.AllowNetwork {
		t.Fatal("config.AllowNetwork = true, want false")
	}
	if len(backend.request.Config.ReadonlyPaths) != 1 || backend.request.Config.ReadonlyPaths[0] != "/readonly" {
		t.Fatalf("config.ReadonlyPaths = %v, want [/readonly]", backend.request.Config.ReadonlyPaths)
	}
}

func TestShellExecTool_UsesLauncherError(t *testing.T) {
	backend := &stubLauncher{err: errors.New("launcher failure")}
	tool := NewShellExecTool(
		WithShellLauncher(sandboxpkg.NewLauncher(sandboxpkg.WithLaunchFunc(backend.Launch))),
	)

	args, _ := json.Marshal(map[string]any{"command": "echo ok"})
	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Fatal("expected launcher error")
	}
	if !strings.Contains(err.Error(), "launcher failure") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestShellExecTool_TeeAllowed(t *testing.T) {
	dir := t.TempDir()
	vr := sandboxpkg.VirtualHome("myws")
	cfg := &sandboxpkg.SandboxConfig{
		VirtualRoot:  vr,
		Root:         dir,
		AllowNetwork: false,
	}

	tool := NewShellExecTool(WithShellSandboxConfig(cfg))
	args, _ := json.Marshal(map[string]any{"command": "echo tee_test | tee " + dir + "/tee_out.txt"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Errorf("tee to sandbox root should be allowed, got: %v", err)
	}
	if !strings.Contains(result, "tee_test") {
		t.Errorf("output should contain tee_test, got: %q", result)
	}
	data, err := os.ReadFile(filepath.Join(dir, "tee_out.txt"))
	if err != nil {
		t.Fatalf("tee output file should exist: %v", err)
	}
	if strings.TrimSpace(string(data)) != "tee_test" {
		t.Errorf("tee file content mismatch, got: %q", string(data))
	}
}

func TestShellExecTool_RelativeRedirectUsesEffectiveDir(t *testing.T) {
	root := t.TempDir()
	commandDir := filepath.Join(root, "subdir")
	if err := os.MkdirAll(filepath.Join(commandDir, "nested"), 0755); err != nil {
		t.Fatalf("mkdir command dir: %v", err)
	}
	vr := sandboxpkg.VirtualHome("myws")
	cfg := &sandboxpkg.SandboxConfig{
		VirtualRoot:  vr,
		Root:         root,
		AllowNetwork: false,
	}

	tool := NewShellExecTool(
		WithShellWorkdir(root),
		WithShellSandboxConfig(cfg),
	)
	args, _ := json.Marshal(map[string]any{"command": "echo redirect_test > nested/out.txt", "dir": "subdir"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("relative redirect should be allowed, got: %v", err)
	}
	if strings.Contains(result, root) {
		t.Fatalf("result should not expose real path %q, got: %q", root, result)
	}
	data, err := os.ReadFile(filepath.Join(commandDir, "nested", "out.txt"))
	if err != nil {
		t.Fatalf("relative redirect output file should exist: %v", err)
	}
	if strings.TrimSpace(string(data)) != "redirect_test" {
		t.Fatalf("relative redirect file content mismatch, got: %q", string(data))
	}
}

func TestShellExecTool_TeeAppendUsesEffectiveDir(t *testing.T) {
	root := t.TempDir()
	commandDir := filepath.Join(root, "subdir")
	if err := os.MkdirAll(commandDir, 0755); err != nil {
		t.Fatalf("mkdir command dir: %v", err)
	}
	teePath := filepath.Join(commandDir, "tee_out.txt")
	if err := os.WriteFile(teePath, []byte("first\n"), 0644); err != nil {
		t.Fatalf("seed tee file: %v", err)
	}
	vr := sandboxpkg.VirtualHome("myws")
	cfg := &sandboxpkg.SandboxConfig{
		VirtualRoot:  vr,
		Root:         root,
		AllowNetwork: false,
	}

	tool := NewShellExecTool(
		WithShellWorkdir(root),
		WithShellSandboxConfig(cfg),
	)
	// Use OS-agnostic command: avoid single quotes which cmd doesn't strip.
	cmdStr := `printf "second\n" | tee -a tee_out.txt`
	if runtime.GOOS == "windows" {
		cmdStr = `echo second | tee -a tee_out.txt`
	}
	args, _ := json.Marshal(map[string]any{"command": cmdStr, "dir": "subdir"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("tee -a relative write should be allowed, got: %v", err)
	}
	if !strings.Contains(result, "second") {
		t.Fatalf("tee output should contain appended text, got: %q", result)
	}
	data, err := os.ReadFile(teePath)
	if err != nil {
		t.Fatalf("tee output file should exist: %v", err)
	}
	// Normalize line endings and trim trailing whitespace for cross-platform comparison.
	// On Windows, echo adds a trailing space; TrimSpace handles both CRLF and trailing spaces.
	got := strings.TrimSpace(strings.ReplaceAll(string(data), "\r\n", "\n"))
	want := "first\nsecond"
	if got != want {
		t.Fatalf("tee append file content mismatch, got: %q", string(data))
	}
}

func TestShellExecTool_DirOutsideSandboxRejected(t *testing.T) {
	dir := t.TempDir()
	vr := sandboxpkg.VirtualHome("myws")
	cfg := &sandboxpkg.SandboxConfig{
		VirtualRoot:  vr,
		Root:         dir,
		AllowNetwork: false,
	}

	tool := NewShellExecTool(WithShellSandboxConfig(cfg))
	// Trying to set Dir to outside the sandbox should be rejected.
	// Use a path that does NOT start with the virtual root, so AutoResolvePath
	// cannot map it into the sandbox.
	args, _ := json.Marshal(map[string]any{"command": "pwd", "dir": "/outside_sandbox"})
	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Error("dir outside sandbox should be rejected")
	}
}

func TestShellExecTool_DirDefaultToWorkdir(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "marker.txt"), []byte("workdir_test"), 0644)

	tool := NewShellExecTool(WithShellWorkdir(dir))
	args, _ := json.Marshal(map[string]any{"command": "cat marker.txt"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "workdir_test") {
		t.Errorf("expected workdir_test, got: %q", result)
	}
}
