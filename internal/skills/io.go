package skills

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// MaxSkillFileSize is the maximum allowed size for a single skill file (1 MB).
// This protects the catalog loader from unbounded memory reads.
const MaxSkillFileSize int64 = 1 << 20 // 1 MB

// maxSkillFileSize aliases the exported constant for internal use.
const maxSkillFileSize = MaxSkillFileSize

// ReadFileWithLimit reads a file after verifying its size does not exceed maxSize.
// Uses io.LimitReader to protect against TOCTOU races.
func ReadFileWithLimit(path string, maxSize int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%s is a directory", path)
	}
	if info.Size() > maxSize {
		return nil, fmt.Errorf("file %s too large: %d bytes (max %d)", path, info.Size(), maxSize)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxSize {
		return nil, fmt.Errorf("file %s too large (exceeds %d bytes)", path, maxSize)
	}
	return data, nil
}

// ListLinkedFiles returns a map of subdir→filenames for non-empty linked file
// directories (references/, templates/, scripts/, assets/) under skillDir.
func ListLinkedFiles(skillDir string) map[string][]string {
	result := make(map[string][]string)
	for _, subdir := range linkedSubdirs {
		ents, err := os.ReadDir(filepath.Join(skillDir, subdir))
		if err != nil {
			continue
		}
		var files []string
		for _, ent := range ents {
			if !ent.IsDir() {
				files = append(files, ent.Name())
			}
		}
		if len(files) > 0 {
			sort.Strings(files)
			result[subdir] = files
		}
	}
	return result
}

// ReadLinkedFile reads a linked file under an allowed subdir with path safety and 1 MB limit.
func ReadLinkedFile(skillDir, filePath string) (string, error) {
	if err := ValidateLinkedFilePath(filePath); err != nil {
		return "", err
	}
	abs, err := VerifyContainment(filepath.Join(skillDir, filePath), skillDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("linked file not found: %q", filePath)
		}
		return "", err
	}
	data, err := ReadFileWithLimit(abs, MaxSkillFileSize)
	if err != nil {
		return "", fmt.Errorf("read linked file: %w", err)
	}
	return string(data), nil
}

// EnsureContainedDir verifies that parentDir is safely contained under skillDir
// (blocking path traversal) and creates parentDir with mode 0755 if it doesn't exist.
// Handles symlinks in intermediate path components by resolving the longest
// existing prefix before performing the containment check.
func EnsureContainedDir(parentDir, skillDir string) error {
	checkDir := parentDir
	if r, err := filepath.EvalSymlinks(parentDir); err == nil {
		checkDir = r
	}
	if _, err := VerifyContainment(checkDir, skillDir); err != nil {
		if !os.IsNotExist(err) {
			if errors.Is(err, ErrPathTraversal) {
				return ErrPathTraversal
			}
			return fmt.Errorf("verify containment: %w", err)
		}
		// Target doesn't exist. Resolve longest existing prefix to handle
		// symlinks in intermediate components, then verify full resolved path.
		resolved, err := resolveExistingPrefix(parentDir)
		if err != nil {
			return ErrPathTraversal
		}
		absBase, err := filepath.Abs(skillDir)
		if err != nil {
			return ErrPathTraversal
		}
		absBaseResolved, err := filepath.EvalSymlinks(absBase)
		if err != nil {
			absBaseResolved = absBase
		}
		if !strings.HasPrefix(resolved, absBaseResolved+string(filepath.Separator)) {
			return ErrPathTraversal
		}
	}
	return os.MkdirAll(parentDir, 0755)
}

// resolveExistingPrefix walks up the path to find the longest existing ancestor,
// resolves its symlinks, and appends the non-existing suffix to produce a fully
// resolved path.
func resolveExistingPrefix(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	p := abs
	for {
		r, err := filepath.EvalSymlinks(p)
		if err == nil {
			remaining := strings.TrimPrefix(abs, p)
			return filepath.Clean(r + remaining), nil
		}
		parent := filepath.Dir(p)
		if parent == p {
			return "", fmt.Errorf("no existing prefix for %q", path)
		}
		p = parent
	}
}

// loadFrontmatterOnly loads only the YAML frontmatter from a SkillFile, leaving
// Content empty. Used for tier-1 catalog scanning to avoid reading full bodies.
func loadFrontmatterOnly(skillDir, category, source string) (Skill, error) {
	f, err := os.Open(filepath.Join(skillDir, SkillFile))
	if err != nil {
		return Skill{}, fmt.Errorf("read %s: %w", SkillFile, err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return Skill{}, fmt.Errorf("stat %s: %w", SkillFile, err)
	}
	if info.IsDir() {
		return Skill{}, fmt.Errorf("%s is a directory", filepath.Join(skillDir, SkillFile))
	}
	if info.Size() > maxSkillFileSize {
		return Skill{}, fmt.Errorf("file %s too large: %d bytes (max %d)", filepath.Join(skillDir, SkillFile), info.Size(), maxSkillFileSize)
	}

	// Read line by line until closing delimiter.
	const maxLineSize = 64 * 1024
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4096), maxLineSize)

	var lines []string
	foundOpen := false
	for scanner.Scan() {
		line := scanner.Text()
		if !foundOpen {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}
			if trimmed == "---" {
				foundOpen = true
				continue
			}
			return Skill{}, errors.New("skill file must start with YAML frontmatter (---)")
		}
		if strings.HasPrefix(line, "---") && strings.TrimSpace(line) == "---" {
			break
		}
		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		return Skill{}, fmt.Errorf("read %s: %w", SkillFile, err)
	}

	// Normalize CRLF to LF for consistency with parseFrontMatter.
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, "\r")
	}

	fmContent := strings.Join(lines, "\n")
	var fm frontMatter
	if err := yaml.Unmarshal([]byte(fmContent), &fm); err != nil {
		return Skill{}, fmt.Errorf("parse YAML frontmatter: %w", err)
	}
	if fm.Name == "" || fm.Description == "" {
		return Skill{}, errors.New("skill name and description are required in frontmatter")
	}
	if err := ValidateSkillName(fm.Name); err != nil {
		return Skill{}, fmt.Errorf("invalid frontmatter name: %w", err)
	}
	if dirName := filepath.Base(skillDir); fm.Name != dirName {
		return Skill{}, fmt.Errorf("skill name %q does not match directory name %q", fm.Name, dirName)
	}
	absDir, err := filepath.Abs(skillDir)
	if err != nil {
		return Skill{}, fmt.Errorf("resolve skill dir: %w", err)
	}
	return Skill{Name: fm.Name, Description: fm.Description, Category: category, Content: "", Source: source, Dir: absDir, Metadata: fm.Metadata}, nil
}

// loadNewFormatSkill loads a SkillFile from a skill directory.
func loadNewFormatSkill(skillDir, category, source string) (Skill, error) {
	data, err := ReadFileWithLimit(filepath.Join(skillDir, SkillFile), maxSkillFileSize)
	if err != nil {
		return Skill{}, fmt.Errorf("read %s: %w", SkillFile, err)
	}
	fm, body, err := parseFrontMatter(string(data))
	if err != nil {
		return Skill{}, fmt.Errorf("parse frontmatter: %w", err)
	}
	if fm.Name == "" || fm.Description == "" {
		return Skill{}, errors.New("skill name and description are required in frontmatter")
	}
	if err := ValidateSkillName(fm.Name); err != nil {
		return Skill{}, fmt.Errorf("invalid frontmatter name: %w", err)
	}
	if dirName := filepath.Base(skillDir); fm.Name != dirName {
		return Skill{}, fmt.Errorf("skill name %q does not match directory name %q", fm.Name, dirName)
	}
	absDir, err := filepath.Abs(skillDir)
	if err != nil {
		return Skill{}, fmt.Errorf("resolve skill dir: %w", err)
	}
	return Skill{Name: fm.Name, Description: fm.Description, Category: category, Content: body, Source: source, Dir: absDir, Metadata: fm.Metadata}, nil
}

// FindSkillDir finds a skill directory by name, searching skillsDir.
func FindSkillDir(skillsDir, name string) (string, error) {
	if err := ValidateSkillName(name); err != nil {
		return "", err
	}
	path, err := findSkillDirIn(name, skillsDir)
	if err != nil {
		return "", fmt.Errorf("find skill %q: %w", name, err)
	}
	return path, nil
}

// findSkillDirIn searches a single root for a skill by name.
func findSkillDirIn(name, root string) (string, error) {
	dir := filepath.Join(root, name)
	if _, err := os.Stat(filepath.Join(dir, SkillFile)); err == nil {
		return dir, nil
	}
	ents, err := os.ReadDir(root)
	if err != nil {
		return "", fmt.Errorf("read skills dir %s: %w", root, err)
	}
	for _, ent := range ents {
		if ent.IsDir() && isValidCategoryName(ent.Name()) {
			if _, err := os.Stat(filepath.Join(root, ent.Name(), name, SkillFile)); err == nil {
				return filepath.Join(root, ent.Name(), name), nil
			}
		}
	}
	return "", fmt.Errorf("skill not found: %q in %s", name, root)
}
