package memory

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/cobot-agent/cobot/internal/textutil"
	cobot "github.com/cobot-agent/cobot/pkg"
)

type Extractor struct {
	store    cobot.MemoryStore
	provider cobot.Provider
	model    string
}

func NewExtractor(store cobot.MemoryStore, provider cobot.Provider, model string) *Extractor {
	return &Extractor{store: store, provider: provider, model: model}
}

func (e *Extractor) Extract(ctx context.Context, summary string, originalMsgs []cobot.Message) error {
	var buf strings.Builder
	for i, m := range originalMsgs {
		if m.Role == cobot.RoleSystem {
			continue
		}
		if i >= 40 {
			buf.WriteString(fmt.Sprintf("\n... (%d more messages omitted)\n", len(originalMsgs)-40))
			break
		}
		buf.WriteString(fmt.Sprintf("[%s]: %s\n", m.Role, textutil.Truncate(m.Content, 300)))
		for _, tc := range m.ToolCalls {
			buf.WriteString(fmt.Sprintf("  tool_call: %s\n", tc.Name))
		}
		if m.ToolResult != nil && m.ToolResult.Output != "" {
			buf.WriteString(fmt.Sprintf("  tool_result: %s\n", textutil.Truncate(m.ToolResult.Output, 200)))
		}
	}

	req := &cobot.ProviderRequest{
		Model: e.model,
		Messages: []cobot.Message{
			{Role: cobot.RoleSystem, Content: extractionPrompt},
			{Role: cobot.RoleUser, Content: fmt.Sprintf(
				"<summary>\n%s\n</summary>\n\n<conversation>\n%s\n</conversation>",
				summary, buf.String(),
			)},
		},
	}

	resp, err := e.provider.Complete(ctx, req)
	if err != nil {
		return fmt.Errorf("memory extraction LLM call: %w", err)
	}

	items := parseExtractionResponse(resp.Content)
	if len(items) == 0 {
		slog.Debug("memory extraction: no items extracted")
		return nil
	}

	stored := 0
	rooms := make(map[string]struct{})
	for _, item := range items {
		_, err := e.store.StoreByName(ctx, item.content, "sessions", item.room, item.hallType)
		if err != nil {
			slog.Debug("memory extraction: store failed", "room", item.room, "err", err)
			continue
		}
		rooms[item.room] = struct{}{}
		stored++
	}

	for room := range rooms {
		if err := e.store.ConsolidateByName(ctx, "sessions", room); err != nil {
			slog.Debug("memory consolidation failed", "room", room, "err", err)
		}
	}

	slog.Debug("memory extraction complete", "extracted", len(items), "stored", stored)
	return nil
}

type extractedItem struct {
	content  string
	room     string
	hallType string
}

func parseExtractionResponse(response string) []extractedItem {
	lines := strings.Split(strings.TrimSpace(response), "\n")
	var items []extractedItem

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var room, hallType string
		switch {
		case strings.HasPrefix(line, "[FACT]"):
			line = strings.TrimPrefix(line, "[FACT]")
			room = "facts"
			hallType = cobot.TagFacts
		case strings.HasPrefix(line, "[DECISION]"):
			line = strings.TrimPrefix(line, "[DECISION]")
			room = "decisions"
			hallType = cobot.TagFacts
		case strings.HasPrefix(line, "[PATTERN]"):
			line = strings.TrimPrefix(line, "[PATTERN]")
			room = "patterns"
			hallType = cobot.TagCode
		case strings.HasPrefix(line, "[PREFERENCE]"):
			line = strings.TrimPrefix(line, "[PREFERENCE]")
			room = "preferences"
			hallType = cobot.TagFacts
		default:
			continue
		}

		content := strings.TrimSpace(line)
		if content == "" {
			continue
		}

		items = append(items, extractedItem{
			content:  content,
			room:     room,
			hallType: hallType,
		})
	}

	return items
}

const extractionPrompt = `You are a memory extraction engine. Given a conversation summary and the original conversation, extract the most important items worth remembering for future sessions.

Extract ONLY items that are:
- Durable: still relevant in future conversations (not ephemeral status updates)
- Specific: contain concrete names, paths, numbers, or decisions (not vague observations)
- Actionable: inform future behavior or decisions

Categorize each item with exactly one tag:
- [FACT] — A concrete fact about the project, codebase, user, or environment
- [DECISION] — A decision made and its rationale
- [PATTERN] — A code pattern, convention, or architectural approach established
- [PREFERENCE] — A user preference about workflow, style, or tooling

Output one item per line, prefixed with its tag. No numbering, no bullets, no commentary.
If nothing is worth extracting, output a single line: NONE

Examples:
[FACT] The project uses SQLite with WAL mode for the MemPalace memory backend
[DECISION] Session compression threshold set to 70% of context window because lower values caused too-frequent compression
[PATTERN] Error handling in providers follows: wrap with fmt.Errorf("method: %w", err), never suppress
[PREFERENCE] User prefers Chinese for discussion but English for code comments`
