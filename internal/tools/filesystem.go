package tools

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	cobot "github.com/cobot-agent/cobot/pkg"
)

//go:embed schemas/embed_filesystem_read_params.json
var filesystemReadParamsJSON []byte

//go:embed schemas/embed_filesystem_write_params.json
var filesystemWriteParamsJSON []byte

//go:embed schemas/embed_filesystem_list_params.json
var filesystemListParamsJSON []byte

type readFileArgs struct {
	Path string `json:"path"`
}

type ReadFileTool struct{ BasicTool }

func NewReadFileTool(sandbox *cobot.SandboxConfig) *ReadFileTool {
	return &ReadFileTool{BasicTool{
		sandboxTool: sandboxTool{sandbox: sandbox},
		name:        "filesystem_read",
		desc:        "Read the contents of a file at the given path.",
		params:      filesystemReadParamsJSON,
	}}
}

func (t *ReadFileTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a readFileArgs
	if err := decodeArgs(args, &a); err != nil {
		return "", err
	}
	if resolved, err := sandboxResolvePath(t.sandbox, a.Path); err != nil {
		return "", err
	} else {
		a.Path = resolved
	}
	data, err := os.ReadFile(a.Path)
	if err != nil {
		return "", sandboxRewriteErr(t.sandbox, err)
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

type WriteFileTool struct{ BasicTool }

func NewWriteFileTool(sandbox *cobot.SandboxConfig) *WriteFileTool {
	return &WriteFileTool{BasicTool{
		sandboxTool: sandboxTool{sandbox: sandbox},
		name:        "filesystem_write",
		desc:        "Write content to a file at the given path.",
		params:      filesystemWriteParamsJSON,
	}}
}

func (t *WriteFileTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a writeFileArgs
	if err := decodeArgs(args, &a); err != nil {
		return "", err
	}
	if resolved, err := sandboxResolvePath(t.sandbox, a.Path); err != nil {
		return "", err
	} else {
		a.Path = resolved
	}
	if dir := filepath.Dir(a.Path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return "", sandboxRewriteErr(t.sandbox, err)
		}
	}
	if err := os.WriteFile(a.Path, []byte(a.Content), 0644); err != nil {
		return "", sandboxRewriteErr(t.sandbox, err)
	}
	if t.sandbox != nil && t.sandbox.VirtualRoot != "" {
		virtualPath := t.sandbox.RealToVirtual(a.Path)
		return fmt.Sprintf("wrote %s", virtualPath), nil
	}
	return "ok", nil
}

type listDirArgs struct {
	Path    string `json:"path"`
	Pattern string `json:"pattern,omitempty"`
}

type ListDirTool struct{ BasicTool }

func NewListDirTool(sandbox *cobot.SandboxConfig) *ListDirTool {
	return &ListDirTool{BasicTool{
		sandboxTool: sandboxTool{sandbox: sandbox},
		name:        "filesystem_list",
		desc:        "List files and directories at the given path.",
		params:      filesystemListParamsJSON,
	}}
}

func (t *ListDirTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a listDirArgs
	if err := decodeArgs(args, &a); err != nil {
		return "", err
	}
	if resolved, err := sandboxResolvePath(t.sandbox, a.Path); err != nil {
		return "", err
	} else {
		a.Path = resolved
	}

	entries, err := os.ReadDir(a.Path)
	if err != nil {
		return "", sandboxRewriteErr(t.sandbox, err)
	}

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

var (
	_ cobot.Tool = (*ReadFileTool)(nil)
	_ cobot.Tool = (*WriteFileTool)(nil)
	_ cobot.Tool = (*ListDirTool)(nil)
)
