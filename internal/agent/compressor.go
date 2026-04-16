package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"

	cobot "github.com/cobot-agent/cobot/pkg"
)

type SummaryProvider interface {
	Complete(ctx context.Context, req *cobot.ProviderRequest) (*cobot.ProviderResponse, error)
}

type Compressor struct {
	sessionCfg    cobot.SessionConfig
	contextWindow int
	provider      SummaryProvider
	summaryModel  string
}

func NewCompressor(cfg cobot.SessionConfig, contextWindow int, provider SummaryProvider, currentModel string) *Compressor {
	model := cfg.SummaryModel
	if model == "" {
		model = currentModel
	}
	return &Compressor{
		sessionCfg:    cfg,
		contextWindow: contextWindow,
		provider:      provider,
		summaryModel:  model,
	}
}

type CompressAction int

const (
	CompressNone      CompressAction = iota
	CompressSummarize                // summarize older messages, keep recent
	CompressFull                     // aggressive compression of the entire context
)

func (c *Compressor) Check(usage cobot.Usage, turnCount int) CompressAction {
	if c.contextWindow <= 0 {
		return CompressNone
	}

	ratio := float64(usage.TotalTokens) / float64(c.contextWindow)

	if c.sessionCfg.CompressThreshold > 0 && ratio >= c.sessionCfg.CompressThreshold {
		return CompressFull
	}

	if c.sessionCfg.SummarizeThreshold > 0 && ratio >= c.sessionCfg.SummarizeThreshold {
		return CompressSummarize
	}

	if c.sessionCfg.SummarizeTurns > 0 && turnCount >= c.sessionCfg.SummarizeTurns {
		return CompressSummarize
	}

	return CompressNone
}

func (c *Compressor) Summarize(ctx context.Context, messages []cobot.Message) (summary string, kept []cobot.Message, err error) {
	if len(messages) < 4 {
		return "", messages, nil
	}

	// Keep the most recent 1/3 of messages (minimum 2), summarize the rest.
	keepCount := len(messages) / 3
	if keepCount < 2 {
		keepCount = 2
	}
	toSummarize := messages[:len(messages)-keepCount]
	kept = messages[len(messages)-keepCount:]

	summary, err = c.callLLMSummarize(ctx, toSummarize, false)
	if err != nil {
		return "", messages, fmt.Errorf("summarize: %w", err)
	}

	return summary, kept, nil
}

func (c *Compressor) Compress(ctx context.Context, messages []cobot.Message) (summary string, err error) {
	if len(messages) == 0 {
		return "", nil
	}
	return c.callLLMSummarize(ctx, messages, true)
}

func (c *Compressor) callLLMSummarize(ctx context.Context, messages []cobot.Message, aggressive bool) (string, error) {
	if c.provider == nil {
		return "", fmt.Errorf("no provider available for summarization")
	}

	var sb strings.Builder
	for _, m := range messages {
		sb.WriteString(fmt.Sprintf("[%s]: %s\n", m.Role, m.Content))
		for _, tc := range m.ToolCalls {
			sb.WriteString(fmt.Sprintf("  tool_call: %s(%s)\n", tc.Name, string(tc.Arguments)))
		}
		if m.ToolResult != nil {
			sb.WriteString(fmt.Sprintf("  tool_result: %s\n", truncate(m.ToolResult.Output, 500)))
		}
	}

	prompt := summarizePrompt
	if aggressive {
		prompt = compressPrompt
	}

	req := &cobot.ProviderRequest{
		Model: c.summaryModel,
		Messages: []cobot.Message{
			{Role: cobot.RoleSystem, Content: prompt},
			{Role: cobot.RoleUser, Content: sb.String()},
		},
	}

	resp, err := c.provider.Complete(ctx, req)
	if err != nil {
		return "", fmt.Errorf("LLM summarize call: %w", err)
	}

	return strings.TrimSpace(resp.Content), nil
}

func (c *Compressor) OptimizeSummary(ctx context.Context, summary string, originalMessages []cobot.Message) (string, error) {
	if c.provider == nil {
		return summary, nil
	}

	var sb strings.Builder
	for i, m := range originalMessages {
		if i >= 10 {
			sb.WriteString(fmt.Sprintf("... (%d more messages)\n", len(originalMessages)-10))
			break
		}
		sb.WriteString(fmt.Sprintf("[%s]: %s\n", m.Role, truncate(m.Content, 200)))
	}

	req := &cobot.ProviderRequest{
		Model: c.summaryModel,
		Messages: []cobot.Message{
			{Role: cobot.RoleSystem, Content: optimizeSummaryPrompt},
			{Role: cobot.RoleUser, Content: fmt.Sprintf("<current_summary>\n%s\n</current_summary>\n\n<original_conversation>\n%s\n</original_conversation>", summary, sb.String())},
		},
	}

	resp, err := c.provider.Complete(ctx, req)
	if err != nil {
		slog.Debug("summary optimization failed, using original", "err", err)
		return summary, nil
	}

	optimized := strings.TrimSpace(resp.Content)
	if len(optimized) == 0 {
		return summary, nil
	}

	// Accept only if the optimized version is shorter or similar length
	// (within 120% of original) — don't accept bloated rewrites.
	if len(optimized) > len(summary)*120/100 {
		slog.Debug("optimized summary rejected: too long", "original_len", len(summary), "optimized_len", len(optimized))
		return summary, nil
	}

	return optimized, nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	// Find last valid rune boundary at or before maxLen to avoid breaking multi-byte UTF-8.
	for i := maxLen; i >= 0; i-- {
		if utf8.RuneStart(s[i]) {
			return s[:i] + "..."
		}
	}
	return "..."
}

const summarizePrompt = `You are a conversation summarizer. Produce a concise summary of the following conversation that preserves:
1. All decisions made and their rationale
2. Key facts, constraints, and requirements discussed
3. Current state of any ongoing work
4. Action items and next steps

Be factual and specific. Preserve exact names, paths, numbers, and technical details.
Do NOT add commentary. Output only the summary.`

const compressPrompt = `You are an aggressive conversation compressor. Produce the most compact summary possible that preserves ONLY:
1. Final decisions and outcomes (not the deliberation)
2. Critical facts and constraints that affect future actions
3. Current state and immediate next steps

Strip all redundancy, pleasantries, and intermediate reasoning. Be terse.
Output only the compressed summary.`

const optimizeSummaryPrompt = `You are a summary quality optimizer. You will receive a summary of a conversation and a sample of the original conversation.

Evaluate the summary against the original and produce an improved version that:
1. Fixes any factual errors or omissions found by comparing to the original
2. Removes redundant or vague statements
3. Ensures all domain-specific terms, file paths, and technical details are preserved exactly
4. Is as concise as possible without losing critical information

Output ONLY the improved summary text. No commentary, no explanation.`
