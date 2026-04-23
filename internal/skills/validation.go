package skills

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ErrPathTraversal is returned when a file path contains traversal patterns.
var ErrPathTraversal = errors.New("invalid file path: path traversal detected")

// validNameRe matches skill and category names: lowercase alphanumeric + hyphens.
// Requires at least 2 characters. Upper length bound (64) is enforced separately in ValidateSkillName.
var validNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]*[a-z0-9]$`)

// linkedSubdirs are the allowed subdirectories for linked files.
var linkedSubdirs = []string{"references", "templates", "scripts", "assets"}

const (
	nameMinLen     = 2
	nameMaxLen     = 64
	viewNameMaxLen = 128
)

// ValidateSkillName validates a skill or category name against the spec.
// Name must match ^[a-z][a-z0-9-]*[a-z0-9]$ (nameMinLen-nameMaxLen chars).
func ValidateSkillName(name string) error {
	if len(name) < nameMinLen || len(name) > nameMaxLen {
		return fmt.Errorf("invalid name %q: must be %d-%d characters", name, nameMinLen, nameMaxLen)
	}
	if !validNameRe.MatchString(name) {
		return fmt.Errorf("invalid name %q: must match ^[a-z][a-z0-9-]*[a-z0-9]$", name)
	}
	return nil
}

// isValidCategoryName checks if a directory name is a valid category.
// Blocks path traversal components and dotfiles/dot-directories.
func isValidCategoryName(name string) bool {
	return name != "" && !strings.Contains(name, "/") && !strings.Contains(name, "\\") && !strings.Contains(name, "..") && !strings.HasPrefix(name, ".")
}

// IsPathTraversalSafe returns false if filePath contains traversal patterns.
// Exported for reuse by other packages (e.g., tools).
func IsPathTraversalSafe(filePath string) bool {
	return !strings.Contains(filePath, "..") && !strings.HasPrefix(filePath, "/") && !strings.HasPrefix(filePath, "\\")
}

// ValidateContent parses frontmatter from content and validates that the
// skill name matches expectedName and description is present.
func ValidateContent(content, expectedName string) error {
	fm, _, err := parseFrontMatter(content)
	if err != nil {
		return fmt.Errorf("invalid %s content: %w", SkillFile, err)
	}
	if fm.Name != expectedName {
		return fmt.Errorf("frontmatter name %q does not match skill name %q", fm.Name, expectedName)
	}
	if fm.Description == "" {
		return errors.New("frontmatter description is required")
	}
	return nil
}

// VerifyContainment resolves symlinks and checks that resolved path is under baseDir.
// Returns the resolved absolute path on success.
// Exported for reuse by other packages (e.g., tools).
func VerifyContainment(fullPath string, baseDir string) (string, error) {
	resolved, err := filepath.EvalSymlinks(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", os.ErrNotExist
		}
		return "", fmt.Errorf("resolve full path: %w", err)
	}
	absResolved, err := filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("abs full path: %w", err)
	}
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return "", fmt.Errorf("resolve base path: %w", err)
	}
	absBaseResolved, err := filepath.EvalSymlinks(absBase)
	if err != nil {
		absBaseResolved = absBase
	}
	// Handle root directory special case: every valid path is "under" root.
	sep := string(filepath.Separator)
	if absBaseResolved == sep {
		if absResolved == sep {
			return "", ErrPathTraversal
		}
		return absResolved, nil
	}
	if !strings.HasPrefix(absResolved, absBaseResolved+sep) {
		return "", ErrPathTraversal
	}
	return absResolved, nil
}

// ValidateLinkedFilePath ensures a file path is under an allowed linked subdir.
// Returns an error if the path is not under references/, templates/, scripts/, or assets/.
// Also rejects paths with traversal patterns for defense-in-depth.
// Uses "/" directly (not os.PathSeparator) because filePath comes from JSON and always uses "/".
func ValidateLinkedFilePath(filePath string) error {
	if !IsPathTraversalSafe(filePath) {
		return ErrPathTraversal
	}
	for _, subdir := range linkedSubdirs {
		prefix := subdir + "/"
		if strings.HasPrefix(filePath, prefix) && len(filePath) > len(prefix) {
			return nil
		}
	}
	return fmt.Errorf("file path must be under one of: %s", strings.Join(linkedSubdirs, ", "))
}
