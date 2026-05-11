package requestctx

import (
	"context"

	cobot "github.com/cobot-agent/cobot/pkg"
)

type messageTargetKey struct{}

// MessageTarget describes where follow-up messages should be delivered.
type MessageTarget struct {
	ChannelID        string
	ChatID           string
	ChatType         string
	ReceiveIDType    string
	ReplyToMessageID string
}

func NewMessageTarget(channelID string, msg *cobot.InboundMessage) MessageTarget {
	if msg == nil {
		return MessageTarget{ChannelID: channelID}
	}
	return MessageTarget{
		ChannelID:        channelID,
		ChatID:           msg.ChatID,
		ChatType:         msg.ChatType,
		ReceiveIDType:    "chat_id",
		ReplyToMessageID: msg.MessageID,
	}
}

func WithMessageTarget(ctx context.Context, target MessageTarget) context.Context {
	return context.WithValue(ctx, messageTargetKey{}, target)
}

func MessageTargetFromContext(ctx context.Context) (MessageTarget, bool) {
	target, ok := ctx.Value(messageTargetKey{}).(MessageTarget)
	return target, ok
}
