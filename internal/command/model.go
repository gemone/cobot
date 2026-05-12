package command

import (
	"context"
	"fmt"
	"strings"

	"github.com/cobot-agent/cobot/internal/agent"
	"github.com/cobot-agent/cobot/pkg"
)

type modelCmd struct{}

func (c *modelCmd) Name() string { return "model" }
func (c *modelCmd) Help() string { return "show or switch the current model (/model <provider:model>)" }
func (c *modelCmd) Execute(ctx context.Context, cmdCtx cobot.CommandContext) (*cobot.OutboundMessage, error) {
	args := strings.TrimSpace(cmdCtx.Text)

	switch {
	case args == "" || args == "show":
		return c.showCurrent(cmdCtx)
	case args == "list":
		return c.listModels()
	default:
		return c.switchModel(cmdCtx)
	}
}

func (c *modelCmd) showCurrent(cmdCtx cobot.CommandContext) (*cobot.OutboundMessage, error) {
	a, ok := cmdCtx.Agent.(*agent.Agent)
	if !ok || a == nil {
		return &cobot.OutboundMessage{Text: "No agent context available."}, nil
	}
	provider := a.Provider()
	if provider == nil {
		return &cobot.OutboundMessage{Text: "No provider configured."}, nil
	}
	return &cobot.OutboundMessage{Text: fmt.Sprintf("Current provider: %T", provider)}, nil
}

func (c *modelCmd) listModels() (*cobot.OutboundMessage, error) {
	return &cobot.OutboundMessage{Text: "Usage: /model <provider:model>\nExample: /model openai:gpt-4o\nKnown providers: openai, anthropic"}, nil
}

func (c *modelCmd) switchModel(cmdCtx cobot.CommandContext) (*cobot.OutboundMessage, error) {
	newModel := strings.TrimSpace(cmdCtx.Text)
	if newModel == "" {
		return &cobot.OutboundMessage{Text: "Usage: /model <provider:model>"}, nil
	}
	return &cobot.OutboundMessage{Text: fmt.Sprintf("Model switch to %q requested (not yet implemented).", newModel)}, nil
}
