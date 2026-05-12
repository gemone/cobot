package command

import (
	"context"
	"fmt"
	"strings"

	"github.com/cobot-agent/cobot/pkg"
)

type helpCmd struct {
	// cmds is populated by SetHelpData before the registry starts processing.
	cmds []cobot.Command
}

func (c *helpCmd) Name() string { return "help" }
func (c *helpCmd) Help() string { return "show this help text" }
func (c *helpCmd) Execute(ctx context.Context, cmdCtx cobot.CommandContext) (*cobot.OutboundMessage, error) {
	cmds := c.cmds
	if cmds == nil {
		// Fallback: try from Data.
		if data, ok := cmdCtx.Data.([]cobot.Command); ok {
			cmds = data
		}
	}
	if len(cmds) == 0 {
		return &cobot.OutboundMessage{Text: "No commands available."}, nil
	}

	var lines []string
	for _, cmd := range cmds {
		lines = append(lines, fmt.Sprintf("/%s — %s", cmd.Name(), cmd.Help()))
	}
	return &cobot.OutboundMessage{Text: "Available commands:\n" + strings.Join(lines, "\n")}, nil
}
