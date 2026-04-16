package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cobot "github.com/cobot-agent/cobot/pkg"
)

func TestReadFileTool(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "test.txt")
	os.WriteFile(f, []byte("hello world"), 0644)

	tool := NewReadFileTool()
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

	tool := NewWriteFileTool()
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
	tool := NewReadFileTool()
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
	sandbox := &cobot.SandboxConfig{VirtualRoot: "/home/test", Root: dir}
	os.MkdirAll(filepath.Join(dir, "src"), 0755)
	os.WriteFile(filepath.Join(dir, "src", "main.go"), []byte("package main"), 0644)

	tool := NewReadFileTool(WithReadSandbox(sandbox))

	// Virtual path resolves correctly
	args, _ := json.Marshal(map[string]string{"path": "/home/test/src/main.go"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if result != "package main" {
		t.Errorf("expected 'package main', got %s", result)
	}
}

func TestReadFileTool_SandboxRejectOutside(t *testing.T) {
	dir := t.TempDir()
	sandbox := &cobot.SandboxConfig{VirtualRoot: "/home/test", Root: dir}

	tool := NewReadFileTool(WithReadSandbox(sandbox))

	args, _ := json.Marshal(map[string]string{"path": "/etc/passwd"})
	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Error("expected error for path outside virtual root")
	}
}

func TestReadFileTool_SandboxRejectRelative(t *testing.T) {
	dir := t.TempDir()
	sandbox := &cobot.SandboxConfig{VirtualRoot: "/home/test", Root: dir}

	tool := NewReadFileTool(WithReadSandbox(sandbox))

	args, _ := json.Marshal(map[string]string{"path": "src/main.go"})
	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Error("expected error for relative path")
	}
}

func TestWriteFileTool_SandboxResolve(t *testing.T) {
	dir := t.TempDir()
	sandbox := &cobot.SandboxConfig{VirtualRoot: "/home/test", Root: dir}

	tool := NewWriteFileTool(WithWriteSandbox(sandbox))

	args, _ := json.Marshal(map[string]string{"path": "/home/test/output.txt", "content": "hello"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if result != "ok" {
		t.Errorf("expected ok, got %s", result)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "output.txt"))
	if string(data) != "hello" {
		t.Errorf("file content mismatch: %s", string(data))
	}
}

func TestWriteFileTool_SandboxRejectOutside(t *testing.T) {
	dir := t.TempDir()
	sandbox := &cobot.SandboxConfig{VirtualRoot: "/home/test", Root: dir}

	tool := NewWriteFileTool(WithWriteSandbox(sandbox))

	args, _ := json.Marshal(map[string]string{"path": "/tmp/evil.txt", "content": "bad"})
	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Error("expected error for path outside virtual root")
	}
}

func TestShellExecTool_SandboxRewriteCommand(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("world"), 0644)

	sandbox := &cobot.SandboxConfig{
		VirtualRoot:     "/home/test",
		Root:            dir,
		AllowNetwork:    true,
		BlockedCommands: nil,
	}
	tool := NewShellExecTool(
		WithShellWorkdir(dir),
		WithShellSandboxConfig(sandbox),
	)

	// The LLM sends a command using the virtual path; the tool should rewrite it.
	args, _ := json.Marshal(map[string]string{"command": "cat /home/test/hello.txt"})
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

	sandbox := &cobot.SandboxConfig{
		VirtualRoot:     "/home/test",
		Root:            dir,
		AllowNetwork:    true,
		BlockedCommands: nil,
	}
	tool := NewShellExecTool(
		WithShellWorkdir(dir),
		WithShellSandboxConfig(sandbox),
	)

	// The LLM sends dir as a virtual path; the tool should resolve it.
	args, _ := json.Marshal(map[string]string{"command": "pwd", "dir": "/home/test/subdir"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result = strings.TrimSpace(strings.ReplaceAll(result, "\r\n", "\n"))

	// Resolve symlinks for comparison (macOS /var → /private/var)
	expected, err := filepath.Abs(sub)
	if err != nil {
		t.Fatal(err)
	}
	expected, err = filepath.EvalSymlinks(expected)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := filepath.Abs(result)
	if err != nil {
		t.Fatal(err)
	}
	actual, err = filepath.EvalSymlinks(actual)
	if err != nil {
		t.Fatal(err)
	}
	if actual != expected {
		t.Errorf("expected %q, got %q", expected, actual)
	}
}

func TestShellExecTool_SandboxRejectDirOutside(t *testing.T) {
	dir := t.TempDir()

	sandbox := &cobot.SandboxConfig{
		VirtualRoot:     "/home/test",
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
	sandbox := &cobot.SandboxConfig{VirtualRoot: "/home/ws", Root: "/tmp/real"}
	tool := NewShellExecTool(WithShellSandboxConfig(sandbox))
	desc := tool.Description()
	if !strings.Contains(desc, "/home/ws") {
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
