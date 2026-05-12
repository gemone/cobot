package command

import (
	"context"
	"fmt"
	"strings"

	"github.com/cobot-agent/cobot/internal/agent"
	"github.com/cobot-agent/cobot/pkg"
)

type skillCmd struct{}

func (c *skillCmd) Name() string { return "skill" }
func (c *skillCmd) Help() string { return "manage skills (/skill list|view <name>|reload)" }
func (c *skillCmd) Execute(ctx context.Context, cmdCtx cobot.CommandContext) (*cobot.OutboundMessage, error) {
	args := strings.TrimSpace(cmdCtx.Text)
	parts := strings.Fields(args)

	switch {
	case len(parts) == 0 || parts[0] == "list":
		return c.list(ctx, cmdCtx)
	case parts[0] == "view" && len(parts) >= 2:
		return c.view(ctx, cmdCtx, strings.Join(parts[1:], " "))
	case parts[0] == "reload":
		return c.reload(ctx, cmdCtx)
	default:
		return &cobot.OutboundMessage{Text: "Usage: /skill list | /skill view <name> | /skill reload"}, nil
	}
}

func (c *skillCmd) list(ctx context.Context, cmdCtx cobot.CommandContext) (*cobot.OutboundMessage, error) {
	a, ok := cmdCtx.Agent.(*agent.Agent)
	if !ok || a == nil || a.SkillManager() == nil {
		return &cobot.OutboundMessage{Text: "SkillManager not available."}, nil
	}
	skills := a.SkillManager().List(ctx, "")
	if len(skills) == 0 {
		return &cobot.OutboundMessage{Text: "No skills installed."}, nil
	}
	var lines []string
	for _, s := range skills {
		lines = append(lines, fmt.Sprintf("- %s", s.Name))
	}
	return &cobot.OutboundMessage{Text: "Skills:\n" + strings.Join(lines, "\n")}, nil
}

func (c *skillCmd) view(ctx context.Context, cmdCtx cobot.CommandContext, name string) (*cobot.OutboundMessage, error) {
	a, ok := cmdCtx.Agent.(*agent.Agent)
	if !ok || a == nil || a.SkillManager() == nil {
		return &cobot.OutboundMessage{Text: "SkillManager not available."}, nil
	}
	desc, err := a.SkillManager().View(ctx, name)
	if err != nil {
		return &cobot.OutboundMessage{Text: fmt.Sprintf("Skill %q not found.", name)}, nil
	}
	return &cobot.OutboundMessage{Text: desc}, nil
}

func (c *skillCmd) reload(ctx context.Context, cmdCtx cobot.CommandContext) (*cobot.OutboundMessage, error) {
	a, ok := cmdCtx.Agent.(*agent.Agent)
	if !ok || a == nil || a.SkillManager() == nil {
		return &cobot.OutboundMessage{Text: "SkillManager not available."}, nil
	}
	_ = a.SkillManager().Reload(ctx) // error ignored; reload is best-effort
	return &cobot.OutboundMessage{Text: "Skills reloaded."}, nil
}
