package skills

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SkillsSectionMarker is the header marker for the skills section in system prompts.
const SkillsSectionMarker = "## Skills (mandatory)"

// LoadCatalog discovers all skills and returns full Skill objects (with tier-1 info populated).
// Scans dirs in order; later dirs override earlier (workspace > global).
// enabledFilter: if non-empty, only include named skills.
func LoadCatalog(ctx context.Context, dirs []string, enabledFilter []string) ([]Skill, error) {
	merged := make(map[string]Skill)
	if err := scanAllDirs(ctx, dirs, func(sk Skill) { merged[sk.Name] = sk }); err != nil {
		return nil, err
	}

	if len(enabledFilter) > 0 {
		allow := make(map[string]struct{}, len(enabledFilter))
		for _, n := range enabledFilter {
			allow[n] = struct{}{}
		}
		for name := range merged {
			if _, ok := allow[name]; !ok {
				delete(merged, name)
			}
		}
	}

	result := make([]Skill, 0, len(merged))
	for _, sk := range merged {
		result = append(result, sk)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

// LoadFull loads tier-2 content for a specific skill by name.
// Searches all dirs in order; workspace version wins (last-match).
func LoadFull(ctx context.Context, dirs []string, name string) (*Skill, error) {
	if err := ValidateSkillName(name); err != nil {
		return nil, err
	}
	// Use last-match-wins semantics, same as LoadOne.
	var found *Skill
	for i, dir := range dirs {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		skillPath := filepath.Join(dir, name, SkillFile)
		if _, err := os.Stat(skillPath); err != nil {
			continue
		}
		src := sourceLabel(i)
		sk, err := loadNewFormatSkill(filepath.Join(dir, name), "", src)
		if err != nil {
			slog.Warn("failed to load skill in fast path", "path", filepath.Join(dir, name), "error", err)
			continue
		}
		if sk.Name == name {
			s := sk
			found = &s
		}
	}
	if found != nil {
		return found, nil
	}
	// Fallback: scan for categorized or legacy skills.
	if err := scanAllDirs(ctx, dirs, func(sk Skill) {
		if sk.Name == name {
			// For new-format skills found in fallback, reload to get full content.
			if sk.Dir != "" {
				if full, err := loadNewFormatSkill(sk.Dir, sk.Category, sk.Source); err == nil {
					sk = full
				}
			}
			s := sk
			found = &s
		}
	}); err != nil {
		return nil, err
	}
	if found != nil {
		return found, nil
	}
	return nil, fmt.Errorf("skill not found: %q", name)
}

// LoadOne loads a single skill by name, searching workspace then global dirs.
// More efficient than LoadFull for single-skill lookups — avoids scanning all skills.
func LoadOne(ctx context.Context, dirs []string, name string) (*Skill, error) {
	if err := ValidateSkillName(name); err != nil {
		return nil, err
	}
	// Use last-match-wins semantics (workspace overrides global), consistent with LoadCatalog.
	var found *Skill
	for i, dir := range dirs {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		skillPath := filepath.Join(dir, name, SkillFile)
		if _, err := os.Stat(skillPath); err != nil {
			continue
		}
		src := sourceLabel(i)
		sk, err := loadNewFormatSkill(filepath.Join(dir, name), "", src)
		if err != nil {
			slog.Warn("failed to load skill in fast path", "path", filepath.Join(dir, name), "error", err)
			continue
		}
		if sk.Name == name {
			s := sk
			found = &s
		}
	}
	if found != nil {
		return found, nil
	}
	// Fallback: scan for categorized or legacy skills (also last-match-wins)
	if err := scanAllDirs(ctx, dirs, func(sk Skill) {
		if sk.Name == name {
			// For new-format skills found in fallback, reload to get full content.
			if sk.Dir != "" {
				if full, err := loadNewFormatSkill(sk.Dir, sk.Category, sk.Source); err == nil {
					sk = full
				}
			}
			s := sk
			found = &s
		}
	}); err != nil {
		return nil, err
	}
	if found != nil {
		return found, nil
	}
	return nil, fmt.Errorf("skill not found: %q", name)
}

// SkillsToPrompt formats tier-1 catalog for system prompt injection.
// Skills are grouped by category (sorted alphabetically); skills without a
// category are placed under "general". The output is wrapped in an
// <available_skills> XML block with mandatory-load semantic guidance.
func SkillsToPrompt(skills []Skill) string {
	if len(skills) == 0 {
		return ""
	}

	// Group by category.
	groups := make(map[string][]Skill)
	for _, sk := range skills {
		cat := sk.Category
		if cat == "" {
			cat = "general"
		}
		groups[cat] = append(groups[cat], sk)
	}

	// Sort category names.
	cats := make([]string, 0, len(groups))
	for cat := range groups {
		cats = append(cats, cat)
	}
	sort.Strings(cats)

	var b strings.Builder
	b.WriteString(SkillsSectionMarker + "\n")
	b.WriteString("Before replying, scan the skills below. If a skill matches or is even partially relevant to your task, you MUST load it with skill_view(name) and follow its instructions. Err on the side of loading — it is always better to have context you don't need than to miss critical steps, pitfalls, or established workflows. Skills contain specialized knowledge — API endpoints, tool-specific commands, and proven workflows that outperform general-purpose approaches. Load the skill even if you think you could handle the task with basic tools.\n\n")
	b.WriteString("If a skill has issues, fix it with skill_manage(action='patch').\n")
	b.WriteString("After difficult/iterative tasks, offer to save as a skill. If a skill you loaded was missing steps, had wrong commands, or needed pitfalls you discovered, update it before finishing.\n\n")
	b.WriteString("<available_skills>\n")

	for _, cat := range cats {
		b.WriteString("  " + cat + ":\n")
		members := groups[cat]
		// Sort skills within category by name.
		sort.Slice(members, func(i, j int) bool { return members[i].Name < members[j].Name })
		for _, sk := range members {
			b.WriteString("    - " + sk.Name + ": " + sk.Description + "\n")
		}
	}

	b.WriteString("</available_skills>\n\n")
	b.WriteString("Only proceed without loading a skill if genuinely none are relevant to the task.\n")
	return b.String()
}

// scanAllDirs iterates over dirs, scanning each and calling cb for every discovered skill.
// Later dirs override earlier (last callback wins for a given name).
// Returns first non-NotExist error encountered.
func scanAllDirs(ctx context.Context, dirs []string, cb func(Skill)) error {
	for i, dir := range dirs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		scanned, err := scanDir(dir, i)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("read skills dir %s: %w", dir, err)
		}
		for _, sk := range scanned {
			cb(sk)
		}
	}
	return nil
}

// scanDir scans a single skills directory for new-format skills (subdirectories with SKILL.md).
func scanDir(dir string, dirIndex int) ([]Skill, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	src := sourceLabel(dirIndex)
	var result []Skill

	for _, ent := range ents {
		if !ent.IsDir() {
			continue
		}
		skillPath := filepath.Join(dir, ent.Name(), SkillFile)
		if _, err := os.Stat(skillPath); err == nil {
			if sk, ok := tryLoadSkillDir(dir, ent.Name(), "", src); ok {
				result = append(result, sk)
			}
			continue
		}
		result = append(result, scanCategoryDir(dir, ent.Name(), src)...)
	}
	return result, nil
}

// sourceLabel returns "global" for index 0, "workspace" otherwise.
func sourceLabel(dirIndex int) string {
	if dirIndex == 0 {
		return "global"
	}
	return "workspace"
}

// tryLoadSkillDir attempts to load a SkillFile from parent/name/.
func tryLoadSkillDir(parent, name, category, src string) (Skill, bool) {
	sk, err := loadFrontmatterOnly(filepath.Join(parent, name), category, src)
	if err != nil {
		slog.Warn("failed to load skill", "path", filepath.Join(parent, name), "error", err)
		return Skill{}, false
	}
	return sk, true
}

// scanCategoryDir scans a category directory for skill subdirectories.
func scanCategoryDir(parent, catName, src string) []Skill {
	if !isValidCategoryName(catName) {
		return nil
	}
	catPath := filepath.Join(parent, catName)
	catEnts, err := os.ReadDir(catPath)
	if err != nil {
		slog.Warn("failed to read category directory", "path", catPath, "error", err)
		return nil
	}
	var result []Skill
	for _, catEnt := range catEnts {
		if catEnt.IsDir() {
			if sk, ok := tryLoadSkillDir(catPath, catEnt.Name(), catName, src); ok {
				result = append(result, sk)
			}
		}
	}
	return result
}
