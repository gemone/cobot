package command

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cobot-agent/cobot/internal/cron"
	"github.com/cobot-agent/cobot/internal/requestctx"
	"github.com/cobot-agent/cobot/pkg"
)

type cronCmd struct{}

func (c *cronCmd) Name() string { return "cron" }
func (c *cronCmd) Help() string {
	return "manage scheduled jobs (/cron list|pause <read_id>|resume <read_id>|bind [job_id]|runs <job_id>)"
}
func (c *cronCmd) Execute(ctx context.Context, cmdCtx cobot.CommandContext) (*cobot.OutboundMessage, error) {
	args := strings.TrimSpace(cmdCtx.Text)
	parts := strings.Fields(args)

	if len(parts) == 0 || parts[0] == "list" {
		return c.list(ctx, cmdCtx)
	}
	switch parts[0] {
	case "pause":
		if len(parts) < 2 {
			return &cobot.OutboundMessage{Text: "Usage: /cron pause <read_id>"}, nil
		}
		return c.pause(ctx, cmdCtx, parts[1])
	case "resume":
		if len(parts) < 2 {
			return &cobot.OutboundMessage{Text: "Usage: /cron resume <read_id>"}, nil
		}
		return c.resume(ctx, cmdCtx, parts[1])
	case "runs":
		if len(parts) < 2 {
			return &cobot.OutboundMessage{Text: "Usage: /cron runs <read_id>"}, nil
		}
		return c.runs(ctx, cmdCtx, parts[1])
	case "bind":
		selector := ""
		if len(parts) >= 2 {
			selector = parts[1]
		}
		return c.bind(ctx, cmdCtx, selector)
	default:
		return &cobot.OutboundMessage{Text: "Usage: /cron list | /cron pause <read_id> | /cron resume <read_id> | /cron bind [job_id] | /cron runs <job_id>"}, nil
	}
}

func (c *cronCmd) list(ctx context.Context, cmdCtx cobot.CommandContext) (*cobot.OutboundMessage, error) {
	scheduler, ok := cmdCtx.Data.(*cron.Scheduler)
	if !ok || scheduler == nil {
		return &cobot.OutboundMessage{Text: "Cron scheduler not available."}, nil
	}
	jobs, err := scheduler.ListJobs()
	if err != nil {
		return &cobot.OutboundMessage{Text: fmt.Sprintf("Failed to list jobs: %v", err)}, nil
	}
	if len(jobs) == 0 {
		return &cobot.OutboundMessage{Text: "No cron jobs configured."}, nil
	}
	var lines []string
	for _, j := range jobs {
		status := j.Status
		if j.NextRun != nil {
			status = fmt.Sprintf("%s (next: %s)", j.Status, j.NextRun.Format(time.RFC3339))
		}
		delivery := ""
		if j.Delivery.ChannelID == "" || j.Delivery.ChatID == "" {
			delivery = fmt.Sprintf(" — delivery target missing (use /cron bind %s, or /cron bind if it is the only unbound job)", j.ID)
		}
		lines = append(lines, fmt.Sprintf("- [%s] %s %s — %s%s", j.ID, j.Name, j.Schedule, status, delivery))
	}
	return &cobot.OutboundMessage{Text: "Cron jobs:\n" + strings.Join(lines, "\n")}, nil
}

func (c *cronCmd) pause(ctx context.Context, cmdCtx cobot.CommandContext, readID string) (*cobot.OutboundMessage, error) {
	scheduler, ok := cmdCtx.Data.(*cron.Scheduler)
	if !ok || scheduler == nil {
		return &cobot.OutboundMessage{Text: "Cron scheduler not available."}, nil
	}
	if err := scheduler.PauseJob(readID); err != nil {
		return &cobot.OutboundMessage{Text: fmt.Sprintf("Failed to pause job: %v", err)}, nil
	}
	return &cobot.OutboundMessage{Text: fmt.Sprintf("Job %q paused.", readID)}, nil
}

func (c *cronCmd) resume(ctx context.Context, cmdCtx cobot.CommandContext, readID string) (*cobot.OutboundMessage, error) {
	scheduler, ok := cmdCtx.Data.(*cron.Scheduler)
	if !ok || scheduler == nil {
		return &cobot.OutboundMessage{Text: "Cron scheduler not available."}, nil
	}
	if err := scheduler.ResumeJob(readID); err != nil {
		return &cobot.OutboundMessage{Text: fmt.Sprintf("Failed to resume job: %v", err)}, nil
	}
	return &cobot.OutboundMessage{Text: fmt.Sprintf("Job %q resumed.", readID)}, nil
}

func (c *cronCmd) bind(ctx context.Context, cmdCtx cobot.CommandContext, selector string) (*cobot.OutboundMessage, error) {
	scheduler, ok := cmdCtx.Data.(*cron.Scheduler)
	if !ok || scheduler == nil {
		return &cobot.OutboundMessage{Text: "Cron scheduler not available."}, nil
	}
	target, ok := requestctx.MessageTargetFromContext(ctx)
	if !ok || target.ChannelID == "" || target.ChatID == "" {
		return &cobot.OutboundMessage{Text: "Current conversation target is unavailable; cannot bind job."}, nil
	}
	jobID, err := scheduler.ResolveBindJobID(strings.TrimSpace(selector))
	if err != nil {
		return &cobot.OutboundMessage{Text: fmt.Sprintf("Failed to bind job: %v", err)}, nil
	}
	if err := scheduler.BindJobTargetByID(jobID, cron.DeliveryTarget{
		ChannelID:        target.ChannelID,
		ChatID:           target.ChatID,
		ChatType:         target.ChatType,
		ReceiveIDType:    target.ReceiveIDType,
		ReplyToMessageID: target.ReplyToMessageID,
	}); err != nil {
		return &cobot.OutboundMessage{Text: fmt.Sprintf("Failed to bind job: %v", err)}, nil
	}
	return &cobot.OutboundMessage{Text: fmt.Sprintf("Job %q bound to current conversation.", jobID)}, nil
}

func (c *cronCmd) runs(ctx context.Context, cmdCtx cobot.CommandContext, readID string) (*cobot.OutboundMessage, error) {
	return &cobot.OutboundMessage{Text: fmt.Sprintf("Run history for %q not yet implemented.", readID)}, nil
}
