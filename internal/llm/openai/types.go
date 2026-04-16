package openai

import (
	"encoding/json"

	cobot "github.com/cobot-agent/cobot/pkg"
)

type chatMessage struct {
	Role         string         `json:"role"`
	Content      *string        `json:"content"`
	ToolCalls    []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID   string         `json:"tool_call_id,omitempty"`
	Name         string         `json:"name,omitempty"`
	FunctionCall *chatFuncCall  `json:"function_call,omitempty"`
}

type chatToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function chatFuncCall `json:"function"`
}

type chatFuncCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type chatTool struct {
	Type     string       `json:"type"`
	Function chatToolFunc `json:"function"`
}

type chatToolFunc struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Tools       []chatTool    `json:"tools,omitempty"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Temperature float64       `json:"temperature,omitempty"`
	Stream      bool          `json:"stream,omitempty"`
}

type chatResponse struct {
	ID      string       `json:"id"`
	Choices []chatChoice `json:"choices"`
	Usage   chatUsage    `json:"usage"`
}

type chatChoice struct {
	Index        int         `json:"index"`
	Message      chatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

type chatUsage struct {
	PromptTokens            int                      `json:"prompt_tokens"`
	CompletionTokens        int                      `json:"completion_tokens"`
	TotalTokens             int                      `json:"total_tokens"`
	PromptTokensDetails     *promptTokensDetails     `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails *completionTokensDetails `json:"completion_tokens_details,omitempty"`
}

type promptTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

type completionTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

type streamChunk struct {
	ID      string         `json:"id"`
	Choices []streamChoice `json:"choices"`
	Usage   *chatUsage     `json:"usage,omitempty"`
}

type streamChoice struct {
	Index        int         `json:"index"`
	Delta        streamDelta `json:"delta"`
	FinishReason *string     `json:"finish_reason"`
}

type streamDelta struct {
	Role      string          `json:"role,omitempty"`
	Content   string          `json:"content,omitempty"`
	ToolCalls []toolCallDelta `json:"tool_calls,omitempty"`
}

type toolCallDelta struct {
	Index    int            `json:"index"`
	ID       string         `json:"id,omitempty"`
	Type     string         `json:"type,omitempty"`
	Function *funcCallDelta `json:"function,omitempty"`
}

type funcCallDelta struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

func toProviderResponse(resp *chatResponse) *cobot.ProviderResponse {
	if len(resp.Choices) == 0 {
		return &cobot.ProviderResponse{}
	}

	choice := resp.Choices[0]
	u := cobot.Usage{
		PromptTokens:     resp.Usage.PromptTokens,
		CompletionTokens: resp.Usage.CompletionTokens,
		TotalTokens:      resp.Usage.TotalTokens,
	}
	if resp.Usage.PromptTokensDetails != nil {
		u.CacheReadTokens = resp.Usage.PromptTokensDetails.CachedTokens
	}
	if resp.Usage.CompletionTokensDetails != nil {
		u.ReasoningTokens = resp.Usage.CompletionTokensDetails.ReasoningTokens
	}
	result := &cobot.ProviderResponse{
		Content: derefString(choice.Message.Content),
		Usage:   u,
	}

	switch choice.FinishReason {
	case "stop":
		result.StopReason = cobot.StopEndTurn
	case "length":
		result.StopReason = cobot.StopMaxTokens
	default:
		result.StopReason = cobot.StopEndTurn
	}

	for _, tc := range choice.Message.ToolCalls {
		result.ToolCalls = append(result.ToolCalls, cobot.ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: json.RawMessage(tc.Function.Arguments),
		})
	}

	return result
}

func fromProviderMessages(msgs []cobot.Message) []chatMessage {
	result := make([]chatMessage, 0, len(msgs))
	for _, m := range msgs {
		cm := chatMessage{
			Role:    string(m.Role),
			Content: stringPtr(m.Content),
		}

		for _, tc := range m.ToolCalls {
			cm.ToolCalls = append(cm.ToolCalls, chatToolCall{
				ID:   tc.ID,
				Type: "function",
				Function: chatFuncCall{
					Name:      tc.Name,
					Arguments: string(tc.Arguments),
				},
			})
		}

		// Assistant messages with tool_calls must have nil content, not empty string.
		// OpenAI API rejects "content": "" when tool_calls are present.
		if len(cm.ToolCalls) > 0 {
			cm.Content = nil
		}

		if m.ToolResult != nil {
			cm.ToolCallID = m.ToolResult.CallID
			content := m.ToolResult.Output
			if m.ToolResult.Error != "" {
				if content != "" {
					content = content + "\n[ERROR] " + m.ToolResult.Error
				} else {
					content = "[ERROR] " + m.ToolResult.Error
				}
			}
			if content == "" {
				// OpenAI requires non-empty content for tool result messages;
				// use a single space as a safe placeholder.
				space := " "
				cm.Content = &space
			} else {
				cm.Content = &content
			}
		}

		result = append(result, cm)
	}
	return result
}

func stringPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func fromProviderTools(tools []cobot.ToolDef) []chatTool {
	result := make([]chatTool, 0, len(tools))
	for _, t := range tools {
		result = append(result, chatTool{
			Type: "function",
			Function: chatToolFunc{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
	}
	return result
}
