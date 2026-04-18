package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sandboxpkg "github.com/cobot-agent/cobot/internal/sandbox"
)

// --- ListDirTool sandbox integration tests ---

func TestListDirTool_SandboxResolve(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "src"), 0755)
	os.WriteFile(filepath.Join(dir, "src", "main.go"), []byte("package main"), 0644)
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# test"), 0644)

	vr := sandboxpkg.VirtualHome("myworkspace")
	sandbox := &sandboxpkg.SandboxConfig{VirtualRoot: vr, Root: dir}
	tool := NewListDirTool(sandbox)

	args, _ := json.Marshal(map[string]string{"path": vr})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "src/") {
		t.Errorf("expected listing to contain 'src/', got %q", result)
	}
	vrREADME := sandboxpkg.PathJoinVirtual(vr, "README.md")
	if !strings.Contains(result, vrREADME) {
		t.Errorf("expected listing to show virtual path %s, got %q", vrREADME, result)
	}
}

func TestListDirTool_SandboxAutoResolveOutside(t *testing.T) {
	dir := t.TempDir()
	sandbox := &sandboxpkg.SandboxConfig{VirtualRoot: sandboxpkg.VirtualHome("myworkspace"), Root: dir}
	tool := NewListDirTool(sandbox)

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

	vr := sandboxpkg.VirtualHome("myworkspace")
	sandbox := &sandboxpkg.SandboxConfig{VirtualRoot: vr, Root: dir}
	tool := NewListDirTool(sandbox)

	// Relative path should auto-resolve under VirtualRoot
	args, _ := json.Marshal(map[string]string{"path": "src"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	vp := sandboxpkg.PathJoinVirtual(vr, "src/main.go")
	if !strings.Contains(result, vp) {
		t.Errorf("expected listing to use virtual path %s, got %q", vp, result)
	}
}

func TestListDirTool_NoSandbox(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte("hello"), 0644)

	tool := NewListDirTool(nil)
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

	vr := sandboxpkg.VirtualHome("myworkspace")
	sandbox := &sandboxpkg.SandboxConfig{VirtualRoot: vr, Root: dir}
	tool := NewSearchFilesTool(sandbox)

	args, _ := json.Marshal(map[string]string{"path": vr, "pattern": "*.go"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	vMain := sandboxpkg.PathJoinVirtual(vr, "src/main.go")
	if !strings.Contains(result, vMain) {
		t.Errorf("expected virtual path in search results, got %q", result)
	}
	vUtil := sandboxpkg.PathJoinVirtual(vr, "src/util.go")
	if !strings.Contains(result, vUtil) {
		t.Errorf("expected virtual path in search results, got %q", result)
	}
}

func TestSearchFilesTool_SandboxAutoResolveOutside(t *testing.T) {
	dir := t.TempDir()
	sandbox := &sandboxpkg.SandboxConfig{VirtualRoot: sandboxpkg.VirtualHome("myworkspace"), Root: dir}
	tool := NewSearchFilesTool(sandbox)

	// /tmp auto-resolves to <vr>/tmp → dir/tmp (doesn't exist)
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

	vr := sandboxpkg.VirtualHome("myworkspace")
	sandbox := &sandboxpkg.SandboxConfig{VirtualRoot: vr, Root: dir}
	tool := NewSearchFilesTool(sandbox)

	// Relative path auto-resolves
	args, _ := json.Marshal(map[string]string{"path": "src", "pattern": "*.go"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	vp := sandboxpkg.PathJoinVirtual(vr, "src/main.go")
	if !strings.Contains(result, vp) {
		t.Errorf("expected virtual path in search results, got %q", result)
	}
}

func TestSearchFilesTool_NoSandbox(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte("hello"), 0644)

	tool := NewSearchFilesTool(nil)
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
	vr := sandboxpkg.VirtualHome("ws")
	sandbox := &sandboxpkg.SandboxConfig{VirtualRoot: vr, Root: "/tmp/real"}
	tool := NewReadFileTool(sandbox)
	desc := tool.Description()
	if !strings.Contains(desc, vr) {
		t.Errorf("description should mention VirtualRoot, got %q", desc)
	}
}

func TestWriteFileTool_Description_Sandbox(t *testing.T) {
	vr := sandboxpkg.VirtualHome("ws")
	sandbox := &sandboxpkg.SandboxConfig{VirtualRoot: vr, Root: "/tmp/real"}
	tool := NewWriteFileTool(sandbox)
	desc := tool.Description()
	if !strings.Contains(desc, vr) {
		t.Errorf("description should mention VirtualRoot, got %q", desc)
	}
}

func TestListDirTool_Description_Sandbox(t *testing.T) {
	vr := sandboxpkg.VirtualHome("ws")
	sandbox := &sandboxpkg.SandboxConfig{VirtualRoot: vr, Root: "/tmp/real"}
	tool := NewListDirTool(sandbox)
	desc := tool.Description()
	if !strings.Contains(desc, vr) {
		t.Errorf("description should mention VirtualRoot, got %q", desc)
	}
}

func TestSearchFilesTool_Description_Sandbox(t *testing.T) {
	vr := sandboxpkg.VirtualHome("ws")
	sandbox := &sandboxpkg.SandboxConfig{VirtualRoot: vr, Root: "/tmp/real"}
	tool := NewSearchFilesTool(sandbox)
	desc := tool.Description()
	if !strings.Contains(desc, vr) {
		t.Errorf("description should mention VirtualRoot, got %q", desc)
	}
}

// --- ReadFileTool virtual path header tests ---

func TestReadFileTool_SandboxVirtualPathHeader(t *testing.T) {
	dir := t.TempDir()
	vr := sandboxpkg.VirtualHome("myworkspace")
	sandbox := &sandboxpkg.SandboxConfig{VirtualRoot: vr, Root: dir}
	os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("world"), 0644)

	tool := NewReadFileTool(sandbox)
	vp := sandboxpkg.PathJoinVirtual(vr, "hello.txt")
	args, _ := json.Marshal(map[string]string{"path": vp})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(result, "# "+vp+"\n") {
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

	tool := NewReadFileTool(nil)
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
	vr := sandboxpkg.VirtualHome("myworkspace")
	sandbox := &sandboxpkg.SandboxConfig{VirtualRoot: vr, Root: dir}

	tool := NewWriteFileTool(sandbox)
	vp := sandboxpkg.PathJoinVirtual(vr, "output.txt")
	args, _ := json.Marshal(map[string]string{"path": vp, "content": "hello"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}

	if result != "wrote "+vp {
		t.Errorf("expected %q, got %q", "wrote "+vp, result)
	}
}

func TestWriteFileTool_NoSandboxOutput(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "output.txt")

	tool := NewWriteFileTool(nil)
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
	vr := sandboxpkg.VirtualHome("myworkspace")
	sandbox := &sandboxpkg.SandboxConfig{VirtualRoot: vr, Root: dir}
	tool := NewReadFileTool(sandbox)

	// /etc/passwd auto-resolves to <vr>/etc/passwd → dir/etc/passwd
	args, _ := json.Marshal(map[string]string{"path": "/etc/passwd"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	vp := sandboxpkg.PathJoinVirtual(vr, "etc/passwd")
	if !strings.HasPrefix(result, "# "+vp+"\n") {
		t.Errorf("expected auto-resolved virtual path header, got %q", result)
	}
}

func TestWriteFileTool_SandboxAutoResolvesAbsolute(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "tmp"), 0755) // create parent dir for auto-resolved path
	vr := sandboxpkg.VirtualHome("myworkspace")
	sandbox := &sandboxpkg.SandboxConfig{VirtualRoot: vr, Root: dir}
	tool := NewWriteFileTool(sandbox)

	// /tmp/output.txt auto-resolves to <vr>/tmp/output.txt
	args, _ := json.Marshal(map[string]string{"path": "/tmp/output.txt", "content": "test"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	vp := sandboxpkg.PathJoinVirtual(vr, "tmp/output.txt")
	if result != "wrote "+vp {
		t.Errorf("expected %q, got %q", "wrote "+vp, result)
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
	vr := sandboxpkg.VirtualHome("myworkspace")
	sandbox := &sandboxpkg.SandboxConfig{VirtualRoot: vr, Root: dir}
	tool := NewListDirTool(sandbox)

	args, _ := json.Marshal(map[string]string{"path": "/var/log"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	vp := sandboxpkg.PathJoinVirtual(vr, "var/log/app.log")
	if !strings.Contains(result, vp) {
		t.Errorf("expected virtual path in listing, got %q", result)
	}
}

func TestSearchFilesTool_SandboxAutoResolvesAbsolute(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "usr", "local"), 0755)
	os.WriteFile(filepath.Join(dir, "usr", "local", "app.go"), []byte("package main"), 0644)
	vr := sandboxpkg.VirtualHome("myworkspace")
	sandbox := &sandboxpkg.SandboxConfig{VirtualRoot: vr, Root: dir}
	tool := NewSearchFilesTool(sandbox)

	args, _ := json.Marshal(map[string]string{"path": "/usr/local", "pattern": "*.go"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	vp := sandboxpkg.PathJoinVirtual(vr, "usr/local/app.go")
	if !strings.Contains(result, vp) {
		t.Errorf("expected virtual path in search results, got %q", result)
	}
}

// --- Real Root path matching test ---

func TestReadFileTool_SandboxRealRootPathMatch(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("world"), 0644)
	vr := sandboxpkg.VirtualHome("ws")
	sandbox := &sandboxpkg.SandboxConfig{VirtualRoot: vr, Root: dir}
	tool := NewReadFileTool(sandbox)

	// LLM accidentally uses the real Root path instead of VirtualRoot
	args, _ := json.Marshal(map[string]string{"path": dir + "/hello.txt"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	vp := sandboxpkg.PathJoinVirtual(vr, "hello.txt")
	if !strings.HasPrefix(result, "# "+vp+"\n") {
		t.Errorf("expected virtual path in output, got %q", result)
	}
}

// --- Relative path auto-resolution tests ---

func TestWriteFileTool_SandboxRelativeAutoResolve(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "src"), 0755)
	vr := sandboxpkg.VirtualHome("myworkspace")
	sandbox := &sandboxpkg.SandboxConfig{VirtualRoot: vr, Root: dir}
	tool := NewWriteFileTool(sandbox)

	// Relative path should auto-resolve under VirtualRoot
	args, _ := json.Marshal(map[string]string{"path": "src/main.go", "content": "package main"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	vp := sandboxpkg.PathJoinVirtual(vr, "src/main.go")
	if result != "wrote "+vp {
		t.Errorf("expected %q, got %q", "wrote "+vp, result)
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
	vr := sandboxpkg.VirtualHome("test")
	sandbox := &sandboxpkg.SandboxConfig{VirtualRoot: vr, Root: t.TempDir()}

	registry := NewRegistry()
	registry.Register(NewReadFileTool(sandbox))
	registry.Register(NewWriteFileTool(sandbox))
	registry.Register(NewListDirTool(sandbox))
	registry.Register(NewSearchFilesTool(sandbox))

	expectedNames := []string{"filesystem_read", "filesystem_write", "filesystem_list", "filesystem_search"}
	for _, name := range expectedNames {
		tool, err := registry.Get(name)
		if err != nil {
			t.Errorf("tool %q not registered: %v", name, err)
			continue
		}
		desc := tool.Description()
		if !strings.Contains(desc, vr) {
			t.Errorf("tool %q description should mention VirtualRoot, got %q", name, desc)
		}
	}
}

func TestAllFilesystemTools_WithoutSandbox(t *testing.T) {
	registry := NewRegistry()
	registry.Register(NewReadFileTool(nil))
	registry.Register(NewWriteFileTool(nil))
	registry.Register(NewListDirTool(nil))
	registry.Register(NewSearchFilesTool(nil))

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
	vr := sandboxpkg.VirtualHome("ws")
	sandbox := &sandboxpkg.SandboxConfig{VirtualRoot: vr, Root: dir}
	tool := NewWriteFileTool(sandbox)

	// Write to a nested path where parent dirs don't exist
	vp := sandboxpkg.PathJoinVirtual(vr, "deep/nested/file.txt")
	args, _ := json.Marshal(map[string]string{"path": vp, "content": "hello"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if result != "wrote "+vp {
		t.Errorf("expected %q, got %q", "wrote "+vp, result)
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
	vr := sandboxpkg.VirtualHome("ws")
	sandbox := &sandboxpkg.SandboxConfig{
		VirtualRoot: vr,
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
	if !strings.Contains(result, vr) && result != "" {
		t.Errorf("expected output with virtual path, got %q", result)
	}
}
