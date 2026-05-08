package tools

import (
	"context"
	"encoding/json"
	"testing"

	brokersqlite "github.com/cobot-agent/cobot/internal/broker"
	"github.com/cobot-agent/cobot/internal/cron"
	"github.com/cobot-agent/cobot/internal/requestctx"
)

func TestCronToolCreateCapturesRequestScopedTarget(t *testing.T) {
	dir := t.TempDir()
	br, err := brokersqlite.NewSQLiteBroker(dir + "/broker.db")
	if err != nil {
		t.Fatalf("new broker: %v", err)
	}
	defer br.Close()

	store := cron.NewStore(dir + "/cron")
	runStore := cron.NewRunStore(dir + "/runs")
	scheduler := cron.NewScheduler(store, func(_ context.Context, _, _, _ string) (string, error) { return "", nil }, runStore, br, nil)
	tool := NewCronTool(scheduler)

	ctx := requestctx.WithMessageTarget(context.Background(), requestctx.MessageTarget{
		ChannelID:        "feishu:test",
		ChatID:           "oc_chat",
		ChatType:         "group",
		ReplyToMessageID: "om_msg",
	})

	args, err := json.Marshal(map[string]any{
		"action":   "create",
		"schedule": "0 9 * * *",
		"prompt":   "say hi",
		"name":     "daily",
	})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}

	if _, err := tool.Execute(ctx, args); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	jobs, err := scheduler.ListJobs()
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}
	job := jobs[0]
	if job.Delivery.ChannelID != "feishu:test" || job.Delivery.ChatID != "oc_chat" || job.Delivery.ChatType != "group" || job.Delivery.ReplyToMessageID != "om_msg" {
		t.Fatalf("unexpected job target: %+v", job.Delivery)
	}
}
