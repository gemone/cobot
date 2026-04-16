package agent

import (
	"unicode"
	"unicode/utf8"

	cobot "github.com/cobot-agent/cobot/pkg"
)

func estimateTokens(text string) int {
	if text == "" {
		return 0
	}

	var ascii, cjk int
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		if r == utf8.RuneError && size <= 1 {
			ascii++
			i++
			continue
		}
		if unicode.Is(unicode.Han, r) ||
			unicode.Is(unicode.Hangul, r) ||
			unicode.Is(unicode.Katakana, r) ||
			unicode.Is(unicode.Hiragana, r) {
			cjk++
		} else {
			ascii++
		}
		i += size
	}

	return ascii/4 + cjk + 3
}

func estimateMessagesUsage(messages []cobot.Message) cobot.Usage {
	var prompt, completion int
	for _, m := range messages {
		tokens := estimateTokens(m.Content)
		for _, tc := range m.ToolCalls {
			tokens += estimateTokens(tc.Name) + estimateTokens(string(tc.Arguments))
		}
		if m.ToolResult != nil {
			tokens += estimateTokens(m.ToolResult.Output) + estimateTokens(m.ToolResult.Error)
		}

		if m.Role == cobot.RoleAssistant {
			completion += tokens
		} else {
			prompt += tokens
		}
	}
	return cobot.Usage{
		PromptTokens:     prompt,
		CompletionTokens: completion,
		TotalTokens:      prompt + completion,
	}
}
