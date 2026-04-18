package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/cobot-agent/cobot/internal/llm/base"
	cobot "github.com/cobot-agent/cobot/pkg"
)

var _ cobot.Provider = (*Provider)(nil)
var _ cobot.ModelValidator = (*Provider)(nil)

const ProviderName = "anthropic"

type Provider struct {
	cfg    base.ProviderConfig
	client *http.Client
}

func NewProvider(apiKey, baseURL string, pc *cobot.ProviderConfig) *Provider {
	var timeout *time.Duration
	if pc != nil {
		timeout = pc.Timeout
	}
	return &Provider{
		cfg: base.ProviderConfig{
			Name:    ProviderName,
			APIKey:  apiKey,
			BaseURL: base.PrepareBaseURL(baseURL, "https://api.anthropic.com"),
		},
		client: base.NewHTTPClientWithTimeout(timeout),
	}
}

func (p *Provider) Name() string { return ProviderName }

var knownAnthropicPrefixes = []string{
	"claude-",
}

func (p *Provider) ValidateModel(_ context.Context, model string) error {
	for _, prefix := range knownAnthropicPrefixes {
		if strings.HasPrefix(model, prefix) {
			return nil
		}
	}
	return fmt.Errorf("model %q is not a recognized Anthropic model (expected claude-* prefix)", model)
}

func (p *Provider) Complete(ctx context.Context, req *cobot.ProviderRequest) (*cobot.ProviderResponse, error) {
	body := p.buildRequest(req, false)
	respBody, err := p.doRequest(ctx, body)
	if err != nil {
		return nil, err
	}
	defer respBody.Close()

	var resp messagesResponse
	if err := json.NewDecoder(respBody).Decode(&resp); err != nil {
		return nil, fmt.Errorf("anthropic: decode response: %w", err)
	}
	return p.toProviderResponse(&resp), nil
}

func (p *Provider) Stream(ctx context.Context, req *cobot.ProviderRequest) (<-chan cobot.ProviderChunk, error) {
	body := p.buildRequest(req, true)
	respBody, err := p.doRequest(ctx, body)
	if err != nil {
		return nil, err
	}

	ch := make(chan cobot.ProviderChunk, 64)
	go func() {
		defer close(ch)
		sse := base.NewSSEScannerWithContext(ctx, respBody, base.DefaultSSEIdleTimeout, ProviderName)
		defer sse.Close()
		p.readStream(sse, ch)
	}()
	return ch, nil
}

func (p *Provider) buildRequest(req *cobot.ProviderRequest, stream bool) messagesRequest {
	var system string
	var msgs []message
	for _, m := range req.Messages {
		if m.Role == cobot.RoleSystem {
			system = m.Content
			continue
		}

		switch {
		case m.ToolResult != nil:
			// Tool result message → tool_result content block
			content := m.ToolResult.Output
			isError := false
			if m.ToolResult.Error != "" {
				isError = true
				if content != "" {
					content = content + "\n[ERROR] " + m.ToolResult.Error
				} else {
					content = "[ERROR] " + m.ToolResult.Error
				}
			}
			blocks, _ := json.Marshal([]toolResultBlock{{
				Type:      "tool_result",
				ToolUseID: m.ToolResult.CallID,
				Content:   content,
				IsError:   isError,
			}})
			msgs = append(msgs, message{Role: "user", Content: blocks})

		case len(m.ToolCalls) > 0:
			// Assistant message with tool calls → text block (if any) + tool_use blocks
			var blocks []any
			if m.Content != "" {
				blocks = append(blocks, textBlock{Type: "text", Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				blocks = append(blocks, toolUseBlock{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Name,
					Input: tc.Arguments,
				})
			}
			content, _ := json.Marshal(blocks)
			msgs = append(msgs, message{Role: "assistant", Content: content})

		default:
			// Plain text message (user or assistant)
			content, _ := json.Marshal([]textBlock{{Type: "text", Text: m.Content}})
			msgs = append(msgs, message{Role: string(m.Role), Content: content})
		}
	}

	var tools []toolDef
	for _, t := range req.Tools {
		tools = append(tools, toolDef{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.Parameters,
		})
	}

	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = 4096
	}

	return messagesRequest{
		Model:     strings.TrimPrefix(req.Model, "anthropic:"),
		MaxTokens: maxTokens,
		Messages:  msgs,
		System:    system,
		Tools:     tools,
		Stream:    stream,
	}
}

func (p *Provider) doRequest(ctx context.Context, body messagesRequest) (io.ReadCloser, error) {
	headers := map[string]string{
		"x-api-key":         p.cfg.APIKey,
		"anthropic-version": "2023-06-01",
	}
	return base.DoRequest(p.client, p.cfg, ctx, "/v1/messages", body, headers)
}

func (p *Provider) toProviderResponse(resp *messagesResponse) *cobot.ProviderResponse {
	var sb strings.Builder
	for _, block := range resp.Content {
		if block.Type == "text" {
			sb.WriteString(block.Text)
		}
	}
	content := sb.String()
	stopReason := cobot.StopEndTurn
	if resp.StopReason == "max_tokens" {
		stopReason = cobot.StopMaxTokens
	}
	return &cobot.ProviderResponse{
		Content:    content,
		StopReason: stopReason,
		Usage: cobot.Usage{
			PromptTokens:     resp.Usage.InputTokens,
			CompletionTokens: resp.Usage.OutputTokens,
			TotalTokens:      resp.Usage.InputTokens + resp.Usage.OutputTokens,
			CacheReadTokens:  resp.Usage.CacheReadInputTokens,
			CacheWriteTokens: resp.Usage.CacheCreationInputTokens,
		},
	}
}

func (p *Provider) readStream(sse *base.SSEScanner, ch chan<- cobot.ProviderChunk) {
	pending := make(map[int]*base.PendingToolCall)
	var inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens int

	for {
		_, data, err := sse.Next()
		if err != nil {
			if err.Error() != "EOF" {
				ch <- cobot.ProviderChunk{
					Content: fmt.Sprintf("[stream error: %v]", err),
					Done:    true,
				}
			}
			return
		}
		if data == nil {
			// [DONE] received — shouldn't happen for Anthropic, but handle gracefully
			return
		}

		var evt streamEvent
		if err := json.Unmarshal(data, &evt); err != nil {
			continue
		}

		switch evt.Type {
		case "message_start":
			if evt.Message != nil {
				inputTokens = evt.Message.Usage.InputTokens
				cacheReadTokens = evt.Message.Usage.CacheReadInputTokens
				cacheWriteTokens = evt.Message.Usage.CacheCreationInputTokens
			}

		case "content_block_start":
			if evt.ContentBlock != nil && evt.ContentBlock.Type == "tool_use" {
				pending[evt.Index] = &base.PendingToolCall{
					ID:   evt.ContentBlock.ID,
					Name: evt.ContentBlock.Name,
				}
			}

		case "content_block_delta":
			if evt.Delta != nil {
				if evt.Delta.Text != "" {
					ch <- cobot.ProviderChunk{Content: evt.Delta.Text}
				}
				if evt.Delta.PartialJSON != "" {
					if ptc, ok := pending[evt.Index]; ok {
						ptc.Args.WriteString(evt.Delta.PartialJSON)
					}
				}
			}

		case "message_delta":
			if evt.MessageDelta != nil {
				outputTokens += evt.MessageDelta.Usage.OutputTokens
				if evt.MessageDelta.StopReason == "tool_use" {
					indices := slices.Sorted(maps.Keys(pending))
					for _, idx := range indices {
						ptc := pending[idx]
						ch <- cobot.ProviderChunk{
							ToolCall: &cobot.ToolCall{
								ID:        ptc.ID,
								Name:      ptc.Name,
								Arguments: json.RawMessage(ptc.Args.String()),
							},
						}
					}
				}
			}

		case "message_stop":
			ch <- cobot.ProviderChunk{Done: true, Usage: &cobot.Usage{
				PromptTokens:     inputTokens,
				CompletionTokens: outputTokens,
				TotalTokens:      inputTokens + outputTokens,
				CacheReadTokens:  cacheReadTokens,
				CacheWriteTokens: cacheWriteTokens,
			}}
			return
		}
	}
}
