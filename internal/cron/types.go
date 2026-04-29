package cron

import (
	"encoding/json"
	"time"

	"github.com/cobot-agent/cobot/pkg/broker"
	"github.com/google/uuid"
)

// cronResultPayload is the message payload for cron task execution results.
type cronResultPayload struct {
	JobID    string    `json:"job_id"`
	JobName  string    `json:"job_name"`
	Result   string    `json:"result"`
	Error    string    `json:"error,omitempty"`
	RunAt    time.Time `json:"run_at"`
	Duration int64     `json:"duration_ms"`
}

// newCronResultMessage builds a cron result message.
func newCronResultMessage(channelID string, payload *cronResultPayload) (*broker.Message, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return &broker.Message{
		ID:        uuid.NewString(),
		Topic:     topicCronResult,
		ChannelID: channelID,
		Payload:   data,
		CreatedAt: time.Now(),
	}, nil
}

// decodeCronResult decodes Message.Payload into a cronResultPayload.
func decodeCronResult(msg *broker.Message) (*cronResultPayload, error) {
	var p cronResultPayload
	if err := json.Unmarshal(msg.Payload, &p); err != nil {
		return nil, err
	}
	return &p, nil
}
