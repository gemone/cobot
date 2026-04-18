package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/cobot-agent/cobot/internal/debuglog"
	"github.com/cobot-agent/cobot/internal/memory"
	cobot "github.com/cobot-agent/cobot/pkg"
)

func (a *Agent) buildRequest(ctx context.Context) *cobot.ProviderRequest {
	return &cobot.ProviderRequest{
		Model:    a.config.Model,
		Messages: a.buildMessages(ctx),
		Tools:    a.tools.ToolDefs(),
	}
}

func (a *Agent) executeToolsAndCollect(ctx context.Context, toolCalls []cobot.ToolCall) []*cobot.ToolResult {
	results := a.tools.ExecuteParallel(ctx, toolCalls)
	for _, tr := range results {
		if tr.Error != "" {
			slog.Error("tool error", "call_id", tr.CallID, "err", tr.Error)
		} else {
			slog.Debug("tool completed", "call_id", tr.CallID, "result_bytes", len(tr.Output))
		}
		a.sessionMgr.AddMessage(cobot.Message{
			Role:       cobot.RoleTool,
			ToolResult: tr,
		}, a.config.Model)
	}
	return results
}

// runLoop executes the core agent loop shared by Prompt and Stream.
// The executeTurn callback handles mode-specific provider interaction
// (Complete vs Stream) and returns stop=true when the loop should
// terminate (final response received with no tool calls).
func (a *Agent) runLoop(ctx context.Context, prompt, debugLabel string, executeTurn func(ctx context.Context, req *cobot.ProviderRequest, turn int) (stop bool, err error)) error {
	sm := a.sessionMgr
	ctx = debuglog.WithSessionID(ctx, sm.SessionID())
	slog.Debug("session", "event", debugLabel, "prompt", prompt)
	sm.AddMessage(cobot.Message{Role: cobot.RoleUser, Content: prompt}, a.config.Model)

	// Store initial user message as STM context.
	a.storeTurnSTM(ctx, prompt, "", nil)

	for turn := 0; turn < a.config.MaxTurns; turn++ {
		req := a.buildRequest(ctx)
		slog.Debug("agent turn", "event", debugLabel, "turn", turn, "model", req.Model, "msgs", len(req.Messages), "tools", len(req.Tools))

		stop, err := executeTurn(ctx, req, turn)
		if err != nil {
			return err
		}

		sm.turnCount.Add(1)
		a.checkAndCompress(ctx)

		if tc := sm.turnCount.Load(); sm.stmPromoteInterval > 0 && int(tc)%sm.stmPromoteInterval == 0 {
			a.promoteSTMBackground(ctx)
		}

		if stop {
			return nil
		}
	}

	return ErrMaxTurnsExceeded
}

// storeTurnSTM extracts and stores short-term memory items from the current
// conversation turn. It is a no-op when the memory store does not implement
// ShortTermMemory.
func (a *Agent) storeTurnSTM(ctx context.Context, userMsg, assistantMsg string, toolResults []string) {
	sm := a.sessionMgr
	if sm.memoryStore == nil {
		return
	}
	stm, ok := sm.memoryStore.(cobot.ShortTermMemory)
	if !ok {
		return
	}
	items := memory.ExtractSTM(userMsg, assistantMsg, toolResults)
	for _, item := range items {
		if _, err := stm.StoreShortTerm(ctx, sm.sessionID, item.Content, item.Category); err != nil {
			slog.Debug("stm store failed", "err", err)
		}
	}
}

func (a *Agent) Prompt(ctx context.Context, message string) (*cobot.ProviderResponse, error) {
	if a.provider == nil {
		return nil, ErrProviderNotConfigured
	}
	ctx = a.deriveCtx(ctx)

	sm := a.sessionMgr
	var result *cobot.ProviderResponse
	err := a.runLoop(ctx, message, "prompt", func(ctx context.Context, req *cobot.ProviderRequest, turn int) (bool, error) {
		resp, err := a.provider.Complete(ctx, req)
		if err != nil {
			slog.Error("provider error", "err", err)
			return false, fmt.Errorf("provider error: %w", err)
		}

		slog.Debug("agent response", "turn", turn, "content_len", len(resp.Content), "tool_calls", len(resp.ToolCalls), "stop", resp.StopReason)
		usage := estimateTurnUsage(resp.Usage, req.Messages, resp.Content, resp.ToolCalls)
		sm.usageTracker.Add(usage)
		sm.PersistUsage()
		sm.AddMessage(cobot.Message{
			Role:      cobot.RoleAssistant,
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		}, a.config.Model)

		if len(resp.ToolCalls) == 0 {
			// Final response — store STM for this turn.
			a.storeTurnSTM(ctx, "", resp.Content, nil)
			result = resp
			return true, nil
		}

		toolResults := a.executeToolsAndCollect(ctx, resp.ToolCalls)
		// Collect tool result outputs for STM.
		var resultOutputs []string
		for _, tr := range toolResults {
			if tr.Error != "" {
				resultOutputs = append(resultOutputs, "Error: "+tr.Error)
			} else {
				resultOutputs = append(resultOutputs, tr.Output)
			}
		}
		a.storeTurnSTM(ctx, "", resp.Content, resultOutputs)
		return false, nil
	})

	return result, err
}

func (a *Agent) Stream(ctx context.Context, message string) (<-chan cobot.Event, error) {
	if a.provider == nil {
		return nil, ErrProviderNotConfigured
	}
	ctx = a.deriveCtx(ctx)

	ch := make(chan cobot.Event, 1024)

	// sendEvent sends an event to the channel, respecting context cancellation.
	// Returns false if context was cancelled (caller should abort).
	sendEvent := func(evt cobot.Event) bool {
		select {
		case ch <- evt:
			return true
		case <-ctx.Done():
			return false
		}
	}

	sm := a.sessionMgr
	a.streamWg.Add(1)
	go func() {
		defer a.streamWg.Done()
		defer close(ch)
		a.streamMu.Lock()
		defer a.streamMu.Unlock()

		err := a.runLoop(ctx, message, "stream", func(ctx context.Context, req *cobot.ProviderRequest, turn int) (bool, error) {
			streamCh, err := a.provider.Stream(ctx, req)
			if err != nil {
				slog.Error("provider stream error", "err", err)
				return false, fmt.Errorf("provider stream error: %w", err)
			}

			var content string
			var toolCalls []cobot.ToolCall
			var turnUsage cobot.Usage
			for chunk := range streamCh {
				select {
				case <-ctx.Done():
					return false, ctx.Err()
				default:
				}
				if chunk.Content != "" {
					content += chunk.Content
					if !sendEvent(cobot.Event{Type: cobot.EventText, Content: chunk.Content}) {
						return false, ctx.Err()
					}
				}
				if chunk.ToolCall != nil {
					toolCalls = append(toolCalls, *chunk.ToolCall)
					if !sendEvent(cobot.Event{Type: cobot.EventToolCall, ToolCall: chunk.ToolCall}) {
						return false, ctx.Err()
					}
				}
				if chunk.Usage != nil {
					turnUsage.PromptTokens += chunk.Usage.PromptTokens
					turnUsage.CompletionTokens += chunk.Usage.CompletionTokens
					turnUsage.TotalTokens += chunk.Usage.TotalTokens
					turnUsage.ReasoningTokens += chunk.Usage.ReasoningTokens
					turnUsage.CacheReadTokens += chunk.Usage.CacheReadTokens
					turnUsage.CacheWriteTokens += chunk.Usage.CacheWriteTokens
				}
				if chunk.Done && len(toolCalls) == 0 {
					slog.Debug("stream done", "turn", turn, "content_len", len(content))
					sm.AddMessage(cobot.Message{Role: cobot.RoleAssistant, Content: content}, a.config.Model)
					turnUsage = estimateTurnUsage(turnUsage, req.Messages, content, nil)
					sm.usageTracker.Add(turnUsage)
					sm.PersistUsage()
					// Final response — store STM for this turn.
					a.storeTurnSTM(ctx, "", content, nil)
					sendEvent(cobot.Event{Type: cobot.EventDone, Done: true, Usage: &turnUsage})
					return true, nil
				}
			}

			if len(toolCalls) > 0 {
				slog.Debug("stream tool calls", "turn", turn, "count", len(toolCalls))
				sm.AddMessage(cobot.Message{Role: cobot.RoleAssistant, Content: content, ToolCalls: toolCalls}, a.config.Model)
				turnUsage = estimateTurnUsage(turnUsage, req.Messages, content, toolCalls)
				sm.usageTracker.Add(turnUsage)
				sm.PersistUsage()

				// Split tool calls into streaming vs normal tools.
				var normalCalls []cobot.ToolCall
				var streamingCalls []cobot.ToolCall
				for _, tc := range toolCalls {
					if a.tools.IsStreamingTool(tc.Name) {
						streamingCalls = append(streamingCalls, tc)
					} else {
						normalCalls = append(normalCalls, tc)
					}
				}

				// Execute normal tools in parallel (blocking).
				if len(normalCalls) > 0 {
					results := a.tools.ExecuteParallel(ctx, normalCalls)
					for _, tr := range results {
						evt := cobot.Event{Type: cobot.EventToolResult, Content: tr.Output}
						if tr.Error != "" {
							evt.Content = tr.Error
							evt.Error = tr.Error
							slog.Error("tool error", "call_id", tr.CallID, "err", tr.Error)
						}
						if !sendEvent(evt) {
							return false, ctx.Err()
						}
						sm.AddMessage(cobot.Message{Role: cobot.RoleTool, ToolResult: tr}, a.config.Model)
					}
				}

				// Execute streaming tools sequentially, forwarding events.
				for _, sc := range streamingCalls {
					if !sendEvent(cobot.Event{Type: cobot.EventToolStart, Content: sc.Name, ToolCall: &sc}) {
						return false, ctx.Err()
					}
					tool, err := a.tools.Get(sc.Name)
					if err != nil {
						tr := &cobot.ToolResult{CallID: sc.ID, Error: err.Error()}
						if !sendEvent(cobot.Event{Type: cobot.EventToolResult, Content: tr.Error, Error: tr.Error}) {
							return false, ctx.Err()
						}
						sm.AddMessage(cobot.Message{Role: cobot.RoleTool, ToolResult: tr}, a.config.Model)
						continue
					}
					st, ok := tool.(cobot.StreamingTool)
					if !ok {
						output, execErr := tool.Execute(ctx, sc.Arguments)
						tr := &cobot.ToolResult{CallID: sc.ID}
						if execErr != nil {
							tr.Error = execErr.Error()
						} else {
							tr.Output = output
						}
						resultEvt := cobot.Event{Type: cobot.EventToolResult, Content: tr.Output}
						if tr.Error != "" {
							resultEvt.Content = tr.Error
							resultEvt.Error = tr.Error
						}
						if !sendEvent(resultEvt) {
							return false, ctx.Err()
						}
						sm.AddMessage(cobot.Message{Role: cobot.RoleTool, ToolResult: tr}, a.config.Model)
						continue
					}
					output, execErr := st.ExecuteStream(ctx, sc.Arguments, ch)
					tr := &cobot.ToolResult{CallID: sc.ID}
					if execErr != nil {
						tr.Error = execErr.Error()
						slog.Error("streaming tool error", "call_id", sc.ID, "err", execErr)
					} else {
						tr.Output = output
					}
					resultEvt := cobot.Event{Type: cobot.EventToolResult, Content: tr.Output}
					if tr.Error != "" {
						resultEvt.Content = tr.Error
						resultEvt.Error = tr.Error
					}
					if !sendEvent(resultEvt) {
						return false, ctx.Err()
					}
					sm.AddMessage(cobot.Message{Role: cobot.RoleTool, ToolResult: tr}, a.config.Model)
				}
			}

			// Store STM for assistant response + tool results.
			a.storeTurnSTM(ctx, "", content, nil)

			return false, nil
		})

		if err != nil {
			if errors.Is(err, ErrMaxTurnsExceeded) {
				slog.Debug("max turns exceeded", "turns", a.config.MaxTurns)
			}
			sendEvent(cobot.Event{Type: cobot.EventError, Error: err.Error()})
		}
	}()

	return ch, nil
}
