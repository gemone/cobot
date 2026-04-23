package tools

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cobot-agent/cobot/internal/skills"
	"github.com/cobot-agent/cobot/internal/workspace"
	cobot "github.com/cobot-agent/cobot/pkg"
)

const (
	maxLinkedFileSize = 1024 * 1024 // 1 MB
)

//go:embed schemas/embed_skills_list_params.json
var skillsListParamsJSON []byte

//go:embed schemas/embed_skill_view_params.json
var skillViewParamsJSON []byte

//go:embed schemas/embed_skill_manage_params.json
var skillManageParamsJSON []byte

// skillSummary is the JSON DTO for listing skills.
type skillSummary struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category,omitempty"`
	Source      string `json:"source,omitempty"`
}

// skillsHandler holds shared state for all skills tool operations.
type skillsHandler struct {
	ws        *workspace.Workspace
	refresher cobot.SkillsPromptRefresher
}

func (h *skillsHandler) skillDirs() []string {
	return []string{workspace.GlobalSkillsDir(), h.ws.SkillsDir()}
}

func (h *skillsHandler) findWritableDir(name string) (string, error) {
	return skills.FindSkillDir(h.ws.SkillsDir(), name)
}

// RegisterSkillsTools registers all skills-related tools.
// The refresher is called after mutating skill operations (create/edit/patch/delete)
// to refresh the system prompt's skills section.
func RegisterSkillsTools(registry cobot.ToolRegistry, ws *workspace.Workspace, refresher cobot.SkillsPromptRefresher) {
	h := &skillsHandler{ws: ws, refresher: refresher}
	registry.Register(&fnTool{
		name: "skills_list", desc: "List all available skills grouped by category. Use to discover skills that match the current task.",
		params: json.RawMessage(skillsListParamsJSON), execute: h.executeList,
	})
	registry.Register(&fnTool{
		name: "skill_view", desc: "Load a skill's full content and linked files when it matches the current task. Skills contain specialized knowledge — API endpoints, tool-specific commands, and proven workflows. Always load before attempting tasks that match a skill's description. First call returns SKILL.md content + linked file index; set file_path to read a specific linked file.",
		params: json.RawMessage(skillViewParamsJSON), execute: h.executeView,
	})
	registry.Register(&fnTool{
		name: "skill_manage", desc: "Create, edit, patch, or delete skills, and manage linked resource files. Use to save new workflows, fix outdated instructions, or update skills after discovering pitfalls.",
		params: json.RawMessage(skillManageParamsJSON), execute: h.executeManage,
	})
}

func (h *skillsHandler) executeList(ctx context.Context, args json.RawMessage) (string, error) {
	var params struct {
		Category string `json:"category"`
	}
	if err := decodeArgs(args, &params); err != nil {
		return "", err
	}
	catalog, err := skills.LoadCatalog(ctx, h.skillDirs(), nil)
	if err != nil {
		return "", fmt.Errorf("load skills catalog: %w", err)
	}
	summaries := make([]skillSummary, 0, len(catalog))
	for _, sk := range catalog {
		if params.Category != "" && sk.Category != params.Category {
			continue
		}
		summaries = append(summaries, skillSummary{
			Name: sk.Name, Description: sk.Description,
			Category: sk.Category, Source: sk.Source,
		})
	}
	data, err := json.MarshalIndent(summaries, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal skills: %w", err)
	}
	return string(data), nil
}

func (h *skillsHandler) executeView(ctx context.Context, args json.RawMessage) (string, error) {
	var params struct {
		Name     string `json:"name"`
		FilePath string `json:"file_path"`
	}
	if err := decodeArgs(args, &params); err != nil {
		return "", err
	}
	if params.Name == "" {
		return "", errors.New("name is required")
	}
	if err := skills.ValidateSkillName(params.Name); err != nil {
		return "", err
	}
	if params.FilePath != "" {
		skillDir, err := skills.FindSkillDir(h.ws.SkillsDir(), params.Name)
		if err != nil {
			skillDir, err = skills.FindSkillDir(workspace.GlobalSkillsDir(), params.Name)
			if err != nil {
				return "", fmt.Errorf("skill %q not found: %w", params.Name, err)
			}
		}
		return skills.ReadLinkedFile(skillDir, params.FilePath)
	}
	sk, err := skills.LoadOne(ctx, h.skillDirs(), params.Name)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if sk.Dir != "" {
		skillMD := filepath.Join(sk.Dir, skills.SkillFile)
		data, err := skills.ReadFileWithLimit(skillMD, skills.MaxSkillFileSize)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", skills.SkillFile, err)
		}
		b.WriteString(string(data))
	}
	if sk.Dir != "" {
		appendLinkedFiles(&b, sk.Dir)
	}
	return b.String(), nil
}

func appendLinkedFiles(b *strings.Builder, skillDir string) {
	linked := skills.ListLinkedFiles(skillDir)
	if len(linked) == 0 {
		return
	}
	b.WriteString("\n\n## Linked Files\n")
	subdirs := make([]string, 0, len(linked))
	for subdir := range linked {
		subdirs = append(subdirs, subdir)
	}
	sort.Strings(subdirs)
	for _, subdir := range subdirs {
		fmt.Fprintf(b, "\n**%s/**\n", subdir)
		for _, f := range linked[subdir] {
			fmt.Fprintf(b, "- %s\n", f)
		}
	}
}

// --- manage actions ---

type manageParams struct {
	Action      string `json:"action"`
	Name        string `json:"name"`
	Category    string `json:"category"`
	Content     string `json:"content"`
	OldString   string `json:"old_string"`
	NewString   string `json:"new_string"`
	FilePath    string `json:"file_path"`
	FileContent string `json:"file_content"`
}

var manageActions = map[string]func(*skillsHandler, context.Context, manageParams) (string, error){
	skills.ActionCreate:     (*skillsHandler).doCreate,
	skills.ActionEdit:       (*skillsHandler).doEdit,
	skills.ActionPatch:      (*skillsHandler).doPatch,
	skills.ActionDelete:     (*skillsHandler).doDelete,
	skills.ActionWriteFile:  (*skillsHandler).doWriteFile,
	skills.ActionRemoveFile: (*skillsHandler).doRemoveFile,
}

func (h *skillsHandler) executeManage(ctx context.Context, args json.RawMessage) (string, error) {
	var p manageParams
	if err := decodeArgs(args, &p); err != nil {
		return "", err
	}
	if p.Name == "" {
		return "", errors.New("name is required")
	}
	if err := skills.ValidateSkillName(p.Name); err != nil {
		return "", err
	}
	if p.Action == "" {
		return "", errors.New("action is required (create, edit, patch, delete, write_file, remove_file)")
	}
	fn, ok := manageActions[p.Action]
	if !ok {
		return "", fmt.Errorf("unknown action: %q", p.Action)
	}
	return fn(h, ctx, p)
}

func (h *skillsHandler) doCreate(ctx context.Context, p manageParams) (string, error) {
	if err := validateAndCheckContent(p.Content, p.Name); err != nil {
		return "", err
	}
	skillDir := filepath.Join(h.ws.SkillsDir(), p.Name)
	if p.Category != "" {
		if err := skills.ValidateSkillName(p.Category); err != nil {
			return "", fmt.Errorf("invalid category: %w", err)
		}
		catDir := filepath.Join(h.ws.SkillsDir(), p.Category)
		// Reject if category directory is itself a skill (has SKILL.md),
		// because the scanner treats such dirs as skills, not categories,
		// making nested skills invisible to the catalog.
		if _, err := os.Stat(filepath.Join(catDir, skills.SkillFile)); err == nil {
			return "", fmt.Errorf("cannot use %q as category: a skill with that name already exists", p.Category)
		}
		skillDir = filepath.Join(catDir, p.Name)
	}
	// Reject if target path already exists.
	if _, err := os.Stat(skillDir); err == nil {
		if _, serr := os.Stat(filepath.Join(skillDir, skills.SkillFile)); serr == nil {
			return "", fmt.Errorf("skill %q already exists; use edit or patch to modify it", p.Name)
		}
		return "", fmt.Errorf("cannot create skill %q: directory already exists with other content", p.Name)
	}
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		return "", fmt.Errorf("create skill directory: %w", err)
	}
	skillMD := filepath.Join(skillDir, skills.SkillFile)
	f, err := os.OpenFile(skillMD, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		if os.IsExist(err) {
			return "", fmt.Errorf("skill %q already exists; use edit or patch to modify it", p.Name)
		}
		return "", fmt.Errorf("write %s: %w", skills.SkillFile, err)
	}
	if _, err := f.WriteString(p.Content); err != nil {
		f.Close()
		os.Remove(skillMD)
		os.Remove(skillDir) // clean up empty dir if we created it
		return "", fmt.Errorf("write %s: %w", skills.SkillFile, err)
	}
	if err := f.Close(); err != nil {
		os.Remove(skillMD)
		os.Remove(skillDir) // clean up empty dir if we created it
		return "", fmt.Errorf("close %s: %w", skills.SkillFile, err)
	}
	slog.Info("skill created", "name", p.Name, "category", p.Category)
	h.refreshSkillsPrompt(ctx)
	return fmt.Sprintf("skill created: %s", p.Name), nil
}

func (h *skillsHandler) doEdit(ctx context.Context, p manageParams) (string, error) {
	if err := validateAndCheckContent(p.Content, p.Name); err != nil {
		return "", err
	}
	skillDir, err := h.findWritableDir(p.Name)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(skillDir, skills.SkillFile), []byte(p.Content), 0644); err != nil {
		return "", fmt.Errorf("write %s: %w", skills.SkillFile, err)
	}
	h.refreshSkillsPrompt(ctx)
	return fmt.Sprintf("skill updated: %s", p.Name), nil
}

func (h *skillsHandler) doPatch(ctx context.Context, p manageParams) (string, error) {
	if p.OldString == "" {
		return "", errors.New("old_string is required for patch action")
	}
	skillDir, err := h.findWritableDir(p.Name)
	if err != nil {
		return "", err
	}
	skillMD := filepath.Join(skillDir, skills.SkillFile)
	data, err := skills.ReadFileWithLimit(skillMD, skills.MaxSkillFileSize)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", skills.SkillFile, err)
	}
	content := string(data)
	if !strings.Contains(content, p.OldString) {
		return "", fmt.Errorf("old_string not found in %s", skills.SkillFile)
	}
	newContent := strings.Replace(content, p.OldString, p.NewString, 1)
	if len(newContent) > int(skills.MaxSkillFileSize) {
		return "", fmt.Errorf("patched content exceeds maximum size of %d bytes", skills.MaxSkillFileSize)
	}
	if err := skills.ValidateContent(newContent, p.Name); err != nil {
		return "", fmt.Errorf("patched content: %w", err)
	}
	if err := os.WriteFile(skillMD, []byte(newContent), 0644); err != nil {
		return "", fmt.Errorf("write patched %s: %w", skills.SkillFile, err)
	}
	h.refreshSkillsPrompt(ctx)
	return fmt.Sprintf("skill patched: %s", p.Name), nil
}

func (h *skillsHandler) doDelete(ctx context.Context, p manageParams) (string, error) {
	skillDir, err := h.findWritableDir(p.Name)
	if err != nil {
		return "", err
	}
	if err := os.RemoveAll(skillDir); err != nil {
		return "", fmt.Errorf("remove skill directory: %w", err)
	}
	slog.Info("skill deleted", "name", p.Name)
	h.refreshSkillsPrompt(ctx)
	return fmt.Sprintf("skill deleted: %s", p.Name), nil
}

func (h *skillsHandler) doWriteFile(ctx context.Context, p manageParams) (string, error) {
	skillDir, fullPath, err := h.resolveLinkedFile(p.Name, p.FilePath)
	if err != nil {
		return "", err
	}
	if len(p.FileContent) > maxLinkedFileSize {
		return "", fmt.Errorf("file_content exceeds maximum size of %d bytes", maxLinkedFileSize)
	}
	// Verify file-level containment to catch symlink escapes.
	fi, err := os.Lstat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			// New file — ensure parent dir is contained and proceed.
			if err := skills.EnsureContainedDir(filepath.Dir(fullPath), skillDir); err != nil {
				return "", err
			}
		} else {
			return "", fmt.Errorf("stat file: %w", err)
		}
	} else if fi.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(fullPath)
		if err != nil {
			return "", fmt.Errorf("read symlink: %w", err)
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(fullPath), target)
		}
		if _, err := skills.VerifyContainment(target, skillDir); err != nil {
			if os.IsNotExist(err) {
				// Dangling symlink: verify the symlink path itself is contained.
				if _, err := skills.VerifyContainment(fullPath, skillDir); err != nil {
					return "", fmt.Errorf("symlink escapes skill directory: %w", err)
				}
			} else {
				return "", fmt.Errorf("symlink target escapes skill directory: %w", err)
			}
		}
	} else {
		if _, err := skills.VerifyContainment(fullPath, skillDir); err != nil {
			return "", err
		}
	}
	if err := os.WriteFile(fullPath, []byte(p.FileContent), 0644); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}
	return fmt.Sprintf("file written: %s/%s", p.Name, p.FilePath), nil
}

func (h *skillsHandler) doRemoveFile(ctx context.Context, p manageParams) (string, error) {
	skillDir, fullPath, err := h.resolveLinkedFile(p.Name, p.FilePath)
	if err != nil {
		return "", err
	}
	// For dangling symlinks, VerifyContainment fails because EvalSymlinks cannot resolve.
	// Use Lstat to check existence and Lstat-based containment for symlinks.
	fi, err := os.Lstat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("file not found: %q", p.FilePath)
		}
		return "", fmt.Errorf("stat file: %w", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(fullPath)
		if err != nil {
			return "", fmt.Errorf("read symlink: %w", err)
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(fullPath), target)
		}
		if _, err := skills.VerifyContainment(target, skillDir); err != nil {
			if !os.IsNotExist(err) {
				return "", fmt.Errorf("symlink target escapes skill directory: %w", err)
			}
			// Dangling symlink pointing within skill dir is safe to remove.
		}
	} else {
		if _, err := skills.VerifyContainment(fullPath, skillDir); err != nil {
			return "", err
		}
	}
	if err := os.Remove(fullPath); err != nil {
		return "", fmt.Errorf("remove file: %w", err)
	}
	return fmt.Sprintf("file removed: %s/%s", p.Name, p.FilePath), nil
}

// --- helpers ---

// refreshSkillsPrompt calls the refresher to rebuild the skills section
// of the system prompt. No-op when refresher is nil.
func (h *skillsHandler) refreshSkillsPrompt(ctx context.Context) {
	if h.refresher == nil {
		return
	}
	if err := h.refresher.RefreshSkillsPrompt(ctx); err != nil {
		slog.Warn("failed to refresh skills prompt", "error", err)
	}
}

func validateAndCheckContent(content, name string) error {
	if content == "" {
		return errors.New("content is required")
	}
	if len(content) > int(skills.MaxSkillFileSize) {
		return fmt.Errorf("content exceeds maximum size of %d bytes", skills.MaxSkillFileSize)
	}
	return skills.ValidateContent(content, name)
}

func (h *skillsHandler) resolveLinkedFile(name, filePath string) (string, string, error) {
	if filePath == "" {
		return "", "", errors.New("file_path is required")
	}
	if err := skills.ValidateLinkedFilePath(filePath); err != nil {
		return "", "", err
	}
	skillDir, err := h.findWritableDir(name)
	if err != nil {
		return "", "", err
	}
	return skillDir, filepath.Join(skillDir, filePath), nil
}

var _ cobot.Tool = (*fnTool)(nil)
