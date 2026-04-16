package tools

import (
	"io/fs"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	cobot "github.com/cobot-agent/cobot/pkg"
)

//go:embed embed_filesystem_read_params.json
var filesystemReadParamsJSON []byte

//go:embed embed_filesystem_write_params.json
var filesystemWriteParamsJSON []byte

type readFileArgs struct {
	Path string `json:"path"`
}

type ReadFileTool struct {
	sandbox *cobot.SandboxConfig
}

type ReadFileToolOption func(*ReadFileTool)

func WithReadSandbox(s *cobot.SandboxConfig) ReadFileToolOption {
	return func(t *ReadFileTool) { t.sandbox = s }
}

func NewReadFileTool(opts ...ReadFileToolOption) *ReadFileTool {
	t := &ReadFileTool{}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

func (t *ReadFileTool) Name() string {
	return "filesystem_read"
}

func (t *ReadFileTool) Description() string {
	desc := "Read the contents of a file at the given path."
	if t.sandbox != nil && t.sandbox.VirtualRoot != "" {
		desc += fmt.Sprintf(" Paths MUST start with %q (e.g. %s/file.txt). Relative paths are auto-resolved under %s.", t.sandbox.VirtualRoot, t.sandbox.VirtualRoot, t.sandbox.VirtualRoot)
	}
	return desc
}

func (t *ReadFileTool) Parameters() json.RawMessage {
	return json.RawMessage(filesystemReadParamsJSON)
}

func (t *ReadFileTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a readFileArgs
	if err := decodeArgs(args, &a); err != nil {
		return "", err
	}
	originalPath := a.Path
	if t.sandbox != nil {
		resolved, err := t.sandbox.AutoResolvePath(a.Path)
		if err != nil {
			return "", err
		}
		a.Path = resolved
	}
	if t.sandbox != nil && !t.sandbox.IsAllowed(a.Path, false) {
		return "", fmt.Errorf("path %q is outside allowed workspace paths", originalPath)
	}
	data, err := os.ReadFile(a.Path)
	if err != nil {
		if t.sandbox != nil {
			return "", fmt.Errorf("%s", t.sandbox.RewriteOutputPaths(err.Error()))
		}
		return "", err
	}
	if t.sandbox != nil && t.sandbox.VirtualRoot != "" {
		virtualPath := t.sandbox.RealToVirtual(a.Path)
		return fmt.Sprintf("# %s\n%s", virtualPath, string(data)), nil
	}
	return string(data), nil
}

type writeFileArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type WriteFileTool struct {
	sandbox *cobot.SandboxConfig
}

type WriteFileToolOption func(*WriteFileTool)

func WithWriteSandbox(s *cobot.SandboxConfig) WriteFileToolOption {
	return func(t *WriteFileTool) { t.sandbox = s }
}

func NewWriteFileTool(opts ...WriteFileToolOption) *WriteFileTool {
	t := &WriteFileTool{}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

func (t *WriteFileTool) Name() string {
	return "filesystem_write"
}

func (t *WriteFileTool) Description() string {
	desc := "Write content to a file at the given path."
	if t.sandbox != nil && t.sandbox.VirtualRoot != "" {
		desc += fmt.Sprintf(" Paths MUST start with %q (e.g. %s/file.txt). Relative paths are auto-resolved under %s.", t.sandbox.VirtualRoot, t.sandbox.VirtualRoot, t.sandbox.VirtualRoot)
	}
	return desc
}

func (t *WriteFileTool) Parameters() json.RawMessage {
	return json.RawMessage(filesystemWriteParamsJSON)
}

func (t *WriteFileTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a writeFileArgs
	if err := decodeArgs(args, &a); err != nil {
		return "", err
	}
	originalPath := a.Path
	if t.sandbox != nil {
		resolved, err := t.sandbox.AutoResolvePath(a.Path)
		if err != nil {
			return "", err
		}
		a.Path = resolved
	}
	if t.sandbox != nil && !t.sandbox.IsAllowed(a.Path, true) {
		return "", fmt.Errorf("path %q is outside allowed workspace paths", originalPath)
	}
	if err := os.WriteFile(a.Path, []byte(a.Content), 0644); err != nil {
		if t.sandbox != nil {
			return "", fmt.Errorf("%s", t.sandbox.RewriteOutputPaths(err.Error()))
		}
		return "", err
	}
	if t.sandbox != nil && t.sandbox.VirtualRoot != "" {
		virtualPath := t.sandbox.RealToVirtual(a.Path)
		return fmt.Sprintf("wrote %s", virtualPath), nil
	}
	return "ok", nil
}

var (
	_ cobot.Tool = (*ReadFileTool)(nil)
	_ cobot.Tool = (*WriteFileTool)(nil)
)
//go:embed embed_filesystem_list_params.json
var filesystemListParamsJSON []byte

//go:embed embed_filesystem_search_params.json
var filesystemSearchParamsJSON []byte

type listDirArgs struct {
	Path    string `json:"path"`
	Pattern string `json:"pattern,omitempty"`
}

type ListDirTool struct {
	sandbox *cobot.SandboxConfig
}

type ListDirToolOption func(*ListDirTool)

func WithListSandbox(s *cobot.SandboxConfig) ListDirToolOption {
	return func(t *ListDirTool) { t.sandbox = s }
}

func NewListDirTool(opts ...ListDirToolOption) *ListDirTool {
	t := &ListDirTool{}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

func (t *ListDirTool) Name() string { return "filesystem_list" }

func (t *ListDirTool) Description() string {
	desc := "List files and directories at the given path."
	if t.sandbox != nil && t.sandbox.VirtualRoot != "" {
		desc += fmt.Sprintf(" Paths MUST start with %q (e.g. %s/file.txt). Relative paths are auto-resolved under %s.", t.sandbox.VirtualRoot, t.sandbox.VirtualRoot, t.sandbox.VirtualRoot)
	}
	return desc
}

func (t *ListDirTool) Parameters() json.RawMessage {
	return json.RawMessage(filesystemListParamsJSON)
}

func (t *ListDirTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a listDirArgs
	if err := decodeArgs(args, &a); err != nil {
		return "", err
	}
	originalPath := a.Path
	if t.sandbox != nil {
		resolved, err := t.sandbox.AutoResolvePath(a.Path)
		if err != nil {
			return "", err
		}
		a.Path = resolved
	}
	if t.sandbox != nil && !t.sandbox.IsAllowed(a.Path, false) {
		return "", fmt.Errorf("path %q is outside allowed workspace paths", originalPath)
	}

	entries, err := os.ReadDir(a.Path)
	if err != nil {
		if t.sandbox != nil {
			return "", fmt.Errorf("%s", t.sandbox.RewriteOutputPaths(err.Error()))
		}
		return "", err
	}

	// When sandbox is active, compute the virtual path prefix for display
	virtualPrefix := ""
	if t.sandbox != nil && t.sandbox.VirtualRoot != "" {
		virtualPrefix = t.sandbox.RealToVirtual(a.Path)
	}

	var lines []string
	for _, entry := range entries {
		name := entry.Name()
		if a.Pattern != "" {
			matched, _ := filepath.Match(a.Pattern, name)
			if !matched {
				continue
			}
		}
		displayName := name
		if virtualPrefix != "" {
			displayName = virtualPrefix + "/" + name
		}
		if entry.IsDir() {
			lines = append(lines, displayName+"/")
		} else {
			info, err := entry.Info()
			if err != nil {
				lines = append(lines, displayName)
			} else {
				lines = append(lines, fmt.Sprintf("%s (%d bytes)", displayName, info.Size()))
			}
		}
	}

	if len(lines) == 0 {
		return "empty directory", nil
	}
	return strings.Join(lines, "\n"), nil
}

type searchFilesArgs struct {
	Path    string `json:"path"`
	Pattern string `json:"pattern"`
}

type SearchFilesTool struct {
	sandbox *cobot.SandboxConfig
}

type SearchFilesToolOption func(*SearchFilesTool)

func WithSearchSandbox(s *cobot.SandboxConfig) SearchFilesToolOption {
	return func(t *SearchFilesTool) { t.sandbox = s }
}

func NewSearchFilesTool(opts ...SearchFilesToolOption) *SearchFilesTool {
	t := &SearchFilesTool{}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

func (t *SearchFilesTool) Name() string { return "filesystem_search" }

func (t *SearchFilesTool) Description() string {
	desc := "Search for files matching a pattern recursively from a root directory."
	if t.sandbox != nil && t.sandbox.VirtualRoot != "" {
		desc += fmt.Sprintf(" Paths MUST start with %q (e.g. %s/file.txt). Relative paths are auto-resolved under %s.", t.sandbox.VirtualRoot, t.sandbox.VirtualRoot, t.sandbox.VirtualRoot)
	}
	return desc
}

func (t *SearchFilesTool) Parameters() json.RawMessage {
	return json.RawMessage(filesystemSearchParamsJSON)
}

func (t *SearchFilesTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a searchFilesArgs
	if err := decodeArgs(args, &a); err != nil {
		return "", err
	}
	originalPath := a.Path
	if t.sandbox != nil {
		resolved, err := t.sandbox.AutoResolvePath(a.Path)
		if err != nil {
			return "", err
		}
		a.Path = resolved
	}
	if t.sandbox != nil && !t.sandbox.IsAllowed(a.Path, false) {
		return "", fmt.Errorf("path %q is outside allowed workspace paths", originalPath)
	}

	var matches []string
	err := filepath.WalkDir(a.Path, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip errors
		}
		matched, _ := filepath.Match(a.Pattern, d.Name())
		if matched {
			displayPath := path
			if t.sandbox != nil && t.sandbox.VirtualRoot != "" {
				displayPath = t.sandbox.RealToVirtual(path)
			}
			if d.IsDir() {
				matches = append(matches, displayPath+"/")
			} else {
				matches = append(matches, displayPath)
			}
		}
		return nil
	})
	if err != nil {
		if t.sandbox != nil {
			return "", fmt.Errorf("%s", t.sandbox.RewriteOutputPaths(err.Error()))
		}
		return "", err
	}

	if len(matches) == 0 {
		return "no files found matching pattern", nil
	}
	return strings.Join(matches, "\n"), nil
}

var (
	_ cobot.Tool = (*ListDirTool)(nil)
	_ cobot.Tool = (*SearchFilesTool)(nil)
)
