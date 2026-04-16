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

// --- ListDirTool sandbox integration tests ---

func TestListDirTool_SandboxResolve(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "src"), 0755)
	os.WriteFile(filepath.Join(dir, "src", "main.go"), []byte("package main"), 0644)
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# test"), 0644)

	sandbox := &cobot.SandboxConfig{VirtualRoot: "/home/myworkspace", Root: dir}
	tool := NewListDirTool(WithListSandbox(sandbox))

	args, _ := json.Marshal(map[string]string{"path": "/home/myworkspace"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "src/") {
		t.Errorf("expected listing to contain 'src/', got %q", result)
	}
	if !strings.Contains(result, "/home/myworkspace/README.md") {
		t.Errorf("expected listing to show virtual path /home/myworkspace/README.md, got %q", result)
	}
}

func TestListDirTool_SandboxRejectOutside(t *testing.T) {
	dir := t.TempDir()
	sandbox := &cobot.SandboxConfig{VirtualRoot: "/home/myworkspace", Root: dir}
	tool := NewListDirTool(WithListSandbox(sandbox))

	args, _ := json.Marshal(map[string]string{"path": "/etc"})
	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Error("expected error for path outside virtual root")
	}
}

func TestListDirTool_SandboxRelativePath(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "src"), 0755)
	os.WriteFile(filepath.Join(dir, "src", "main.go"), []byte("package main"), 0644)

	sandbox := &cobot.SandboxConfig{VirtualRoot: "/home/myworkspace", Root: dir}
	tool := NewListDirTool(WithListSandbox(sandbox))

	// Relative path should auto-resolve under VirtualRoot
	args, _ := json.Marshal(map[string]string{"path": "src"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "/home/myworkspace/src/main.go") {
		t.Errorf("expected listing to use virtual paths, got %q", result)
	}
}

func TestListDirTool_NoSandbox(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte("hello"), 0644)

	tool := NewListDirTool()
	args, _ := json.Marshal(map[string]string{"path": dir})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "test.txt") {
		t.Errorf("expected listing to contain 'test.txt', got %q", result)
	}
}

// --- SearchFilesTool sandbox integration tests ---

func TestSearchFilesTool_SandboxResolve(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "src"), 0755)
	os.WriteFile(filepath.Join(dir, "src", "main.go"), []byte("package main"), 0644)
	os.WriteFile(filepath.Join(dir, "src", "util.go"), []byte("package main"), 0644)

	sandbox := &cobot.SandboxConfig{VirtualRoot: "/home/myworkspace", Root: dir}
	tool := NewSearchFilesTool(WithSearchSandbox(sandbox))

	args, _ := json.Marshal(map[string]string{"path": "/home/myworkspace", "pattern": "*.go"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "/home/myworkspace/src/main.go") {
		t.Errorf("expected virtual path in search results, got %q", result)
	}
	if !strings.Contains(result, "/home/myworkspace/src/util.go") {
		t.Errorf("expected virtual path in search results, got %q", result)
	}
}

func TestSearchFilesTool_SandboxRejectOutside(t *testing.T) {
	dir := t.TempDir()
	sandbox := &cobot.SandboxConfig{VirtualRoot: "/home/myworkspace", Root: dir}
	tool := NewSearchFilesTool(WithSearchSandbox(sandbox))

	args, _ := json.Marshal(map[string]string{"path": "/tmp", "pattern": "*.txt"})
	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Error("expected error for path outside virtual root")
	}
}

func TestSearchFilesTool_SandboxRelativePath(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "src"), 0755)
	os.WriteFile(filepath.Join(dir, "src", "main.go"), []byte("package main"), 0644)

	sandbox := &cobot.SandboxConfig{VirtualRoot: "/home/myworkspace", Root: dir}
	tool := NewSearchFilesTool(WithSearchSandbox(sandbox))

	// Relative path auto-resolves
	args, _ := json.Marshal(map[string]string{"path": "src", "pattern": "*.go"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "/home/myworkspace/src/main.go") {
		t.Errorf("expected virtual path in search results, got %q", result)
	}
}

func TestSearchFilesTool_NoSandbox(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte("hello"), 0644)

	tool := NewSearchFilesTool()
	args, _ := json.Marshal(map[string]string{"path": dir, "pattern": "*.txt"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "test.txt") {
		t.Errorf("expected search results to contain 'test.txt', got %q", result)
	}
}

// --- Tool Description sandbox tests ---

func TestReadFileTool_Description_Sandbox(t *testing.T) {
	sandbox := &cobot.SandboxConfig{VirtualRoot: "/home/ws", Root: "/tmp/real"}
	tool := NewReadFileTool(WithReadSandbox(sandbox))
	desc := tool.Description()
	if !strings.Contains(desc, "/home/ws") {
		t.Errorf("description should mention VirtualRoot, got %q", desc)
	}
}

func TestWriteFileTool_Description_Sandbox(t *testing.T) {
	sandbox := &cobot.SandboxConfig{VirtualRoot: "/home/ws", Root: "/tmp/real"}
	tool := NewWriteFileTool(WithWriteSandbox(sandbox))
	desc := tool.Description()
	if !strings.Contains(desc, "/home/ws") {
		t.Errorf("description should mention VirtualRoot, got %q", desc)
	}
}

func TestListDirTool_Description_Sandbox(t *testing.T) {
	sandbox := &cobot.SandboxConfig{VirtualRoot: "/home/ws", Root: "/tmp/real"}
	tool := NewListDirTool(WithListSandbox(sandbox))
	desc := tool.Description()
	if !strings.Contains(desc, "/home/ws") {
		t.Errorf("description should mention VirtualRoot, got %q", desc)
	}
}

func TestSearchFilesTool_Description_Sandbox(t *testing.T) {
	sandbox := &cobot.SandboxConfig{VirtualRoot: "/home/ws", Root: "/tmp/real"}
	tool := NewSearchFilesTool(WithSearchSandbox(sandbox))
	desc := tool.Description()
	if !strings.Contains(desc, "/home/ws") {
		t.Errorf("description should mention VirtualRoot, got %q", desc)
	}
}

// --- ReadFileTool virtual path header tests ---

func TestReadFileTool_SandboxVirtualPathHeader(t *testing.T) {
	dir := t.TempDir()
	sandbox := &cobot.SandboxConfig{VirtualRoot: "/home/myworkspace", Root: dir}
	os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("world"), 0644)

	tool := NewReadFileTool(WithReadSandbox(sandbox))
	args, _ := json.Marshal(map[string]string{"path": "/home/myworkspace/hello.txt"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(result, "# /home/myworkspace/hello.txt\n") {
		t.Errorf("expected virtual path header, got %q", result)
	}
	if !strings.Contains(result, "world") {
		t.Errorf("expected file content in output, got %q", result)
	}
}

func TestReadFileTool_NoSandboxNoHeader(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "test.txt")
	os.WriteFile(f, []byte("hello world"), 0644)

	tool := NewReadFileTool()
	args, _ := json.Marshal(map[string]string{"path": f})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(result, "# ") {
		t.Errorf("non-sandbox mode should NOT prepend virtual path header, got %q", result)
	}
	if result != "hello world" {
		t.Errorf("expected 'hello world', got %q", result)
	}
}

// --- WriteFileTool virtual path output tests ---

func TestWriteFileTool_SandboxVirtualPathOutput(t *testing.T) {
	dir := t.TempDir()
	sandbox := &cobot.SandboxConfig{VirtualRoot: "/home/myworkspace", Root: dir}

	tool := NewWriteFileTool(WithWriteSandbox(sandbox))
	args, _ := json.Marshal(map[string]string{"path": "/home/myworkspace/output.txt", "content": "hello"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}

	if result != "wrote /home/myworkspace/output.txt" {
		t.Errorf("expected 'wrote /home/myworkspace/output.txt', got %q", result)
	}
}

func TestWriteFileTool_NoSandboxOutput(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "output.txt")

	tool := NewWriteFileTool()
	args, _ := json.Marshal(map[string]string{"path": f, "content": "written content"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if result != "ok" {
		t.Errorf("non-sandbox mode should return 'ok', got %q", result)
	}
}

// --- Absolute non-virtual path rejection tests ---

func TestReadFileTool_SandboxRejectsAbsoluteNonVirtual(t *testing.T) {
	dir := t.TempDir()
	sandbox := &cobot.SandboxConfig{VirtualRoot: "/home/myworkspace", Root: dir}
	tool := NewReadFileTool(WithReadSandbox(sandbox))

	args, _ := json.Marshal(map[string]string{"path": "/etc/passwd"})
	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Error("expected error for absolute path outside virtual root")
	}
	if !strings.Contains(err.Error(), "/home/myworkspace") {
		t.Errorf("error should suggest using virtual root, got %q", err.Error())
	}
}

func TestWriteFileTool_SandboxRejectsAbsoluteNonVirtual(t *testing.T) {
	dir := t.TempDir()
	sandbox := &cobot.SandboxConfig{VirtualRoot: "/home/myworkspace", Root: dir}
	tool := NewWriteFileTool(WithWriteSandbox(sandbox))

	args, _ := json.Marshal(map[string]string{"path": "/tmp/evil.txt", "content": "bad"})
	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Error("expected error for absolute path outside virtual root")
	}
}

func TestListDirTool_SandboxRejectsAbsoluteNonVirtual(t *testing.T) {
	dir := t.TempDir()
	sandbox := &cobot.SandboxConfig{VirtualRoot: "/home/myworkspace", Root: dir}
	tool := NewListDirTool(WithListSandbox(sandbox))

	args, _ := json.Marshal(map[string]string{"path": "/var/log"})
	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Error("expected error for absolute path outside virtual root")
	}
}

func TestSearchFilesTool_SandboxRejectsAbsoluteNonVirtual(t *testing.T) {
	dir := t.TempDir()
	sandbox := &cobot.SandboxConfig{VirtualRoot: "/home/myworkspace", Root: dir}
	tool := NewSearchFilesTool(WithSearchSandbox(sandbox))

	args, _ := json.Marshal(map[string]string{"path": "/usr/local", "pattern": "*.go"})
	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Error("expected error for absolute path outside virtual root")
	}
}

// --- Relative path auto-resolution tests ---

func TestWriteFileTool_SandboxRelativeAutoResolve(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "src"), 0755)
	sandbox := &cobot.SandboxConfig{VirtualRoot: "/home/myworkspace", Root: dir}
	tool := NewWriteFileTool(WithWriteSandbox(sandbox))

	// Relative path should auto-resolve under VirtualRoot
	args, _ := json.Marshal(map[string]string{"path": "src/main.go", "content": "package main"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if result != "wrote /home/myworkspace/src/main.go" {
		t.Errorf("expected 'wrote /home/myworkspace/src/main.go', got %q", result)
	}

	data, err := os.ReadFile(filepath.Join(dir, "src", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "package main" {
		t.Errorf("file content mismatch: %s", string(data))
	}
}

// --- Registration test: ensure all tools are instantiable with and without sandbox ---

func TestAllFilesystemTools_WithSandbox(t *testing.T) {
	sandbox := &cobot.SandboxConfig{VirtualRoot: "/home/test", Root: t.TempDir()}

	registry := NewRegistry()
	registry.Register(NewReadFileTool(WithReadSandbox(sandbox)))
	registry.Register(NewWriteFileTool(WithWriteSandbox(sandbox)))
	registry.Register(NewListDirTool(WithListSandbox(sandbox)))
	registry.Register(NewSearchFilesTool(WithSearchSandbox(sandbox)))

	expectedNames := []string{"filesystem_read", "filesystem_write", "filesystem_list", "filesystem_search"}
	for _, name := range expectedNames {
		tool, err := registry.Get(name)
		if err != nil {
			t.Errorf("tool %q not registered: %v", name, err)
			continue
		}
		desc := tool.Description()
		if !strings.Contains(desc, "/home/test") {
			t.Errorf("tool %q description should mention VirtualRoot, got %q", name, desc)
		}
	}
}

func TestAllFilesystemTools_WithoutSandbox(t *testing.T) {
	registry := NewRegistry()
	registry.Register(NewReadFileTool())
	registry.Register(NewWriteFileTool())
	registry.Register(NewListDirTool())
	registry.Register(NewSearchFilesTool())

	expectedNames := []string{"filesystem_read", "filesystem_write", "filesystem_list", "filesystem_search"}
	for _, name := range expectedNames {
		_, err := registry.Get(name)
		if err != nil {
			t.Errorf("tool %q not registered: %v", name, err)
		}
	}
}
