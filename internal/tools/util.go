package tools

import (
	"context"
	"encoding/json"

	cobot "github.com/cobot-agent/cobot/pkg"
)

var decodeArgs = cobot.DecodeToolArgs

// fnTool adapts a function into a cobot.Tool, eliminating per-tool struct boilerplate.
type fnTool struct {
	name    string
	desc    string
	params  json.RawMessage
	execute func(ctx context.Context, args json.RawMessage) (string, error)
}

func (t *fnTool) Name() string                { return t.name }
func (t *fnTool) Description() string         { return t.desc }
func (t *fnTool) Parameters() json.RawMessage { return t.params }
func (t *fnTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	return t.execute(ctx, args)
}
