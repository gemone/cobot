package cron

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	cobot "github.com/cobot-agent/cobot/pkg"
	"github.com/cobot-agent/cobot/pkg/broker"
)

func isSQLITEBusy(err error) bool {
	return err != nil && strings.Contains(err.Error(), "SQLITE_BUSY")
}

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

func (s *Scheduler) consumeOnce(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			slog.Warn("consumeOnce recovered from panic", "error", r)
		}
	}()

	var msgs []*broker.Message
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		msgs, err = s.broker.Consume(ctx, topicCronResult, "", s.sessionID, 50)
		if !isSQLITEBusy(err) {
			break
		}
		time.Sleep(time.Duration(50<<attempt) * time.Millisecond)
	}
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
		if s.shouldAckCronResult(notifyCtx, msg) {
			ackIDs = append(ackIDs, msg.ID)
		}
	}
	if len(ackIDs) > 0 {
		if err := s.broker.AckAll(notifyCtx, ackIDs, s.sessionID); err != nil {
			slog.Warn("failed to batch ack cron results", "error", err)
		}
	}
}

func (s *Scheduler) shouldAckCronResult(ctx context.Context, msg *broker.Message) bool {
	payload, err := decodeCronResult(msg)
	if err != nil {
		slog.Warn("failed to decode cron result", "msg_id", msg.ID, "error", err)
		return true
	}

	target := payload.deliveryTarget(msg.ChannelID)
	if target.ChannelID == "" {
		slog.Warn("cron result has no channel target, skipping delivery", "job_id", payload.JobID, "chat_id", target.ChatID)
		return true
	}
	if s.deliverer == nil {
		return true
	}

	content := formatCronResult(payload.JobName, payload.Result, payload.Error)
	return s.deliverCronResult(ctx, payload, target, content)
}

func (s *Scheduler) deliverCronResult(ctx context.Context, payload *cronResultPayload, target DeliveryTarget, content string) bool {
	if target.ChannelID == "" || target.ChatID == "" {
		slog.Warn("cron result has no delivery target, skipping delivery", "job_id", payload.JobID, "channel_id", target.ChannelID, "chat_id", target.ChatID)
		return false
	}

	if s.sendCronResult(ctx, payload.JobName, content, target) {
		return true
	}
	if target.ReplyToMessageID == "" {
		return false
	}

	slog.Warn("failed to reply with cron result, retrying without reply target", "channel_id", target.ChannelID, "chat_id", target.ChatID, "reply_to", target.ReplyToMessageID)
	target.ReplyToMessageID = ""
	return s.sendCronResult(ctx, payload.JobName, content, target)
}

func (s *Scheduler) sendCronResult(ctx context.Context, jobName, content string, target DeliveryTarget) bool {
	_, err := cronResultSendSucceeded(s.deliverer.Send(ctx, target.ChannelID, cronResultOutboundMessage(jobName, content, target)))
	if err == nil {
		return true
	}
	slog.Warn("failed to deliver cron result", "channel_id", target.ChannelID, "chat_id", target.ChatID, "error", err)
	return false
}

func cronResultSendSucceeded(result *cobot.SendResult, err error) (bool, error) {
	if err != nil {
		return false, err
	}
	if result == nil {
		return false, fmt.Errorf("delivery returned no result")
	}
	if !result.Success {
		return false, fmt.Errorf("delivery reported unsuccessful send")
	}
	return true, nil
}

func cronResultOutboundMessage(jobName, content string, target DeliveryTarget) *cobot.OutboundMessage {
	title := fmt.Sprintf("Cron job %q completed", jobName)
	return &cobot.OutboundMessage{
		ReceiveID:        target.ChatID,
		ReceiveType:      target.ChatType,
		ReplyToMessageID: target.ReplyToMessageID,
		Text:             title + "\n\n" + content,
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
		Delivery: job.Delivery,
	}
	if runErr != nil {
		payload.Error = runErr.Error()
	}
	msg, err := newCronResultMessage(job.Delivery.ChannelID, payload)
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
