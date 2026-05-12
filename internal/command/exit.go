package command

import (
	"context"

	"github.com/cobot-agent/cobot/pkg"
)

type exitCmd struct{}

func (c *exitCmd) Name() string { return "exit" }
func (c *exitCmd) Help() string { return "end the current conversation" }
func (c *exitCmd) Execute(ctx context.Context, cmdCtx cobot.CommandContext) (*cobot.OutboundMessage, error) {
	return &cobot.OutboundMessage{Text: "Goodbye!"}, nil
}

// stopCmd is an alias for exitCmd.
type stopCmd struct{}

func (c *stopCmd) Name() string { return "stop" }
func (c *stopCmd) Help() string { return "same as /exit" }
func (c *stopCmd) Execute(ctx context.Context, cmdCtx cobot.CommandContext) (*cobot.OutboundMessage, error) {
	return &cobot.OutboundMessage{Text: "Goodbye!"}, nil
}
