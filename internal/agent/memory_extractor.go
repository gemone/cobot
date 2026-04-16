package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	cobot "github.com/cobot-agent/cobot/pkg"
)

// extractMemories extracts memorable facts, decisions, and patterns from a
// compressed session and persists them to the MemPalace memory store.
//
// It runs as a background goroutine so it does not block the main conversation.
// The method is a no-op when memoryStore is nil.
func (a *Agent) extractMemories(ctx context.Context, summary string, originalMsgs []cobot.Message) {
	if a.memoryStore == nil || a.provider == nil {
		return
	}

	// Capture values before launching goroutine to avoid data race on
	// a.compressor, a.config, and a.memoryStore which may be modified
	// concurrently by SetModel/initCompressor or Close.
	model := a.compressorModel()
	store := a.memoryStore
	provider := a.provider

	go func() {
		if err := a.doExtractMemoriesWith(ctx, summary, originalMsgs, model, store, provider); err != nil {
			slog.Debug("memory extraction failed", "err", err)
		}
	}()
}

func (a *Agent) doExtractMemoriesWith(ctx context.Context, summary string, originalMsgs []cobot.Message, model string, store cobot.MemoryStore, provider cobot.Provider) error {
	// Build a concise representation of the original conversation for the LLM.
	var conversationBuf strings.Builder
	for i, m := range originalMsgs {
		if m.Role == cobot.RoleSystem {
			continue
		}
		// Cap to first 40 messages to keep the extraction prompt manageable.
		if i >= 40 {
			conversationBuf.WriteString(fmt.Sprintf("\n... (%d more messages omitted)\n", len(originalMsgs)-40))
			break
		}
		conversationBuf.WriteString(fmt.Sprintf("[%s]: %s\n", m.Role, truncate(m.Content, 300)))
		for _, tc := range m.ToolCalls {
			conversationBuf.WriteString(fmt.Sprintf("  tool_call: %s\n", tc.Name))
		}
		if m.ToolResult != nil && m.ToolResult.Output != "" {
			conversationBuf.WriteString(fmt.Sprintf("  tool_result: %s\n", truncate(m.ToolResult.Output, 200)))
		}
	}

	userContent := fmt.Sprintf(
		"<summary>\n%s\n</summary>\n\n<conversation>\n%s\n</conversation>",
		summary,
		conversationBuf.String(),
	)

	req := &cobot.ProviderRequest{
		Model: model,
		Messages: []cobot.Message{
			{Role: cobot.RoleSystem, Content: memoryExtractionPrompt},
			{Role: cobot.RoleUser, Content: userContent},
		},
	}

	resp, err := provider.Complete(ctx, req)
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
		_, err := store.StoreByName(ctx, item.content, "sessions", item.room, item.hallType)
		if err != nil {
			slog.Debug("memory extraction: store failed", "room", item.room, "err", err)
			continue
		}
		rooms[item.room] = struct{}{}
		stored++
	}

	for room := range rooms {
		if err := store.ConsolidateByName(ctx, "sessions", room); err != nil {
			slog.Debug("memory consolidation failed", "room", room, "err", err)
		}
	}

	slog.Debug("memory extraction complete", "extracted", len(items), "stored", stored)
	return nil
}

// compressorModel returns the model to use for summarization/extraction LLM calls.
func (a *Agent) compressorModel() string {
	if a.compressor != nil && a.compressor.summaryModel != "" {
		return a.compressor.summaryModel
	}
	return a.config.Model
}

// memoryItem represents a single extracted memory entry.
type memoryItem struct {
	content  string
	room     string // target room name in the "sessions" wing
	hallType string // cobot.TagFacts, cobot.TagLog, or cobot.TagCode
}

// parseExtractionResponse parses the structured LLM response into memory items.
//
// Expected format from the LLM (one item per block):
//
//	[FACT] content here
//	[DECISION] content here
//	[PATTERN] content here
//	[PREFERENCE] content here
func parseExtractionResponse(response string) []memoryItem {
	lines := strings.Split(strings.TrimSpace(response), "\n")
	var items []memoryItem

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
			// Skip lines that don't match any tag — LLM noise.
			continue
		}

		content := strings.TrimSpace(line)
		if content == "" {
			continue
		}

		items = append(items, memoryItem{
			content:  content,
			room:     room,
			hallType: hallType,
		})
	}

	return items
}

const memoryExtractionPrompt = `You are a memory extraction engine. Given a conversation summary and the original conversation, extract the most important items worth remembering for future sessions.

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
