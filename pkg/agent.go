package cobot

// Agent-related defaults.
const (
	DefaultMaxTurns = 50

	DefaultSystemPrompt = "You are Cobot, a personal AI assistant."

	DefaultSubAgentSystemPrompt = `You are a focused sub-agent delegated to complete a specific task. Be direct and efficient. Do not call delegate_task (avoid infinite recursion). You do not have access to the main agent's persistent memory store. Use the provided tools to accomplish the task and return a concise result.`
)
