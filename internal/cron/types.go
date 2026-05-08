package cron

import (
	"encoding/json"
	"time"

	"github.com/cobot-agent/cobot/pkg/broker"
	"github.com/google/uuid"
)

// cronResultPayload is the message payload for cron task execution results.
type cronResultPayload struct {
	JobID    string         `json:"job_id"`
	JobName  string         `json:"job_name"`
	Result   string         `json:"result"`
	Error    string         `json:"error,omitempty"`
	RunAt    time.Time      `json:"run_at"`
	Duration int64          `json:"duration_ms"`
	Delivery DeliveryTarget `json:"delivery,omitempty"`
}

type DeliveryTarget struct {
	ChannelID        string `json:"channel_id,omitempty"         yaml:"channel_id,omitempty"`
	ChatID           string `json:"chat_id,omitempty"            yaml:"chat_id,omitempty"`
	ChatType         string `json:"chat_type,omitempty"          yaml:"chat_type,omitempty"`
	ReplyToMessageID string `json:"reply_to_message_id,omitempty" yaml:"reply_to_message_id,omitempty"`
}

func (p *cronResultPayload) deliveryTarget(fallbackChannelID string) DeliveryTarget {
	target := p.Delivery
	if target.ChannelID == "" {
		target.ChannelID = fallbackChannelID
	}
	return target
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
