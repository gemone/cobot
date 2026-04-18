package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"time"

	"github.com/cobot-agent/cobot/internal/llm/base"
	cobot "github.com/cobot-agent/cobot/pkg"
)

var _ cobot.Provider = (*Provider)(nil)
var _ cobot.ModelValidator = (*Provider)(nil)

const ProviderName = "openai"

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
			BaseURL: base.PrepareBaseURL(baseURL, "https://api.openai.com/v1"),
		},
		client: base.NewHTTPClientWithTimeout(timeout),
	}
}

func (p *Provider) Name() string { return ProviderName }

func (p *Provider) ValidateModel(ctx context.Context, model string) error {
	url := p.cfg.BaseURL + "/models/" + model
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("openai: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("openai: validate model: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("model %q not found", model)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("openai: validate model: HTTP %d", resp.StatusCode)
	}
	return nil
}

func (p *Provider) Complete(ctx context.Context, req *cobot.ProviderRequest) (*cobot.ProviderResponse, error) {
	body := chatRequest{
		Model:       req.Model,
		Messages:    fromProviderMessages(req.Messages),
		Tools:       fromProviderTools(req.Tools),
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		Stream:      false,
	}

	headers := map[string]string{"Authorization": "Bearer " + p.cfg.APIKey}
	respBody, err := base.DoRequest(p.client, p.cfg, ctx, "/chat/completions", body, headers)
	if err != nil {
		return nil, err
	}
	defer respBody.Close()

	var resp chatResponse
	if err := json.NewDecoder(respBody).Decode(&resp); err != nil {
		return nil, fmt.Errorf("openai: decode response: %w", err)
	}

	return toProviderResponse(&resp), nil
}

func (p *Provider) Stream(ctx context.Context, req *cobot.ProviderRequest) (<-chan cobot.ProviderChunk, error) {
	body := chatRequest{
		Model:       req.Model,
		Messages:    fromProviderMessages(req.Messages),
		Tools:       fromProviderTools(req.Tools),
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		Stream:      true,
	}

	headers := map[string]string{"Authorization": "Bearer " + p.cfg.APIKey}
	respBody, err := base.DoRequest(p.client, p.cfg, ctx, "/chat/completions", body, headers)
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

func (p *Provider) readStream(sse *base.SSEScanner, ch chan<- cobot.ProviderChunk) {
	pending := make(map[int]*base.PendingToolCall)

	var lastUsage *cobot.Usage
	for {
		_, data, err := sse.Next()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				ch <- cobot.ProviderChunk{
					Content: fmt.Sprintf("[stream error: %v]", err),
					Done:    true,
				}
			}
			return
		}
		if data == nil {
			// [DONE] received
			if lastUsage != nil {
				ch <- cobot.ProviderChunk{Done: true, Usage: lastUsage}
			}
			return
		}

		var chunk streamChunk
		if err := json.Unmarshal(data, &chunk); err != nil {
			ch <- cobot.ProviderChunk{
				Content: fmt.Sprintf("[stream error: malformed data: %v]", err),
			}
			continue
		}

		// Capture usage if the provider includes it in the stream response.
		if chunk.Usage != nil {
			lastUsage = &cobot.Usage{
				PromptTokens:     chunk.Usage.PromptTokens,
				CompletionTokens: chunk.Usage.CompletionTokens,
				TotalTokens:      chunk.Usage.TotalTokens,
			}
			if chunk.Usage.PromptTokensDetails != nil {
				lastUsage.CacheReadTokens = chunk.Usage.PromptTokensDetails.CachedTokens
			}
			if chunk.Usage.CompletionTokensDetails != nil {
				lastUsage.ReasoningTokens = chunk.Usage.CompletionTokensDetails.ReasoningTokens
			}
		}

		for _, choice := range chunk.Choices {
			for _, tc := range choice.Delta.ToolCalls {
				ptc, exists := pending[tc.Index]
				if !exists {
					ptc = &base.PendingToolCall{}
					pending[tc.Index] = ptc
				}
				if tc.ID != "" {
					ptc.ID = tc.ID
				}
				if tc.Function != nil {
					if tc.Function.Name != "" {
						ptc.Name = tc.Function.Name
					}
					ptc.Args.WriteString(tc.Function.Arguments)
				}
			}

			pc := cobot.ProviderChunk{
				Content: choice.Delta.Content,
			}

			if choice.FinishReason != nil {
				if *choice.FinishReason == "tool_calls" {
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
				pc.Done = true
			}

			ch <- pc
		}
	}
}
