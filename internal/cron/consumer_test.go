package cron

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	cobot "github.com/cobot-agent/cobot/pkg"
	"github.com/cobot-agent/cobot/pkg/broker"
)

// ---------------------------------------------------------------------------
// Fakes for consumer tests
// ---------------------------------------------------------------------------

// fakeDeliverer records Send calls for test assertions.
type fakeDeliverer struct {
	mu    sync.Mutex
	calls []deliverCall
}

type deliverCall struct {
	channelID string
	msg       *cobot.OutboundMessage
}

func (f *fakeDeliverer) Send(_ context.Context, channelID string, msg *cobot.OutboundMessage) (*cobot.SendResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, deliverCall{channelID: channelID, msg: msg})
	return &cobot.SendResult{Success: true, MessageID: "test-msg-id"}, nil
}

func (f *fakeDeliverer) getCalls() []deliverCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]deliverCall, len(f.calls))
	copy(out, f.calls)
	return out
}

// fakeBrokerForAck is a minimal broker.Broker implementation for testing
// ackAllExisting. Only Consume and AckAll are meaningful; all other methods
// are no-ops.
type fakeBrokerForAck struct {
	consumeCalls int
	ackAllFn     func(ctx context.Context, msgIDs []string, sessionID string) error
}

func (f *fakeBrokerForAck) TryAcquire(_ context.Context, _ string, _ string, _ time.Duration) (bool, error) {
	return false, nil
}
func (f *fakeBrokerForAck) Renew(_ context.Context, _ string, _ string, _ time.Duration) error {
	return nil
}
func (f *fakeBrokerForAck) Release(_ context.Context, _ string, _ string) error { return nil }
func (f *fakeBrokerForAck) Register(_ context.Context, _ *broker.SessionInfo) error {
	return nil
}
func (f *fakeBrokerForAck) Unregister(_ context.Context, _ string) error { return nil }
func (f *fakeBrokerForAck) Heartbeat(_ context.Context, _ string) error  { return nil }
func (f *fakeBrokerForAck) ListByChannel(_ context.Context, _ string) ([]*broker.SessionInfo, error) {
	return nil, nil
}
func (f *fakeBrokerForAck) ListAll(_ context.Context) ([]*broker.SessionInfo, error) {
	return nil, nil
}
func (f *fakeBrokerForAck) Publish(_ context.Context, _ *broker.Message) error { return nil }
func (f *fakeBrokerForAck) Ack(_ context.Context, _ string, _ string) error    { return nil }
func (f *fakeBrokerForAck) Cleanup(_ context.Context) error                    { return nil }
func (f *fakeBrokerForAck) Close() error                                       { return nil }

func (f *fakeBrokerForAck) Consume(_ context.Context, topic, channelID, sessionID string, limit int) ([]*broker.Message, error) {
	f.consumeCalls++
	// Return messages for the first 11 batches (1100 msgs total) then stop.
	if f.consumeCalls > 11 {
		return nil, nil
	}
	msgs := make([]*broker.Message, limit)
	for i := range msgs {
		msgs[i] = &broker.Message{
			ID:        fmt.Sprintf("msg-%d-%d", f.consumeCalls, i),
			Topic:     topic,
			ChannelID: channelID,
		}
	}
	return msgs, nil
}

func (f *fakeBrokerForAck) AckAll(ctx context.Context, msgIDs []string, sessionID string) error {
	if f.ackAllFn != nil {
		return f.ackAllFn(ctx, msgIDs, sessionID)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestFormatCronResult verifies the formatting of cron job execution results.
func TestFormatCronResult(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		jobName string
		result  string
		runErr  string
		want    string
	}{
		{
			name:    "success with output",
			jobName: "backup",
			result:  "backup completed successfully",
			runErr:  "",
			want:    "✅ Job backup result:\nbackup completed successfully",
		},
		{
			name:    "error",
			jobName: "backup",
			result:  "",
			runErr:  "exit status 1",
			want:    "❌ Job backup failed: exit status 1",
		},
		{
			name:    "success with multi-line output",
			jobName: "report",
			result:  "line1\nline2\nline3",
			runErr:  "",
			want:    "✅ Job report result:\nline1\nline2\nline3",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatCronResult(tt.jobName, tt.result, tt.runErr)
			if got != tt.want {
				t.Errorf("formatCronResult(%q, %q, %q) =\n%q\nwant:\n%q", tt.jobName, tt.result, tt.runErr, got, tt.want)
			}
		})
	}
}

// TestAckAllExisting_IterationLimit verifies that ackAllExisting stops after
// maxAckMessages even when the broker still has messages to return.
func TestAckAllExisting_IterationLimit(t *testing.T) {
	t.Parallel()

	var ackedTotal int
	fb := &fakeBrokerForAck{
		ackAllFn: func(_ context.Context, msgIDs []string, _ string) error {
			ackedTotal += len(msgIDs)
			return nil
		},
	}

	store := NewStore(t.TempDir())
	s := NewScheduler(store, noopExecuteFn, nil, fb, nil)

	s.ackAllExisting(context.Background())

	// Should have consumed exactly 10 batches (10 × 100 = 1000 = maxAckMessages).
	if fb.consumeCalls != 10 {
		t.Errorf("expected 10 Consume calls, got %d", fb.consumeCalls)
	}

	// Should have acked exactly 1000 messages.
	if ackedTotal != maxAckMessages {
		t.Errorf("expected %d acked messages, got %d", maxAckMessages, ackedTotal)
	}
}

// TestConsumeOnce_EmptyChannelID verifies that messages with an empty
// ChannelID are acked but not delivered.
func TestConsumeOnce_EmptyChannelID(t *testing.T) {
	t.Parallel()

	br, cleanup := tempTestBroker(t)
	defer cleanup()
	ctx := context.Background()

	deliverer := &fakeDeliverer{}
	store := NewStore(t.TempDir())
	s := NewScheduler(store, noopExecuteFn, nil, br, deliverer)

	// Publish a cron result with empty channel ID.
	payload := &cronResultPayload{
		JobID:   "job-empty-ch",
		JobName: "test-empty-channel",
		Result:  "hello",
	}
	msg, err := newCronResultMessage("", payload)
	if err != nil {
		t.Fatalf("newCronResultMessage: %v", err)
	}
	if err := br.Publish(ctx, msg); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	s.consumeOnce(ctx)

	if calls := deliverer.getCalls(); len(calls) != 0 {
		t.Errorf("expected 0 deliver calls for empty ChannelID, got %d", len(calls))
	}

	msgs, err := br.Consume(ctx, topicCronResult, "", s.sessionID, 50)
	if err != nil {
		t.Fatalf("Consume after ack: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages after ack, got %d", len(msgs))
	}
}

// TestConsumeOnce_ValidChannelID verifies that messages with a non-empty
// ChannelID are both delivered and acked.
func TestConsumeOnce_ValidChannelID(t *testing.T) {
	t.Parallel()

	br, cleanup := tempTestBroker(t)
	defer cleanup()
	ctx := context.Background()

	deliverer := &fakeDeliverer{}
	store := NewStore(t.TempDir())
	s := NewScheduler(store, noopExecuteFn, nil, br, deliverer)

	// Publish a cron result with a valid channel ID.
	payload := &cronResultPayload{
		JobID:   "job-valid-ch",
		JobName: "test-valid-channel",
		Result:  "world",
	}
	msg, err := newCronResultMessage("channel-123", payload)
	if err != nil {
		t.Fatalf("newCronResultMessage: %v", err)
	}
	if err := br.Publish(ctx, msg); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	s.consumeOnce(ctx)

	calls := deliverer.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 deliver call, got %d", len(calls))
	}
	if calls[0].channelID != "channel-123" {
		t.Errorf("deliver channelID = %q, want %q", calls[0].channelID, "channel-123")
	}
	if calls[0].msg == nil {
		t.Fatal("expected delivered message")
	}
	if calls[0].msg.ReceiveID != "" {
		t.Errorf("deliver msg.ReceiveID = %q, want empty (channel resolves platform destination)", calls[0].msg.ReceiveID)
	}
	wantTitle := `Cron job "test-valid-channel" completed`
	if !strings.Contains(calls[0].msg.Text, wantTitle) {
		t.Errorf("deliver msg.Text missing title %q; got %q", wantTitle, calls[0].msg.Text)
	}
	if !strings.Contains(calls[0].msg.Text, "world") {
		t.Errorf("deliver msg.Text missing result %q; got %q", "world", calls[0].msg.Text)
	}

	msgs, err := br.Consume(ctx, topicCronResult, "", s.sessionID, 50)
	if err != nil {
		t.Fatalf("Consume after ack: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages after ack, got %d", len(msgs))
	}
}
