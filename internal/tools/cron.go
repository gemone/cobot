package tools

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cobot-agent/cobot/internal/requestctx"
	"github.com/cobot-agent/cobot/internal/textutil"

	"github.com/cobot-agent/cobot/internal/cron"
	cobot "github.com/cobot-agent/cobot/pkg"
)

//go:embed schemas/embed_cron_params.json
var cronParamsJSON []byte

var _ cobot.Tool = (*CronTool)(nil)

const (
	actionCreate   = "create"
	actionList     = "list"
	actionDelete   = "delete"
	actionPause    = "pause"
	actionResume   = "resume"
	actionListRuns = "list_runs"
)

const displayTimeFmt = "2006-01-02 15:04:05"

// CronTool allows the agent to schedule and manage recurring and one-shot tasks.
type CronTool struct {
	scheduler *cron.Scheduler
}

// CronToolOption is a functional option for CronTool.
type CronToolOption func(*CronTool)

// NewCronTool creates a new CronTool with the given scheduler.
func NewCronTool(scheduler *cron.Scheduler, opts ...CronToolOption) *CronTool {
	t := &CronTool{scheduler: scheduler}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

func (t *CronTool) Name() string { return "cron" }

func (t *CronTool) Description() string {
	return `Schedule and manage recurring and one-shot tasks. Actions: create (schedule a new job), list (show all jobs), delete (remove a job), pause (temporarily stop a job), resume (restart a paused job), list_runs (show execution history for a job). Use cron expressions like "0 9 * * *" for recurring tasks or ISO timestamps for one-shot tasks. Results are stored in per-job run databases and can be viewed with list_runs.`
}

func (t *CronTool) Parameters() json.RawMessage {
	return json.RawMessage(cronParamsJSON)
}

type cronParams struct {
	Action   string `json:"action"`
	Schedule string `json:"schedule"`
	Prompt   string `json:"prompt"`
	JobID    string `json:"job_id"`
	ReadID   string `json:"read_id"`
	Name     string `json:"name"`
	Model    string `json:"model"`
	Limit    int    `json:"limit,omitempty"`
}

func (t *CronTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var params cronParams
	if err := decodeArgs(args, &params); err != nil {
		return "", err
	}

	switch params.Action {
	case actionCreate:
		return t.handleCreate(ctx, params)
	case actionList:
		return t.handleList()
	case actionDelete:
		return t.handleDelete(params)
	case actionPause:
		return t.handlePause(params)
	case actionResume:
		return t.handleResume(params)
	case actionListRuns:
		return t.handleListRuns(params)
	default:
		return "", fmt.Errorf("unknown action: %s (valid: %s, %s, %s, %s, %s, %s)", params.Action, actionCreate, actionList, actionDelete, actionPause, actionResume, actionListRuns)
	}
}

func (t *CronTool) handleCreate(ctx context.Context, params cronParams) (string, error) {
	if params.Schedule == "" {
		return "", fmt.Errorf("schedule is required for create action")
	}
	if params.Prompt == "" {
		return "", fmt.Errorf("prompt is required for create action")
	}

	oneShot := cron.IsOneShot(params.Schedule)

	name := params.Name
	if name == "" {
		name = "unnamed"
	}

	job := &cron.Job{
		ID:        cron.NewJobID(),
		Name:      name,
		Schedule:  params.Schedule,
		Prompt:    params.Prompt,
		Model:     params.Model,
		Status:    cron.StatusActive,
		OneShot:   oneShot,
		CreatedAt: time.Now(),
	}
	applyMessageTarget(job, ctx)

	if err := t.scheduler.AddJob(job); err != nil {
		return "", err
	}

	var nextStr string
	if job.NextRun != nil {
		nextStr = job.NextRun.Format(time.RFC3339)
	} else {
		nextStr = "N/A"
	}

	typ := "recurring"
	if oneShot {
		typ = "one-shot"
	}
	return fmt.Sprintf("Job created:\n  ID: %s\n  read_id: %s\n  Name: %s\n  Schedule: %s\n  Type: %s\n  Next run: %s\n",
		job.ID, job.ReadID(), job.Name, job.Schedule, typ, nextStr), nil
}

func applyMessageTarget(job *cron.Job, ctx context.Context) {
	target, ok := requestctx.MessageTargetFromContext(ctx)
	if !ok {
		return
	}
	job.Delivery = cron.DeliveryTarget{
		ChannelID:        target.ChannelID,
		ChatID:           target.ChatID,
		ChatType:         target.ChatType,
		ReplyToMessageID: target.ReplyToMessageID,
	}
}

func (t *CronTool) handleList() (string, error) {
	jobs, err := t.scheduler.ListJobs()
	if err != nil {
		return "", err
	}

	if len(jobs) == 0 {
		return "No cron jobs found.", nil
	}

	var b strings.Builder
	b.Grow(len(jobs) * 120)
	fmt.Fprintf(&b, "Cron jobs (%d):\n", len(jobs))
	for _, job := range jobs {
		lastRun := "never"
		if job.LastRun != nil {
			lastRun = job.LastRun.Format(time.RFC3339)
		}
		nextRun := "N/A"
		if job.NextRun != nil {
			nextRun = job.NextRun.Format(time.RFC3339)
		}
		fmt.Fprintf(&b, "  %s | %s | %s | status=%s | runs=%d | last=%s | next=%s | read_id=%s\n",
			job.ID, job.Name, job.Schedule, job.Status, job.RunCount, lastRun, nextRun, job.ReadID())
	}
	return b.String(), nil
}

// withReadID validates and extracts a job ID from readID, calls fn, and formats the result.
func (t *CronTool) withReadID(readID string, action string, fn func(string) error, verb string) (string, error) {
	if readID == "" {
		return "", fmt.Errorf("read_id is required for %s action. Use the list action first to get the current read_id", action)
	}
	jobID, _, err := cron.ParseReadID(readID)
	if err != nil {
		return "", fmt.Errorf("invalid read ID: %w", err)
	}
	if err := fn(readID); err != nil {
		return "", err
	}
	return fmt.Sprintf("Job %s %s.", jobID, verb), nil
}

func (t *CronTool) handleDelete(params cronParams) (string, error) {
	return t.withReadID(params.ReadID, "delete", t.scheduler.RemoveJob, "deleted")
}

func (t *CronTool) handlePause(params cronParams) (string, error) {
	return t.withReadID(params.ReadID, "pause", t.scheduler.PauseJob, "paused")
}

func (t *CronTool) handleResume(params cronParams) (string, error) {
	return t.withReadID(params.ReadID, "resume", t.scheduler.ResumeJob, "resumed")
}

func (t *CronTool) handleListRuns(params cronParams) (string, error) {
	if params.JobID == "" {
		return "", fmt.Errorf("job_id is required for list_runs action")
	}
	records, err := t.scheduler.ListJobRuns(params.JobID, params.Limit)
	if err != nil {
		return "", err
	}
	if len(records) == 0 {
		return fmt.Sprintf("No execution records for job %s.", params.JobID), nil
	}
	var b strings.Builder
	b.Grow(len(records) * 120)
	fmt.Fprintf(&b, "Execution records for job %s (%d most recent):\n", params.JobID, len(records))
	for _, r := range records {
		if r.Error != "" {
			fmt.Fprintf(&b, "  [%s] FAILED (%dms): %s\n", r.RunAt.Format(displayTimeFmt), r.Duration, r.Error)
		} else {
			output := textutil.Truncate(r.Result, 100)
			fmt.Fprintf(&b, "  [%s] OK (%dms): %s\n", r.RunAt.Format(displayTimeFmt), r.Duration, output)
		}
	}
	return b.String(), nil
}
