package memory

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	cobot "github.com/cobot-agent/cobot/pkg"
)

// WorkflowAnalyzer periodically analyzes LTM workflow patterns and generates
// auto-generated skill content.
type WorkflowAnalyzer struct {
	store     cobot.MemoryStore
	provider  cobot.Provider
	model     string
	skillsDir string
	lastHash  string
	mu        sync.Mutex
}

// NewWorkflowAnalyzer creates a new WorkflowAnalyzer.
func NewWorkflowAnalyzer(store cobot.MemoryStore, provider cobot.Provider, model, skillsDir string) *WorkflowAnalyzer {
	return &WorkflowAnalyzer{
		store:     store,
		provider:  provider,
		model:     model,
		skillsDir: skillsDir,
	}
}

// Analyze scans LTM "sessions" wing for workflow, preference, and decision
// items, analyzes them with an LLM, and returns a skill content string if
// the content has changed since the last call.
func (wa *WorkflowAnalyzer) Analyze(ctx context.Context) (skillContent string, changed bool, err error) {
	// Search for recent session memories.
	items, err := wa.searchSessionMemories(ctx)
	if err != nil {
		return "", false, fmt.Errorf("search session memories: %w", err)
	}
	if len(items) == 0 {
		return "", false, nil
	}

	prompt := buildWorkflowAnalysisPrompt(items)

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	req := &cobot.ProviderRequest{
		Model: wa.model,
		Messages: []cobot.Message{
			{Role: cobot.RoleSystem, Content: workflowAnalysisSystemPrompt},
			{Role: cobot.RoleUser, Content: prompt},
		},
	}

	resp, err := wa.provider.Complete(ctx, req)
	if err != nil {
		return "", false, fmt.Errorf("workflow analysis LLM call: %w", err)
	}

	content := resp.Content
	if strings.TrimSpace(content) == "" || strings.EqualFold(strings.TrimSpace(content), "NONE") {
		return "", false, nil
	}

	hash := hashContent(content)

	wa.mu.Lock()
	defer wa.mu.Unlock()
	if hash == wa.lastHash {
		return "", false, nil
	}
	wa.lastHash = hash

	return content, true, nil
}

// SkillsDir returns the skills directory path.
func (wa *WorkflowAnalyzer) SkillsDir() string {
	return wa.skillsDir
}

func (wa *WorkflowAnalyzer) searchSessionMemories(ctx context.Context) ([]string, error) {
	var items []string

	// Search facts room.
	results, err := wa.store.Search(ctx, &cobot.SearchQuery{
		Tier1: "sessions",
		Tier2: "facts",
		Limit: 20,
	})
	if err != nil {
		slog.Debug("workflow analyzer: search facts failed", "error", err)
	} else {
		for _, r := range results {
			items = append(items, r.Content)
		}
	}

	// Search patterns room.
	results, err = wa.store.Search(ctx, &cobot.SearchQuery{
		Tier1: "sessions",
		Tier2: "patterns",
		Limit: 20,
	})
	if err != nil {
		slog.Debug("workflow analyzer: search patterns failed", "error", err)
	} else {
		for _, r := range results {
			items = append(items, r.Content)
		}
	}

	return items, nil
}

func buildWorkflowAnalysisPrompt(items []string) string {
	var b strings.Builder
	b.WriteString("User workflow patterns from past sessions:\n\n")
	for i, item := range items {
		fmt.Fprintf(&b, "%d. %s\n", i+1, item)
	}
	return b.String()
}

func hashContent(content string) string {
	sum := sha256.Sum256([]byte(content))
	return fmt.Sprintf("%x", sum)
}

const workflowAnalysisSystemPrompt = `You are a workflow analyst. Given a list of user workflow patterns, preferences, and decisions from past coding sessions, synthesize them into a concise skill document.

Identify:
- Recurring processes the user follows
- Tools and commands the user habitually uses
- Decision patterns and architectural preferences
- Style conventions and quality standards

Output a single SKILL.md document with YAML frontmatter:

---
name: auto-user-workflow-patterns
description: Auto-generated summary of user workflow patterns
category: auto-generated
---

Then a markdown body describing the workflow patterns. Be specific and actionable. If there is not enough information to generate a meaningful skill, output: NONE`
