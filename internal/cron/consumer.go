package cron

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	cobot "github.com/cobot-agent/cobot/pkg"
)

// consumeLoop periodically consumes cron result messages from the broker.
// On first call it acks all pre-existing messages to avoid re-delivering
// results from before this process started.
func (s *Scheduler) consumeLoop(ctx context.Context) {
	defer s.wg.Done()

	s.ackAllExisting(ctx)

	ticker := time.NewTicker(consumeInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.consumeOnce(ctx)
		}
	}
}

const maxAckMessages = 1000

// ackAllExisting consumes and acks all pending messages without notifying.
// This prevents re-delivery of cron results from previous process lifetimes.
// It consumes messages for ALL channels (channelID="") because on restart the
// new sessionID has no prior consume state — any unacked messages from the old
// session would otherwise be re-delivered. In single-instance deployments this
// is always safe; in multi-instance deployments, each instance acks on behalf
// of its own previous session.
func (s *Scheduler) ackAllExisting(ctx context.Context) {
	acked := 0
	for {
		msgs, err := s.broker.Consume(ctx, topicCronResult, "", s.sessionID, 100)
		if err != nil || len(msgs) == 0 {
			slog.Debug("ackAllExisting completed", "acked", acked)
			return
		}
		// Batch ack all messages in the fetched batch.
		ids := make([]string, 0, len(msgs))
		for _, msg := range msgs {
			ids = append(ids, msg.ID)
		}
		if err := s.broker.AckAll(ctx, ids, s.sessionID); err != nil {
			slog.Warn("batch ack failed", "error", err, "count", len(ids))
		}
		acked += len(msgs)
		if acked >= maxAckMessages {
			slog.Warn("ackAllExisting hit iteration limit, some messages remain", "limit", maxAckMessages)
			break
		}
	}
}

// consumeOnce consumes unacknowledged cron result messages and delivers them locally.
func (s *Scheduler) consumeOnce(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			slog.Warn("consumeOnce recovered from panic", "error", r)
		}
	}()
	// sessionID is used as the consume session identity (separate from leader lease holderID).
	msgs, err := s.broker.Consume(ctx, topicCronResult, "", s.sessionID, 50)
	if err != nil {
		slog.Warn("failed to consume cron results", "error", err)
		return
	}

	if len(msgs) == 0 {
		return
	}

	notifyCtx, notifyCancel := context.WithTimeout(ctx, brokerOpTimeout)
	defer notifyCancel()

	ackIDs := make([]string, 0, len(msgs))
	for _, msg := range msgs {
		payload, err := decodeCronResult(msg)
		if err != nil {
			slog.Warn("failed to decode cron result", "msg_id", msg.ID, "error", err)
			ackIDs = append(ackIDs, msg.ID)
			continue
		}
		if msg.ChannelID == "" {
			ackIDs = append(ackIDs, msg.ID)
			continue
		}
		content := formatCronResult(payload.JobName, payload.Result, payload.Error)
		if s.deliverer != nil {
			title := fmt.Sprintf("Cron job %q completed", payload.JobName)
			out := &cobot.OutboundMessage{
				Text: title + "\n\n" + content,
			}
			if _, err := s.deliverer.Send(notifyCtx, msg.ChannelID, out); err != nil {
				slog.Warn("failed to deliver cron result", "channel_id", msg.ChannelID, "error", err)
			}
		}
		ackIDs = append(ackIDs, msg.ID)
	}
	if len(ackIDs) > 0 {
		if err := s.broker.AckAll(notifyCtx, ackIDs, s.sessionID); err != nil {
			slog.Warn("failed to batch ack cron results", "error", err)
		}
	}
}

// formatCronResult formats a cron job execution result for display.
func formatCronResult(jobName, result, runErr string) string {
	if runErr != "" {
		return fmt.Sprintf("❌ Job %s failed: %s", jobName, runErr)
	}
	return fmt.Sprintf("✅ Job %s result:\n%s", jobName, result)
}

// publishJobResult publishes the job result via the broker so followers can consume it.
func (s *Scheduler) publishJobResult(job *Job, result string, runErr error, duration time.Duration) {
	payload := &cronResultPayload{
		JobID:    job.ID,
		JobName:  job.Name,
		Result:   result,
		RunAt:    time.Now(),
		Duration: duration.Milliseconds(),
	}
	if runErr != nil {
		payload.Error = runErr.Error()
	}
	msg, err := newCronResultMessage(job.ChannelID, payload)
	if err != nil {
		slog.Warn("failed to marshal cron result", "job_id", job.ID, "error", err)
		return
	}
	// Use Background() so publish completes even if the job's ctx was cancelled.
	ctx, cancel := context.WithTimeout(context.Background(), brokerOpTimeout)
	defer cancel()
	if err := s.broker.Publish(ctx, msg); err != nil {
		slog.Warn("failed to publish cron result", "job_id", job.ID, "error", err)
	}
}
