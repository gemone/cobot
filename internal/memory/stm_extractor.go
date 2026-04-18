package memory

import (
	"strings"

	"github.com/cobot-agent/cobot/internal/textutil"
)

// STMItem represents a single short-term memory item extracted from a
// conversation turn. Extraction is rule-based (no LLM calls) to keep it
// fast and cheap.
type STMItem struct {
	Content  string
	Category string // "task_state", "decision", "context", "observation", "todo", "requirement", "note", "error"
}

const stmMaxPerCategory = 5

// ExtractSTM performs lightweight rule-based extraction of short-term memory
// items from the current conversation turn.
func ExtractSTM(userMsg, assistantMsg string, toolResults []string) []STMItem {
	var items []STMItem

	// Extract user directives and requirements (context/requirement).
	if userMsg != "" {
		items = append(items, extractUserItems(userMsg)...)
	}

	// Extract tool result summaries (observation/error).
	for _, result := range toolResults {
		if isSignificant(result) {
			items = append(items, STMItem{
				Content:  summarizeToolResult(result),
				Category: toolResultCategory(result),
			})
		}
	}

	// Extract assistant response summary (only if it contains key information).
	if assistantMsg != "" && len(assistantMsg) > 20 {
		// Check for TODO/FIXME items in assistant message.
		todoItems := extractTODOItems(assistantMsg)
		items = append(items, todoItems...)

		summary := textutil.Truncate(assistantMsg, 200)
		items = append(items, STMItem{
			Content:  "Assistant: " + summary,
			Category: "task_state",
		})
	}

	// Apply per-category cap, then total cap.
	items = capPerCategory(items)

	if len(items) > stmMaxItems {
		items = items[len(items)-stmMaxItems:]
	}

	return items
}

// extractUserItems extracts context and requirement items from user messages.
func extractUserItems(userMsg string) []STMItem {
	var items []STMItem

	// Check for requirement phrases.
	if hasRequirementPhrase(userMsg) {
		items = append(items, STMItem{
			Content:  "Requirement: " + textutil.Truncate(userMsg, 200),
			Category: "requirement",
		})
	}

	// Check for TODO items mentioned by user.
	todoItems := extractTODOItems(userMsg)
	items = append(items, todoItems...)

	// Always store user message as context.
	items = append(items, STMItem{
		Content:  "User: " + textutil.Truncate(userMsg, 200),
		Category: "context",
	})

	return items
}

// hasRequirementPhrase checks if the message contains requirement indicators.
func hasRequirementPhrase(msg string) bool {
	lower := strings.ToLower(msg)
	indicators := []string{
		"用户要求",
		"需要",
		"must ",
		"should ",
		"need to ",
		"needs to ",
		"please ",
		"make sure",
		"ensure that",
		"i want",
		"i need",
		"we need",
		"required:",
	}
	for _, indicator := range indicators {
		if strings.Contains(lower, indicator) {
			return true
		}
	}
	return false
}

// extractTODOItems extracts TODO/FIXME/HACK items from text.
func extractTODOItems(text string) []STMItem {
	var items []STMItem
	lines := strings.Split(text, "\n")
	todoKeywords := []string{"TODO", "FIXME", "HACK", "todo:", "fixme:", "hack:"}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		upper := strings.ToUpper(trimmed)
		for _, kw := range todoKeywords {
			if strings.Contains(upper, strings.ToUpper(kw)) {
				items = append(items, STMItem{
					Content:  textutil.Truncate(trimmed, 200),
					Category: "todo",
				})
				break
			}
		}
	}
	return items
}

// toolResultCategory determines the category for a tool result.
func toolResultCategory(result string) string {
	lower := strings.ToLower(result)
	if strings.Contains(lower, "error") || strings.Contains(lower, "fatal") ||
		strings.Contains(lower, "panic") || strings.Contains(lower, "failed") {
		return "error"
	}
	return "observation"
}

// capPerCategory limits each category to stmMaxPerCategory items,
// keeping the most recent ones.
func capPerCategory(items []STMItem) []STMItem {
	counts := make(map[string]int)
	var capped []STMItem

	// Walk backwards to keep most recent, then reverse.
	for i := len(items) - 1; i >= 0; i-- {
		cat := items[i].Category
		if counts[cat] < stmMaxPerCategory {
			capped = append(capped, items[i])
			counts[cat]++
		}
	}

	// Reverse to restore original order.
	for left, right := 0, len(capped)-1; left < right; left, right = left+1, right-1 {
		capped[left], capped[right] = capped[right], capped[left]
	}

	return capped
}

// isSignificant returns true if the tool result contains useful information
// worth keeping in short-term memory.
func isSignificant(result string) bool {
	return len(strings.TrimSpace(result)) >= 5
}

// summarizeToolResult creates a brief summary of a tool result for STM.
func summarizeToolResult(result string) string {
	// Check for common patterns.
	lower := strings.ToLower(result)

	// Build / test results.
	if strings.Contains(lower, "build") || strings.Contains(lower, "compil") {
		if strings.Contains(lower, "error") || strings.Contains(lower, "fail") {
			return "Build failed: " + textutil.Truncate(result, 150)
		}
		return "Build passed: " + textutil.Truncate(result, 100)
	}

	if strings.Contains(lower, "test") {
		if strings.Contains(lower, "fail") {
			return "Tests failed: " + textutil.Truncate(result, 150)
		}
		if strings.Contains(lower, "pass") {
			return "Tests passed: " + textutil.Truncate(result, 100)
		}
	}

	// Error states.
	if strings.Contains(lower, "error") || strings.Contains(lower, "fatal") {
		return "Error: " + textutil.Truncate(result, 150)
	}

	// Default: truncate to reasonable length.
	return textutil.Truncate(result, 200)
}
