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

	"github.com/cobot-agent/cobot/internal/sandbox"
	cobot "github.com/cobot-agent/cobot/pkg"
)

//go:embed schemas/embed_filesystem_search_params.json
var filesystemSearchParamsJSON []byte

type searchFilesArgs struct {
	Path       string `json:"path"`
	Pattern    string `json:"pattern"`
	MaxResults int    `json:"max_results,omitempty"`
}

type SearchFilesTool struct {
	BasicTool
}

func NewSearchFilesTool(sandbox *sandbox.SandboxConfig) *SearchFilesTool {
	return &SearchFilesTool{BasicTool{
		sandboxTool: sandboxTool{sandbox: sandbox},
		name:        "filesystem_search",
		desc:        "Search for files matching a pattern recursively from a root directory.",
		params:      filesystemSearchParamsJSON,
	}}
}

func (t *SearchFilesTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a searchFilesArgs
	if err := decodeArgs(args, &a); err != nil {
		return "", err
	}
	if resolved, err := sandboxResolvePath(t.sandbox, a.Path, false); err != nil {
		return "", err
	} else {
		a.Path = resolved
	}

	maxResults := a.MaxResults
	if maxResults <= 0 {
		maxResults = 50
	}

	// Try fd-find first
	if result, err := t.searchWithFd(ctx, &a, maxResults); err == nil {
		return result, nil
	}

	// Fallback to Go implementation
	return t.searchWithGo(&a, maxResults)
}

// searchWithFd attempts to use fd-find for filename search.
func (t *SearchFilesTool) searchWithFd(ctx context.Context, a *searchFilesArgs, maxResults int) (string, error) {
	fdArgs := []string{
		a.Pattern,
		a.Path,
		"--type", "f",
		"--max-results", fmt.Sprintf("%d", maxResults),
		"--absolute-path",
	}
	output, err := runFd(ctx, fdArgs)
	if err != nil {
		return "", err
	}
	if output == "" {
		return "no files found matching pattern", nil
	}

	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	var results []string
	for _, line := range lines {
		if t.sandbox != nil && t.sandbox.VirtualRoot != "" {
			line = t.sandbox.RealToVirtual(line)
		}
		results = append(results, line)
	}

	truncated := false
	if len(results) > maxResults {
		results = results[:maxResults]
		truncated = true
	}
	result := strings.Join(results, "\n")
	if truncated {
		result += fmt.Sprintf("\n... (truncated at %d results)", maxResults)
	}
	return result, nil
}

// searchWithGo is the Go builtin fallback for filename search.
func (t *SearchFilesTool) searchWithGo(a *searchFilesArgs, maxResults int) (string, error) {
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

//go:embed schemas/embed_filesystem_grep_params.json
var filesystemGrepParamsJSON []byte

type grepFilesArgs struct {
	Path       string `json:"path"`
	Pattern    string `json:"pattern"`
	Include    string `json:"include,omitempty"`
	MaxResults int    `json:"max_results,omitempty"`
}

type GrepFilesTool struct {
	BasicTool
}

func NewGrepFilesTool(sandbox *sandbox.SandboxConfig) *GrepFilesTool {
	return &GrepFilesTool{BasicTool{
		sandboxTool: sandboxTool{sandbox: sandbox},
		name:        "filesystem_grep",
		desc:        "Search file contents for lines matching a regular expression pattern. Returns matching lines with line numbers.",
		params:      filesystemGrepParamsJSON,
	}}
}

func (t *GrepFilesTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a grepFilesArgs
	if err := decodeArgs(args, &a); err != nil {
		return "", err
	}
	if resolved, err := sandboxResolvePath(t.sandbox, a.Path, false); err != nil {
		return "", err
	} else {
		a.Path = resolved
	}

	maxResults := a.MaxResults
	if maxResults <= 0 {
		maxResults = 50
	}

	// Try ripgrep first
	if result, err := t.grepWithRg(ctx, &a, maxResults); err == nil {
		return result, nil
	}

	// Fallback to Go implementation
	return t.grepWithGo(&a, maxResults)
}

// grepWithRg attempts to use ripgrep for content search.
func (t *GrepFilesTool) grepWithRg(ctx context.Context, a *grepFilesArgs, maxResults int) (string, error) {
	rgArgs := []string{
		"--no-heading",
		"--line-number",
		"--color", "never",
		"--max-count", "10",
		"--max-filesize", "1M",
	}
	if a.Include != "" {
		rgArgs = append(rgArgs, "--glob", a.Include)
	}
	rgArgs = append(rgArgs, a.Pattern, a.Path)

	output, err := runRg(ctx, rgArgs)
	if err != nil {
		return "", err
	}
	if output == "" {
		return "no matches found", nil
	}

	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	// Rewrite real paths to virtual paths and truncate
	var results []string
	for _, line := range lines {
		if t.sandbox != nil && t.sandbox.VirtualRoot != "" {
			// rg output is "path:linenum:content", rewrite the path part
			idx := strings.Index(line, ":")
			if idx >= 0 {
				realPath := line[:idx]
				virtualPath := t.sandbox.RealToVirtual(realPath)
				line = virtualPath + line[idx:]
			}
		}
		results = append(results, line)
		if len(results) >= maxResults*10 { // max 10 matches per file * max files
			break
		}
	}

	truncated := false
	if len(results) > maxResults*10 {
		results = results[:maxResults*10]
		truncated = true
	}
	result := strings.Join(results, "\n")
	if truncated {
		result += fmt.Sprintf("\n... (truncated at %d lines)", len(results))
	}
	return result, nil
}

// grepWithGo is the Go builtin fallback for content search.
func (t *GrepFilesTool) grepWithGo(a *grepFilesArgs, maxResults int) (string, error) {
	re, err := regexp.Compile(a.Pattern)
	if err != nil {
		return "", fmt.Errorf("invalid regex pattern: %w", err)
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
	_ cobot.Tool = (*SearchFilesTool)(nil)
	_ cobot.Tool = (*GrepFilesTool)(nil)
)
