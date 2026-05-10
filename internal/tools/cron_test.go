package tools

import (
	"context"
	"encoding/json"
	"strings"
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
		ReceiveIDType:    "chat_id",
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
	if job.Delivery.ChannelID != "feishu:test" || job.Delivery.ChatID != "oc_chat" || job.Delivery.ChatType != "group" || job.Delivery.ReceiveIDType != "chat_id" || job.Delivery.ReplyToMessageID != "om_msg" {
		t.Fatalf("unexpected job target: %+v", job.Delivery)
	}
}

func TestCronToolBindAttachesCurrentConversationTarget(t *testing.T) {
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

	job := &cron.Job{
		ID:       cron.NewJobID(),
		Name:     "legacy",
		Schedule: "0 9 * * *",
		Prompt:   "say hi",
		Status:   cron.StatusActive,
	}
	if err := scheduler.AddJob(job); err != nil {
		t.Fatalf("AddJob: %v", err)
	}

	jobs, err := scheduler.ListJobs()
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	readID := jobs[0].ReadID()
	jobID := jobs[0].ID

	ctx := requestctx.WithMessageTarget(context.Background(), requestctx.MessageTarget{
		ChannelID:        "feishu:test",
		ChatID:           "oc_chat",
		ChatType:         "group",
		ReceiveIDType:    "chat_id",
		ReplyToMessageID: "om_msg",
	})

	args, err := json.Marshal(map[string]any{
		"action": "bind",
		"job_id": jobID,
	})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}

	if _, err := tool.Execute(ctx, args); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	jobs, err = scheduler.ListJobs()
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	bound := jobs[0]
	if bound.Delivery.ChannelID != "feishu:test" || bound.Delivery.ChatID != "oc_chat" || bound.Delivery.ChatType != "group" || bound.Delivery.ReceiveIDType != "chat_id" || bound.Delivery.ReplyToMessageID != "om_msg" {
		t.Fatalf("unexpected bound target: %+v", bound.Delivery)
	}

	// Backward compatibility: read_id still works.
	bound.Delivery = cron.DeliveryTarget{}
	if err := store.Update(bound); err != nil {
		t.Fatalf("store Update reset delivery: %v", err)
	}
	args, err = json.Marshal(map[string]any{
		"action":  "bind",
		"read_id": readID,
	})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	if _, err := tool.Execute(ctx, args); err != nil {
		t.Fatalf("Execute read_id bind: %v", err)
	}
}

func TestCronToolBindAutoSelectsSingleMissingTargetJob(t *testing.T) {
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

	job := &cron.Job{ID: cron.NewJobID(), Name: "legacy", Schedule: "0 9 * * *", Prompt: "say hi", Status: cron.StatusActive}
	if err := scheduler.AddJob(job); err != nil {
		t.Fatalf("AddJob: %v", err)
	}

	ctx := requestctx.WithMessageTarget(context.Background(), requestctx.MessageTarget{
		ChannelID:        "feishu:test",
		ChatID:           "oc_chat",
		ChatType:         "group",
		ReceiveIDType:    "chat_id",
		ReplyToMessageID: "om_msg",
	})
	args, err := json.Marshal(map[string]any{"action": "bind"})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	out, err := tool.Execute(ctx, args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, job.ID) {
		t.Fatalf("expected bind output to mention job id %s, got %s", job.ID, out)
	}
}

func TestCronToolListMarksMissingDeliveryTarget(t *testing.T) {
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

	job := &cron.Job{
		ID:       cron.NewJobID(),
		Name:     "legacy",
		Schedule: "0 9 * * *",
		Prompt:   "say hi",
		Status:   cron.StatusActive,
	}
	if err := scheduler.AddJob(job); err != nil {
		t.Fatalf("AddJob: %v", err)
	}

	args, err := json.Marshal(map[string]any{"action": "list"})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	out, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "delivery=missing") {
		t.Fatalf("expected missing delivery marker, got: %s", out)
	}
	if !strings.Contains(out, "read_id=") {
		t.Fatalf("expected read_id in list output, got: %s", out)
	}
}
