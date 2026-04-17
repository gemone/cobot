package tools

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	cobot "github.com/cobot-agent/cobot/pkg"
)

// sandboxResolvePath resolves and validates a path within the sandbox.
// If sandbox is nil, the path is returned unchanged.
// If sandbox is active, AutoResolvePath is called to map virtual/relative/absolute
// paths into the sandbox, then ValidatePath ensures the resolved path stays within bounds.
func sandboxResolvePath(sandbox *cobot.SandboxConfig, path string) (string, error) {
	if sandbox == nil {
		return path, nil
	}
	originalPath := path
	resolved, err := sandbox.AutoResolvePath(path)
	if err != nil {
		return "", err
	}
	if err := sandbox.ValidatePath(resolved); err != nil {
		return "", fmt.Errorf("path %q is outside allowed workspace paths", originalPath)
	}
	return resolved, nil
}

// sandboxTool provides common sandbox functionality for filesystem tools.
type sandboxTool struct {
	sandbox *cobot.SandboxConfig
}

// describeWithSandbox appends the sandbox notice to a tool description.
func (s *sandboxTool) describeWithSandbox(desc string) string {
	if s.sandbox != nil && s.sandbox.VirtualRoot != "" {
		return desc + fmt.Sprintf(" Sandbox is active. All file paths are automatically resolved under %q — provide paths starting with %q for best results. Relative paths and other absolute paths are auto-mapped into the sandbox.", s.sandbox.VirtualRoot, s.sandbox.VirtualRoot)
	}
	return desc
}

// sandboxRewriteErr rewrites real paths to virtual paths in error messages.
func sandboxRewriteErr(sandbox *cobot.SandboxConfig, err error) error {
	if sandbox == nil || sandbox.VirtualRoot == "" {
		return err
	}
	return fmt.Errorf("%s", sandbox.RewriteOutputPaths(err.Error()))
}

//go:embed embed_filesystem_read_params.json
var filesystemReadParamsJSON []byte

//go:embed embed_filesystem_write_params.json
var filesystemWriteParamsJSON []byte

type readFileArgs struct {
	Path string `json:"path"`
}

type ReadFileTool struct {
	sandboxTool
}

func NewReadFileTool(sandbox *cobot.SandboxConfig) *ReadFileTool {
	return &ReadFileTool{sandboxTool{sandbox: sandbox}}
}

func (t *ReadFileTool) Name() string {
	return "filesystem_read"
}

func (t *ReadFileTool) Description() string {
	return t.describeWithSandbox("Read the contents of a file at the given path.")
}

func (t *ReadFileTool) Parameters() json.RawMessage {
	return json.RawMessage(filesystemReadParamsJSON)
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

type WriteFileTool struct {
	sandboxTool
}

func NewWriteFileTool(sandbox *cobot.SandboxConfig) *WriteFileTool {
	return &WriteFileTool{sandboxTool{sandbox: sandbox}}
}

func (t *WriteFileTool) Name() string {
	return "filesystem_write"
}

func (t *WriteFileTool) Description() string {
	return t.describeWithSandbox("Write content to a file at the given path.")
}

func (t *WriteFileTool) Parameters() json.RawMessage {
	return json.RawMessage(filesystemWriteParamsJSON)
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
	// Ensure parent directory exists
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

//go:embed embed_filesystem_list_params.json
var filesystemListParamsJSON []byte

//go:embed embed_filesystem_search_params.json
var filesystemSearchParamsJSON []byte

type listDirArgs struct {
	Path    string `json:"path"`
	Pattern string `json:"pattern,omitempty"`
}

type ListDirTool struct {
	sandboxTool
}

func NewListDirTool(sandbox *cobot.SandboxConfig) *ListDirTool {
	return &ListDirTool{sandboxTool{sandbox: sandbox}}
}

func (t *ListDirTool) Name() string { return "filesystem_list" }

func (t *ListDirTool) Description() string {
	return t.describeWithSandbox("List files and directories at the given path.")
}

func (t *ListDirTool) Parameters() json.RawMessage {
	return json.RawMessage(filesystemListParamsJSON)
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
	Path       string `json:"path"`
	Pattern    string `json:"pattern"`
	MaxResults int    `json:"max_results,omitempty"`
}

type SearchFilesTool struct {
	sandboxTool
}

func NewSearchFilesTool(sandbox *cobot.SandboxConfig) *SearchFilesTool {
	return &SearchFilesTool{sandboxTool{sandbox: sandbox}}
}

func (t *SearchFilesTool) Name() string { return "filesystem_search" }

func (t *SearchFilesTool) Description() string {
	return t.describeWithSandbox("Search for files matching a pattern recursively from a root directory.")
}

func (t *SearchFilesTool) Parameters() json.RawMessage {
	return json.RawMessage(filesystemSearchParamsJSON)
}

func (t *SearchFilesTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a searchFilesArgs
	if err := decodeArgs(args, &a); err != nil {
		return "", err
	}
	if resolved, err := sandboxResolvePath(t.sandbox, a.Path); err != nil {
		return "", err
	} else {
		a.Path = resolved
	}

	maxResults := a.MaxResults
	if maxResults <= 0 {
		maxResults = 50
	}

	var matches []string
	err := filepath.WalkDir(a.Path, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if len(matches) >= maxResults {
			return fs.SkipAll
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "node_modules" || name == ".svn" || name == ".hg" || name == "__pycache__" || name == ".idea" || name == ".vscode" {
				return fs.SkipDir
			}
			if strings.HasPrefix(name, ".") && name != "." {
				return fs.SkipDir
			}
			return nil
		}
		matched, _ := filepath.Match(a.Pattern, d.Name())
		if matched {
			displayPath := path
			if t.sandbox != nil && t.sandbox.VirtualRoot != "" {
				displayPath = t.sandbox.RealToVirtual(path)
			}
			matches = append(matches, displayPath)
		}
		return nil
	})
	if err != nil && err != fs.SkipAll {
		return "", sandboxRewriteErr(t.sandbox, err)
	}

	if len(matches) == 0 {
		return "no files found matching pattern", nil
	}
	truncated := len(matches) >= maxResults
	if truncated {
		matches = matches[:maxResults]
	}
	result := strings.Join(matches, "\n")
	if truncated {
		result += fmt.Sprintf("\n... (truncated at %d results)", maxResults)
	}
	return result, nil
}

//go:embed embed_filesystem_grep_params.json
var filesystemGrepParamsJSON []byte

type grepFilesArgs struct {
	Path       string `json:"path"`
	Pattern    string `json:"pattern"`
	Include    string `json:"include,omitempty"`
	MaxResults int    `json:"max_results,omitempty"`
}

type GrepFilesTool struct {
	sandboxTool
}

func NewGrepFilesTool(sandbox *cobot.SandboxConfig) *GrepFilesTool {
	return &GrepFilesTool{sandboxTool{sandbox: sandbox}}
}

func (t *GrepFilesTool) Name() string { return "filesystem_grep" }

func (t *GrepFilesTool) Description() string {
	return t.describeWithSandbox("Search file contents for lines matching a regular expression pattern. Returns matching lines with line numbers.")
}

func (t *GrepFilesTool) Parameters() json.RawMessage {
	return json.RawMessage(filesystemGrepParamsJSON)
}

func (t *GrepFilesTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a grepFilesArgs
	if err := decodeArgs(args, &a); err != nil {
		return "", err
	}
	if resolved, err := sandboxResolvePath(t.sandbox, a.Path); err != nil {
		return "", err
	} else {
		a.Path = resolved
	}

	re, err := regexp.Compile(a.Pattern)
	if err != nil {
		return "", fmt.Errorf("invalid regex pattern: %w", err)
	}

	maxResults := a.MaxResults
	if maxResults <= 0 {
		maxResults = 50
	}

	var results []string
	fileCount := 0

	err = filepath.WalkDir(a.Path, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if fileCount >= maxResults {
			return fs.SkipAll
		}
		// Skip hidden and common ignored directories
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "node_modules" || name == ".svn" || name == ".hg" || name == "__pycache__" || name == ".idea" || name == ".vscode" {
				return fs.SkipDir
			}
			if strings.HasPrefix(name, ".") && name != "." {
				return fs.SkipDir
			}
			return nil
		}
		// Filter by include pattern
		if a.Include != "" {
			matched, _ := filepath.Match(a.Include, d.Name())
			if !matched {
				return nil
			}
		}
		// Skip binary-ish files by extension
		if isBinaryExt(filepath.Ext(d.Name())) {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		// Quick binary check — skip files with null bytes in first 8KB
		checkLen := len(data)
		if checkLen > 8192 {
			checkLen = 8192
		}
		if bytes.IndexByte(data[:checkLen], 0) >= 0 {
			return nil
		}

		lines := bytes.Split(data, []byte("\n"))
		var fileMatches []string
		for i, line := range lines {
			if re.Match(line) {
				displayPath := path
				if t.sandbox != nil && t.sandbox.VirtualRoot != "" {
					displayPath = t.sandbox.RealToVirtual(path)
				}
				fileMatches = append(fileMatches, fmt.Sprintf("%s:%d:%s", displayPath, i+1, string(line)))
				if len(fileMatches) >= 10 { // max 10 matches per file
					break
				}
			}
		}
		if len(fileMatches) > 0 {
			results = append(results, fileMatches...)
			fileCount++
		}
		return nil
	})
	if err != nil && err != fs.SkipAll {
		return "", sandboxRewriteErr(t.sandbox, err)
	}

	if len(results) == 0 {
		return "no matches found", nil
	}
	if fileCount >= maxResults {
		results = append(results, fmt.Sprintf("... (truncated at %d files, %d matches)", maxResults, len(results)))
	}
	return strings.Join(results, "\n"), nil
}

var binaryExtensions = map[string]bool{
	".exe": true, ".dll": true, ".so": true, ".dylib": true,
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".bmp": true, ".ico": true, ".webp": true, ".tiff": true,
	".zip": true, ".tar": true, ".gz": true, ".bz2": true, ".xz": true, ".zst": true, ".7z": true, ".rar": true,
	".mp3": true, ".mp4": true, ".avi": true, ".mov": true, ".mkv": true, ".flac": true, ".wav": true, ".ogg": true,
	".pdf": true, ".doc": true, ".docx": true, ".xls": true, ".xlsx": true, ".ppt": true, ".pptx": true,
	".o": true, ".a": true, ".lib": true, ".obj": true, ".pyc": true, ".pyo": true, ".class": true, ".jar": true, ".war": true,
	".woff": true, ".woff2": true, ".ttf": true, ".eot": true, ".otf": true,
	".sqlite": true, ".db": true, ".iso": true, ".dmg": true, ".pkg": true,
	".wasm": true,
}

func isBinaryExt(ext string) bool {
	return binaryExtensions[strings.ToLower(ext)]
}

var (
	_ cobot.Tool = (*ReadFileTool)(nil)
	_ cobot.Tool = (*WriteFileTool)(nil)
	_ cobot.Tool = (*ListDirTool)(nil)
	_ cobot.Tool = (*SearchFilesTool)(nil)
	_ cobot.Tool = (*GrepFilesTool)(nil)
)
