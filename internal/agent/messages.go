package agent

import (
	"context"

	cobot "github.com/cobot-agent/cobot/pkg"
)

// buildMessages assembles the message list for the LLM, prepending the system
// prompt (cached LTM + fresh STM) as the first message.
func (a *Agent) buildMessages(ctx context.Context) []cobot.Message {
	sm := a.sessionMgr
	msgs := sm.session.Messages()
	system := sm.GetSystemPrompt()
	if system == "" {
		return msgs
	}

	// Append STM context on every turn (not cached like LTM).
	stmText := sm.getSTMContext(ctx)
	if stmText != "" {
		system = system + "\n\n" + stmText
	}

	return append([]cobot.Message{{Role: cobot.RoleSystem, Content: system}}, msgs...)
}
