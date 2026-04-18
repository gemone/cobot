package cobot

import (
	"fmt"
	"path/filepath"
	"strings"
)

type SandboxConfig struct {
	VirtualRoot     string   `yaml:"virtual_root,omitempty"`
	Root            string   `yaml:"root"`
	AllowPaths      []string `yaml:"allow_paths,omitempty"`
	ReadonlyPaths   []string `yaml:"readonly_paths,omitempty"`
	AllowNetwork    bool     `yaml:"allow_network"`
	BlockedCommands []string `yaml:"blocked_commands,omitempty"`
}

func (s *SandboxConfig) IsAllowed(path string, write bool) bool {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	absPath = EvalSymlinks(absPath)

	readonlyMatched := false
	for _, rp := range s.ReadonlyPaths {
		absRP, err := filepath.Abs(rp)
		if err != nil {
			continue
		}
		absRP = EvalSymlinks(absRP)
		if IsSubpath(absPath, absRP) {
			readonlyMatched = true
			if write {
				return false
			}
		}
	}
	if readonlyMatched && !write {
		return true
	}

	for _, ap := range s.AllowPaths {
		absAP, err := filepath.Abs(ap)
		if err != nil {
			continue
		}
		absAP = EvalSymlinks(absAP)
		if IsSubpath(absPath, absAP) {
			return true
		}
	}

	if s.Root != "" {
		absRoot, err := filepath.Abs(s.Root)
		if err != nil {
			return false
		}
		absRoot = EvalSymlinks(absRoot)
		if IsSubpath(absPath, absRoot) {
			if readonlyMatched && write {
				return false
			}
			return true
		}
	}

	return false
}

// AutoResolvePath resolves any path form (virtual, real, relative, absolute) into the sandbox.
// Path traversal (../) is blocked by ResolvePath's VirtualRoot prefix validation.
func (s *SandboxConfig) AutoResolvePath(path string) (string, error) {
	if s == nil || s.VirtualRoot == "" {
		return path, nil
	}

	cleaned := filepath.Clean(path)
	vr := filepath.Clean(s.VirtualRoot)

	if cleaned == vr || strings.HasPrefix(cleaned, vr+"/") {
		return s.ResolvePath(path)
	}

	if s.Root != "" {
		absRoot := filepath.Clean(s.Root)
		if cleaned == absRoot || strings.HasPrefix(cleaned, absRoot+"/") {
			rel := strings.TrimPrefix(cleaned, absRoot)
			if rel == "" || rel == "/" {
				return s.ResolvePath(vr)
			}
			return s.ResolvePath(vr + rel)
		}
	}

	if !strings.HasPrefix(cleaned, "/") {
		virtualPath := vr + "/" + cleaned
		return s.ResolvePath(virtualPath)
	}

	virtualPath := vr + cleaned
	return s.ResolvePath(virtualPath)
}

// ResolvePath validates that path starts with VirtualRoot and translates it to the real filesystem path.
func (s *SandboxConfig) ResolvePath(path string) (string, error) {
	if s == nil || s.VirtualRoot == "" {
		return path, nil
	}

	cleaned := filepath.Clean(path)
	vr := filepath.Clean(s.VirtualRoot)

	if cleaned != vr && !strings.HasPrefix(cleaned, vr+"/") {
		return "", fmt.Errorf("path %q must start with %q (sandbox enforced)", path, s.VirtualRoot)
	}

	rel := strings.TrimPrefix(cleaned, vr)
	if rel == "" || rel == "/" {
		return s.Root, nil
	}
	return filepath.Join(s.Root, rel[1:]), nil
}

func (s *SandboxConfig) RewritePaths(text string) string {
	if s == nil || s.VirtualRoot == "" {
		return text
	}
	return strings.ReplaceAll(text, s.VirtualRoot, s.Root)
}

func (s *SandboxConfig) RewriteOutputPaths(text string) string {
	if s == nil || s.VirtualRoot == "" || s.Root == "" {
		return text
	}
	resolvedRoot := EvalSymlinks(s.Root)
	result := strings.ReplaceAll(text, resolvedRoot, s.VirtualRoot)
	if resolvedRoot != s.Root {
		result = strings.ReplaceAll(result, s.Root, s.VirtualRoot)
	}
	return result
}

func (s *SandboxConfig) RewriteError(err error) error {
	if s == nil || s.VirtualRoot == "" || err == nil {
		return err
	}
	return fmt.Errorf("%s: %w", s.RewriteOutputPaths(err.Error()), err)
}

func (s *SandboxConfig) RealToVirtual(realPath string) string {
	if s == nil || s.VirtualRoot == "" || s.Root == "" {
		return realPath
	}
	absPath, err := filepath.Abs(realPath)
	if err != nil {
		return s.VirtualRoot + "/[external]/" + filepath.Base(realPath)
	}
	absRoot := filepath.Clean(s.Root)
	if absPath == absRoot {
		return s.VirtualRoot
	}
	if strings.HasPrefix(absPath, absRoot+string(filepath.Separator)) {
		rel := strings.TrimPrefix(absPath, absRoot+string(filepath.Separator))
		return filepath.Join(s.VirtualRoot, rel)
	}
	return s.VirtualRoot + "/[external]/" + filepath.Base(absPath)
}

func (s *SandboxConfig) ValidatePath(resolvedPath string) error {
	if s == nil || s.Root == "" {
		return nil
	}
	absPath, err := filepath.Abs(resolvedPath)
	if err != nil {
		return fmt.Errorf("cannot resolve path %q: %w", resolvedPath, err)
	}
	absPath = filepath.Clean(absPath)
	absRoot := filepath.Clean(s.Root)
	if absPath == absRoot || strings.HasPrefix(absPath, absRoot+string(filepath.Separator)) {
		return nil
	}
	return fmt.Errorf("path %q is outside sandbox root", resolvedPath)
}

func (s *SandboxConfig) IsBlockedCommand(cmd string) bool {
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return false
	}
	baseCmd := filepath.Base(fields[0])
	for _, blocked := range s.BlockedCommands {
		if baseCmd == blocked || cmd == blocked || strings.HasPrefix(cmd, blocked+" ") || strings.HasPrefix(cmd, blocked) {
			return true
		}
		if strings.Contains(cmd, "|"+blocked) || strings.Contains(cmd, ";"+blocked) ||
			strings.Contains(cmd, "$("+blocked) || strings.Contains(cmd, "`"+blocked) {
			return true
		}
	}
	return false
}
