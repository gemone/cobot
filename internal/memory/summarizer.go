package memory

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	cobot "github.com/cobot-agent/cobot/pkg"
)

// Insight represents a structured insight extracted from STM items by the LLM.
type Insight struct {
	Content  string
	Category string // "fact" or "pattern"
}

// Summarizer uses an LLM to intelligently summarize STM items into structured
// insights before promoting them to long-term memory.
type Summarizer struct {
	provider cobot.Provider
	model    string
}

// NewSummarizer creates a new Summarizer with the given provider and model.
func NewSummarizer(provider cobot.Provider, model string) *Summarizer {
	return &Summarizer{provider: provider, model: model}
}

// Summarize takes STM items and an optional compressed summary and returns
// structured insights. On LLM failure, returns an empty slice (caller should
// fall back to dumb copy).
func (s *Summarizer) Summarize(ctx context.Context, items []STMItem, compressedSummary string) ([]Insight, error) {
	if len(items) == 0 && compressedSummary == "" {
		return nil, nil
	}
	prompt := buildSummarizationPrompt(items, compressedSummary)
	return s.callLLM(ctx, prompt)
}

func (s *Summarizer) callLLM(ctx context.Context, userPrompt string) ([]Insight, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req := &cobot.ProviderRequest{
		Model: s.model,
		Messages: []cobot.Message{
			{Role: cobot.RoleSystem, Content: summarizationSystemPrompt},
			{Role: cobot.RoleUser, Content: userPrompt},
		},
	}

	resp, err := s.provider.Complete(ctx, req)
	if err != nil {
		slog.Debug("summarizer LLM call failed", "error", err)
		return nil, nil
	}

	insights := parseSummarizationResponse(resp.Content)
	slog.Debug("summarizer extracted insights", "count", len(insights))
	return insights, nil
}

func buildSummarizationPrompt(items []STMItem, compressedSummary string) string {
	var b strings.Builder
	if compressedSummary != "" {
		b.WriteString("Compressed session summary:\n")
		b.WriteString(compressedSummary)
		b.WriteString("\n\n")
	}
	b.WriteString("Short-term memory items:\n\n")
	for _, item := range items {
		fmt.Fprintf(&b, "[%s] %s\n", item.Category, item.Content)
	}
	return b.String()
}

// parseSummarizationResponse parses the LLM response into insights using a
// block-based parser that looks for [FACT] and [PATTERN] tags.
func parseSummarizationResponse(response string) []Insight {
	response = strings.TrimSpace(response)
	if strings.EqualFold(response, "NONE") {
		return nil
	}

	var insights []Insight

	// Find all tag positions first.
	tagRe := regexp.MustCompile(`(?m)^\[(FACT|PATTERN)\]\s*`)
	matches := tagRe.FindAllStringIndex(response, -1)

	for i, m := range matches {
		start := m[0]
		end := len(response)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		block := response[start:end]

		// Extract category.
		catMatch := tagRe.FindStringSubmatchIndex(block)
		if catMatch == nil {
			continue
		}
		catStart, catEnd := catMatch[2], catMatch[3]
		category := strings.ToLower(block[catStart:catEnd])

		// Remove the tag from the block to get content.
		contentBlock := block[catMatch[1]:]
		lines := strings.Split(contentBlock, "\n")
		var contentLines []string
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			contentLines = append(contentLines, line)
		}
		content := strings.TrimSpace(strings.Join(contentLines, " "))
		if content == "" {
			continue
		}

		insights = append(insights, Insight{
			Content:  content,
			Category: category,
		})
	}

	return insights
}

// insightRoomMapping maps insight categories to LTM room names and hall types.
func insightRoomMapping(category string) (roomName, hallType string) {
	switch category {
	case "fact":
		return "facts", cobot.TagFacts
	case "pattern":
		return "patterns", "pattern"
	default:
		return "facts", cobot.TagFacts
	}
}

const summarizationSystemPrompt = `You are a memory curator. Extract durable insights from short-term memory items.

Classify each insight as:
- [FACT] — technical knowledge about the project, codebase, or environment
- [PATTERN] — the user's workflow, preferences, habits, or decisions

Output format:
[FACT] The project uses PostgreSQL with connection pooling
[PATTERN] User runs tests before every commit and prefers inline error handling

Rules:
- Extract only durable, specific, actionable items
- Skip ephemeral status updates and temporary TODOs
- If nothing is worth extracting, output: NONE`
