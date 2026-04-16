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

func TestListDirTool_SandboxAutoResolveOutside(t *testing.T) {
	dir := t.TempDir()
	sandbox := &cobot.SandboxConfig{VirtualRoot: "/home/myworkspace", Root: dir}
	tool := NewListDirTool(WithListSandbox(sandbox))

	args, _ := json.Marshal(map[string]string{"path": "/etc"})
	_, err := tool.Execute(context.Background(), args)
	// Path is auto-resolved inside sandbox; since /etc doesn't exist there, expect file-not-found
	if err == nil {
		t.Error("expected error for non-existent auto-resolved path")
	}
	// Should NOT be a sandbox path rejection error
	errStr := err.Error()
	if strings.Contains(errStr, "must start with") || strings.Contains(errStr, "outside allowed") {
		t.Errorf("should not be a path rejection error, got: %q", errStr)
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

func TestSearchFilesTool_SandboxAutoResolveOutside(t *testing.T) {
	dir := t.TempDir()
	sandbox := &cobot.SandboxConfig{VirtualRoot: "/home/myworkspace", Root: dir}
	tool := NewSearchFilesTool(WithSearchSandbox(sandbox))

	// /tmp auto-resolves to /home/myworkspace/tmp → dir/tmp (doesn't exist)
	args, _ := json.Marshal(map[string]string{"path": "/tmp", "pattern": "*.txt"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// WalkDir on non-existent path returns error, but SearchFiles returns "no files found"
	if result != "no files found matching pattern" {
		t.Errorf("expected 'no files found matching pattern', got %q", result)
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

// --- Absolute path auto-resolution tests (paths are auto-resolved inside sandbox) ---

func TestReadFileTool_SandboxAutoResolvesAbsolute(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "etc"), 0755)
	os.WriteFile(filepath.Join(dir, "etc", "passwd"), []byte("root:x:0:0"), 0644)
	sandbox := &cobot.SandboxConfig{VirtualRoot: "/home/myworkspace", Root: dir}
	tool := NewReadFileTool(WithReadSandbox(sandbox))

	// /etc/passwd auto-resolves to /home/myworkspace/etc/passwd → dir/etc/passwd
	args, _ := json.Marshal(map[string]string{"path": "/etc/passwd"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(result, "# /home/myworkspace/etc/passwd\n") {
		t.Errorf("expected auto-resolved virtual path header, got %q", result)
	}
}

func TestWriteFileTool_SandboxAutoResolvesAbsolute(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "tmp"), 0755) // create parent dir for auto-resolved path
	sandbox := &cobot.SandboxConfig{VirtualRoot: "/home/myworkspace", Root: dir}
	tool := NewWriteFileTool(WithWriteSandbox(sandbox))

	// /tmp/output.txt auto-resolves to /home/myworkspace/tmp/output.txt
	args, _ := json.Marshal(map[string]string{"path": "/tmp/output.txt", "content": "test"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if result != "wrote /home/myworkspace/tmp/output.txt" {
		t.Errorf("expected 'wrote /home/myworkspace/tmp/output.txt', got %q", result)
	}
	// Verify file was written at auto-resolved real path
	data, err := os.ReadFile(filepath.Join(dir, "tmp", "output.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "test" {
		t.Errorf("expected file content 'test', got %q", string(data))
	}
}

func TestListDirTool_SandboxAutoResolvesAbsolute(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "var", "log"), 0755)
	os.WriteFile(filepath.Join(dir, "var", "log", "app.log"), []byte("log data"), 0644)
	sandbox := &cobot.SandboxConfig{VirtualRoot: "/home/myworkspace", Root: dir}
	tool := NewListDirTool(WithListSandbox(sandbox))

	args, _ := json.Marshal(map[string]string{"path": "/var/log"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "/home/myworkspace/var/log/app.log") {
		t.Errorf("expected virtual path in listing, got %q", result)
	}
}

func TestSearchFilesTool_SandboxAutoResolvesAbsolute(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "usr", "local"), 0755)
	os.WriteFile(filepath.Join(dir, "usr", "local", "app.go"), []byte("package main"), 0644)
	sandbox := &cobot.SandboxConfig{VirtualRoot: "/home/myworkspace", Root: dir}
	tool := NewSearchFilesTool(WithSearchSandbox(sandbox))

	args, _ := json.Marshal(map[string]string{"path": "/usr/local", "pattern": "*.go"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "/home/myworkspace/usr/local/app.go") {
		t.Errorf("expected virtual path in search results, got %q", result)
	}
}

// --- Real Root path matching test ---

func TestReadFileTool_SandboxRealRootPathMatch(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("world"), 0644)
	sandbox := &cobot.SandboxConfig{VirtualRoot: "/home/ws", Root: dir}
	tool := NewReadFileTool(WithReadSandbox(sandbox))

	// LLM accidentally uses the real Root path instead of VirtualRoot
	args, _ := json.Marshal(map[string]string{"path": dir + "/hello.txt"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(result, "# /home/ws/hello.txt\n") {
		t.Errorf("expected virtual path in output, got %q", result)
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

// --- WriteFileTool auto-create parent dirs test ---

func TestWriteFileTool_SandboxCreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	sandbox := &cobot.SandboxConfig{VirtualRoot: "/home/ws", Root: dir}
	tool := NewWriteFileTool(WithWriteSandbox(sandbox))

	// Write to a nested path where parent dirs don't exist
	args, _ := json.Marshal(map[string]string{"path": "/home/ws/deep/nested/file.txt", "content": "hello"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if result != "wrote /home/ws/deep/nested/file.txt" {
		t.Errorf("expected 'wrote /home/ws/deep/nested/file.txt', got %q", result)
	}
	data, err := os.ReadFile(filepath.Join(dir, "deep", "nested", "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Errorf("expected 'hello', got %q", string(data))
	}
}

// --- ShellExecTool sandbox auto-resolve dir test ---

func TestShellExecTool_SandboxAutoResolvesDir(t *testing.T) {
	dir := t.TempDir()
	// Create the "src" directory so the shell can chdir into it.
	os.MkdirAll(filepath.Join(dir, "src"), 0755)
	sandbox := &cobot.SandboxConfig{
		VirtualRoot: "/home/ws",
		Root:        dir,
	}
	tool := NewShellExecTool(
		WithShellWorkdir(dir),
		WithShellSandboxConfig(sandbox),
	)

	// LLM passes a relative dir — should auto-resolve
	args, _ := json.Marshal(map[string]string{"command": "pwd", "dir": "src"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The output should contain the virtual path (RewriteOutputPaths rewrites real → virtual)
	if !strings.Contains(result, "/home/ws") && result != "" {
		t.Errorf("expected output with virtual path, got %q", result)
	}
}
