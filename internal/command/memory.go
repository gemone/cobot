package command

import (
	"context"

	"github.com/cobot-agent/cobot/pkg"
)

type memoryCmd struct{}

func (c *memoryCmd) Name() string   { return "memory" }
func (c *memoryCmd) Help() string  { return "manage session memory (/memory status)" }
func (c *memoryCmd) Execute(ctx context.Context, cmdCtx cobot.CommandContext) (*cobot.OutboundMessage, error) {
	args := cmdCtx.Text

	switch {
	case args == "" || args == "status":
		return &cobot.OutboundMessage{Text: "Session memory status: ok."}, nil
	default:
		return &cobot.OutboundMessage{Text: "Usage: /memory status"}, nil
	}
}
