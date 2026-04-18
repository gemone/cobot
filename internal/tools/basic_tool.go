package tools

import (
	"encoding/json"
)

// BasicTool eliminates Name/Description/Parameters boilerplate.
// Embed it in tool structs that follow the standard sandbox description pattern.
type BasicTool struct {
	sandboxTool
	name   string
	desc   string
	params json.RawMessage
}

func (b *BasicTool) Name() string                { return b.name }
func (b *BasicTool) Description() string         { return b.describeWithSandbox(b.desc) }
func (b *BasicTool) Parameters() json.RawMessage { return b.params }
