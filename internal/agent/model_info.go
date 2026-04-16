package agent

import (
	"sort"
	"strings"
)

func ContextWindowForModel(model string, overrides map[string]int) int {
	if overrides != nil {
		if v, ok := overrides[model]; ok {
			return v
		}
	}
	if v, ok := builtinContextWindows[model]; ok {
		return v
	}
	for _, entry := range sortedPrefixes {
		if strings.HasPrefix(model, entry.prefix) {
			return entry.window
		}
	}
	return defaultContextWindow
}

const defaultContextWindow = 128_000

var builtinContextWindows = map[string]int{
	// OpenAI
	"gpt-4o":        128_000,
	"gpt-4o-mini":   128_000,
	"gpt-4-turbo":   128_000,
	"gpt-4":         8_192,
	"gpt-3.5-turbo": 16_385,
	"o1":            200_000,
	"o1-mini":       128_000,
	"o1-pro":        200_000,
	"o3":            200_000,
	"o3-mini":       200_000,
	"o4-mini":       200_000,

	// Anthropic
	"claude-3-5-sonnet": 200_000,
	"claude-3-5-haiku":  200_000,
	"claude-3-opus":     200_000,
	"claude-3-sonnet":   200_000,
	"claude-3-haiku":    200_000,
	"claude-4-sonnet":   200_000,
	"claude-4-opus":     200_000,
	"claude-opus-4":     200_000,
	"claude-sonnet-4":   200_000,

	// DeepSeek
	"deepseek-chat":     64_000,
	"deepseek-reasoner": 64_000,
}

type prefixEntry struct {
	prefix string
	window int
}

var sortedPrefixes = func() []prefixEntry {
	entries := make([]prefixEntry, 0, len(builtinContextWindows))
	for k, v := range builtinContextWindows {
		entries = append(entries, prefixEntry{k, v})
	}
	sort.Slice(entries, func(i, j int) bool {
		return len(entries[i].prefix) > len(entries[j].prefix)
	})
	return entries
}()
