package agent

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"

	"github.com/cobot-agent/cobot/internal/skills"
	"github.com/cobot-agent/cobot/internal/workspace"
	cobot "github.com/cobot-agent/cobot/pkg"
)

// SkillManager maintains an in-memory index of skills for fast lookups
// without scanning the filesystem on every list/view operation.
type SkillManager struct {
	mu        sync.RWMutex
	catalog   map[string]skills.Skill // keyed by skill name
	dirs      []string                // global dir first, workspace dir second (workspace wins)
	ws        *workspace.Workspace
	refresher cobot.SkillsPromptRefresher
}

// NewSkillManager creates a SkillManager and loads the initial catalog.
func NewSkillManager(ws *workspace.Workspace, refresher cobot.SkillsPromptRefresher) (*SkillManager, error) {
	dirs := []string{workspace.GlobalSkillsDir(), ws.SkillsDir()}
	sm := &SkillManager{
		catalog:   make(map[string]skills.Skill),
		dirs:      dirs,
		ws:        ws,
		refresher: refresher,
	}
	if err := sm.Reload(context.Background()); err != nil {
		return nil, err
	}
	return sm, nil
}

// Reload re-scans the skills directories and rebuilds the in-memory catalog.
func (sm *SkillManager) Reload(ctx context.Context) error {
	skList, err := skills.LoadCatalog(ctx, sm.dirs, nil)
	if err != nil {
		return err
	}
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.catalog = make(map[string]skills.Skill, len(skList))
	for _, sk := range skList {
		sm.catalog[sk.Name] = sk
	}
	slog.Debug("skill manager: reloaded catalog", "count", len(sm.catalog))
	return nil
}

// List returns all skills in the catalog, optionally filtered by category.
func (sm *SkillManager) List(ctx context.Context, category string) []skills.Skill {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	if category == "" {
		result := make([]skills.Skill, 0, len(sm.catalog))
		for _, sk := range sm.catalog {
			result = append(result, sk)
		}
		return result
	}
	var result []skills.Skill
	for _, sk := range sm.catalog {
		if sk.Category == category {
			result = append(result, sk)
		}
	}
	return result
}

// Get returns a skill by name, using the in-memory catalog.
func (sm *SkillManager) Get(name string) (skills.Skill, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	sk, ok := sm.catalog[name]
	return sk, ok
}

// ErrSkillNotFound is returned when a requested skill is not in the catalog.
var ErrSkillNotFound = errors.New("skill not found")

// View returns the full skill content for the given name.
// It first checks the in-memory catalog for the skill path, then reads
// the SKILL.md file and linked files.
func (sm *SkillManager) View(ctx context.Context, name string) (string, error) {
	sm.mu.RLock()
	sk, ok := sm.catalog[name]
	sm.mu.RUnlock()
	if !ok {
		return "", ErrSkillNotFound
	}

	if sk.Dir == "" {
		return "", ErrSkillNotFound
	}

	skillPath := filepath.Join(sk.Dir, skills.SkillFile)
	data, err := skills.ReadFileWithLimit(skillPath, skills.MaxSkillFileSize)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.Write(data)

	// Append linked files index.
	var linked []string
	for subdir, files := range skills.ListLinkedFiles(sk.Dir) {
		for _, f := range files {
			linked = append(linked, filepath.Join(subdir, f))
		}
	}
	if len(linked) > 0 {
		b.WriteString("\n\n## Linked Files\n")
		for _, p := range linked {
			b.WriteString("- " + p + "\n")
		}
	}
	return b.String(), nil
}

// ReadLinkedFile reads a specific linked file within a skill.
func (sm *SkillManager) ReadLinkedFile(ctx context.Context, name, filePath string) (string, error) {
	sm.mu.RLock()
	sk, ok := sm.catalog[name]
	sm.mu.RUnlock()
	if !ok || sk.Dir == "" {
		return "", ErrSkillNotFound
	}
	skillDir, err := skills.FindSkillDir(sk.Dir, name)
	if err != nil {
		return "", err
	}
	return skills.ReadLinkedFile(skillDir, filePath)
}

// SkillsDirs returns the skills directories in priority order (workspace wins).
func (sm *SkillManager) SkillsDirs() []string {
	return sm.dirs
}

// RefreshPrompt calls the system prompt refresher after catalog mutations.
func (sm *SkillManager) RefreshPrompt(ctx context.Context) {
	if sm.refresher == nil {
		return
	}
	if err := sm.refresher.RefreshSkillsPrompt(ctx); err != nil {
		slog.Warn("skill manager: failed to refresh skills prompt", "error", err)
	}
}

// Count returns the number of skills in the catalog.
func (sm *SkillManager) Count() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return len(sm.catalog)
}
