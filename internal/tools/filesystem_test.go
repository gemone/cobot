package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	sandpkg "github.com/cobot-agent/cobot/internal/sandbox"
)

func TestReadFileTool(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "test.txt")
	os.WriteFile(f, []byte("hello world"), 0644)

	tool := NewReadFileTool(nil)
	args, _ := json.Marshal(map[string]string{"path": f})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if result != "hello world" {
		t.Errorf("expected 'hello world', got %s", result)
	}
}

func TestWriteFileTool(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "output.txt")

	tool := NewWriteFileTool(nil)
	args, _ := json.Marshal(map[string]string{"path": f, "content": "written content"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if result != "ok" {
		t.Errorf("expected ok, got %s", result)
	}
	data, _ := os.ReadFile(f)
	if string(data) != "written content" {
		t.Errorf("file content mismatch: %s", string(data))
	}
}

func TestReadFileNotFound(t *testing.T) {
	tool := NewReadFileTool(nil)
	args, _ := json.Marshal(map[string]string{"path": "/nonexistent/file.txt"})
	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestShellExecTool(t *testing.T) {
	tool := NewShellExecTool()
	args, _ := json.Marshal(map[string]string{"command": "echo hello"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	result = strings.ReplaceAll(result, "\r\n", "\n")
	if result != "hello\n" {
		t.Errorf("expected %q, got %q", "hello\n", result)
	}
}

func TestShellExecToolMultiArg(t *testing.T) {
	tool := NewShellExecTool()
	args, _ := json.Marshal(map[string]string{"command": "echo hello world"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	result = strings.ReplaceAll(result, "\r\n", "\n")
	if result != "hello world\n" {
		t.Errorf("expected %q, got %q", "hello world\n", result)
	}
}

func TestReadFileTool_SandboxResolve(t *testing.T) {
	dir := t.TempDir()
	vr := sandpkg.VirtualHome("test")
	sandbox := &sandpkg.SandboxConfig{VirtualRoot: vr, Root: dir}
	os.MkdirAll(filepath.Join(dir, "src"), 0755)
	os.WriteFile(filepath.Join(dir, "src", "main.go"), []byte("package main"), 0644)

	tool := NewReadFileTool(sandbox)

	// Virtual path resolves correctly
	vp := sandpkg.PathJoinVirtual(vr, "src/main.go")
	args, _ := json.Marshal(map[string]string{"path": vp})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	expected := "# " + vp + "\npackage main"
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestReadFileTool_SandboxRejectOutside(t *testing.T) {
	dir := t.TempDir()
	sandbox := &sandpkg.SandboxConfig{VirtualRoot: sandpkg.VirtualHome("test"), Root: dir}

	tool := NewReadFileTool(sandbox)

	args, _ := json.Marshal(map[string]string{"path": "/etc/passwd"})
	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Error("expected error for path outside virtual root")
	}
}

func TestReadFileTool_SandboxRejectRelative(t *testing.T) {
	dir := t.TempDir()
	vr := sandpkg.VirtualHome("test")
	sandbox := &sandpkg.SandboxConfig{VirtualRoot: vr, Root: dir}
	os.MkdirAll(filepath.Join(dir, "src"), 0755)
	os.WriteFile(filepath.Join(dir, "src", "main.go"), []byte("package main"), 0644)

	tool := NewReadFileTool(sandbox)

	// Relative paths are now auto-resolved under VirtualRoot, so this should succeed
	args, _ := json.Marshal(map[string]string{"path": "src/main.go"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	expected := "# " + sandpkg.PathJoinVirtual(vr, "src/main.go") + "\npackage main"
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestWriteFileTool_SandboxResolve(t *testing.T) {
	dir := t.TempDir()
	vr := sandpkg.VirtualHome("test")
	sandbox := &sandpkg.SandboxConfig{VirtualRoot: vr, Root: dir}

	tool := NewWriteFileTool(sandbox)

	vp := sandpkg.PathJoinVirtual(vr, "output.txt")
	args, _ := json.Marshal(map[string]string{"path": vp, "content": "hello"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	expected := "wrote " + vp
	if result != expected {
		t.Errorf("expected %q, got %s", expected, result)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "output.txt"))
	if string(data) != "hello" {
		t.Errorf("file content mismatch: %s", string(data))
	}
}

func TestWriteFileTool_SandboxRejectOutside(t *testing.T) {
	dir := t.TempDir()
	vr := sandpkg.VirtualHome("test")
	sandbox := &sandpkg.SandboxConfig{VirtualRoot: vr, Root: dir}

	tool := NewWriteFileTool(sandbox)

	// /tmp/evil.txt is auto-resolved to dir/tmp/evil.txt inside the sandbox,
	// so it succeeds (the path is safely contained within the sandbox).
	args, _ := json.Marshal(map[string]string{"path": "/tmp/evil.txt", "content": "bad"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The written file should appear under the auto-resolved virtual path.
	expectedVP := sandpkg.PathJoinVirtual(vr, "tmp/evil.txt")
	expected := "wrote " + expectedVP
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
	data, err := os.ReadFile(filepath.Join(dir, "tmp", "evil.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "bad" {
		t.Errorf("expected 'bad', got %q", string(data))
	}
}

func TestWriteFileTool_SandboxRejectReadonlyPath(t *testing.T) {
	dir := t.TempDir()
	readonlyDir := filepath.Join(dir, "readonly")
	if err := os.MkdirAll(readonlyDir, 0755); err != nil {
		t.Fatal(err)
	}

	vr := sandpkg.VirtualHome("test")
	sandbox := &sandpkg.SandboxConfig{
		VirtualRoot:   vr,
		Root:          dir,
		ReadonlyPaths: []string{readonlyDir},
	}

	tool := NewWriteFileTool(sandbox)
	vp := sandpkg.PathJoinVirtual(vr, "readonly/output.txt")
	args, _ := json.Marshal(map[string]string{"path": vp, "content": "blocked"})

	if _, err := tool.Execute(context.Background(), args); err == nil {
		t.Fatal("expected readonly write to fail")
	}
	if _, err := os.Stat(filepath.Join(readonlyDir, "output.txt")); !os.IsNotExist(err) {
		t.Fatalf("readonly write should not create file, got stat err=%v", err)
	}
}

func TestShellExecTool_SandboxRewriteCommand(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("world"), 0644)

	vr := sandpkg.VirtualHome("test")
	sandbox := &sandpkg.SandboxConfig{
		VirtualRoot:     vr,
		Root:            dir,
		AllowNetwork:    true,
		BlockedCommands: nil,
	}
	tool := NewShellExecTool(
		WithShellWorkdir(dir),
		WithShellSandboxConfig(sandbox),
	)

	// The LLM sends a command using the virtual path; the tool should rewrite it.
	vp := sandpkg.PathJoinVirtual(vr, "hello.txt")
	args, _ := json.Marshal(map[string]string{"command": "cat " + vp})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result = strings.TrimSpace(strings.ReplaceAll(result, "\r\n", "\n"))
	if result != "world" {
		t.Errorf("expected 'world', got %q", result)
	}
}

func TestShellExecTool_SandboxRewriteDir(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "subdir")
	os.MkdirAll(sub, 0755)

	vr := sandpkg.VirtualHome("test")
	sandbox := &sandpkg.SandboxConfig{
		VirtualRoot:     vr,
		Root:            dir,
		AllowNetwork:    true,
		BlockedCommands: nil,
	}
	tool := NewShellExecTool(
		WithShellWorkdir(dir),
		WithShellSandboxConfig(sandbox),
	)

	cmd := "pwd"
	if runtime.GOOS == "windows" {
		cmd = "cd"
	}
	vp := sandpkg.PathJoinVirtual(vr, "subdir")
	args, _ := json.Marshal(map[string]string{"command": cmd, "dir": vp})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result = strings.TrimSpace(strings.ReplaceAll(result, "\r\n", "\n"))

	expected := sandpkg.PathJoinVirtual(vr, "subdir")
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestShellExecTool_BlocksConfiguredCommandAfterAndAnd(t *testing.T) {
	tool := NewShellExecTool(
		WithShellWorkdir(t.TempDir()),
		WithShellSandboxConfig(&sandpkg.SandboxConfig{BlockedCommands: []string{"echo blocked"}}),
	)

	args, _ := json.Marshal(map[string]string{"command": "true&&echo blocked"})
	if _, err := tool.Execute(context.Background(), args); err == nil {
		t.Fatal("expected blocked command after && to fail")
	}
}

func TestShellExecTool_BlocksNetworkCommandAfterAndAnd(t *testing.T) {
	cfg := &sandpkg.SandboxConfig{}
	cfg.SetAllowNetwork(false)
	tool := NewShellExecTool(
		WithShellWorkdir(t.TempDir()),
		WithShellSandboxConfig(cfg),
	)

	args, _ := json.Marshal(map[string]string{"command": "true&&curl --version"})
	if _, err := tool.Execute(context.Background(), args); err == nil {
		t.Fatal("expected network command after && to fail")
	}
}

func TestShellExecTool_SandboxRejectDirOutside(t *testing.T) {
	dir := t.TempDir()

	sandbox := &sandpkg.SandboxConfig{
		VirtualRoot:     sandpkg.VirtualHome("test"),
		Root:            dir,
		AllowNetwork:    true,
		BlockedCommands: nil,
	}
	tool := NewShellExecTool(
		WithShellWorkdir(dir),
		WithShellSandboxConfig(sandbox),
	)

	args, _ := json.Marshal(map[string]string{"command": "pwd", "dir": "/etc"})
	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Error("expected error for dir outside virtual root")
	}
}

func TestShellExecTool_NoSandboxUnchanged(t *testing.T) {
	tool := NewShellExecTool()
	args, _ := json.Marshal(map[string]string{"command": "echo hello"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	result = strings.ReplaceAll(result, "\r\n", "\n")
	if result != "hello\n" {
		t.Errorf("expected 'hello\\n', got %q", result)
	}
}

func TestShellExecTool_Description_Sandbox(t *testing.T) {
	vr := sandpkg.VirtualHome("ws")
	sandbox := &sandpkg.SandboxConfig{VirtualRoot: vr, Root: "/tmp/real"}
	tool := NewShellExecTool(WithShellSandboxConfig(sandbox))
	desc := tool.Description()
	if !strings.Contains(desc, vr) {
		t.Errorf("description should mention VirtualRoot, got %q", desc)
	}
}

func TestShellExecTool_Description_NoSandbox(t *testing.T) {
	tool := NewShellExecTool(WithShellWorkdir("/some/dir"))
	desc := tool.Description()
	if !strings.Contains(desc, "/some/dir") {
		t.Errorf("description should mention workdir, got %q", desc)
	}
}
