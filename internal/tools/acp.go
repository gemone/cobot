package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	cobot "github.com/cobot-agent/cobot/pkg"
)

// ACPSubAgent implements cobot.SubAgent by launching an external ACP-compatible process.
type ACPSubAgent struct {
	command      string
	args         []string
	workdir      string
	timeout      time.Duration
	systemPrompt string
	model        string
}

// NewACPSubAgent creates a new external ACP sub-agent.
func NewACPSubAgent(command string, args []string, workdir string, timeout time.Duration) *ACPSubAgent {
	if command == "" {
		command = "opencode"
	}
	if timeout == 0 {
		timeout = 10 * time.Minute
	}
	return &ACPSubAgent{
		command: command,
		args:    args,
		workdir: workdir,
		timeout: timeout,
	}
}

// SetModel stores the model spec; external agents may ignore it.
func (a *ACPSubAgent) SetModel(spec string) error {
	a.model = spec
	return nil
}

// SetSystemPrompt stores the system prompt to send with each prompt.
func (a *ACPSubAgent) SetSystemPrompt(prompt string) error {
	a.systemPrompt = prompt
	return nil
}

// buildCmd constructs the exec.Cmd. If the args contain "run", it treats the
// command as an opencode-style CLI runner and appends the prompt as the final
// positional argument with --format json.
func (a *ACPSubAgent) buildCmd(ctx context.Context, message string) *exec.Cmd {
	isRunMode := false
	for _, arg := range a.args {
		if arg == "run" {
			isRunMode = true
			break
		}
	}

	var cmdArgs []string
	if isRunMode {
		cmdArgs = append([]string{}, a.args...)
		fullPrompt := message
		if a.systemPrompt != "" {
			fullPrompt = a.systemPrompt + "\n\n" + message
		}
		cmdArgs = append(cmdArgs, fullPrompt, "--format", "json")
	} else {
		cmdArgs = append([]string{}, a.args...)
	}

	cmd := exec.CommandContext(ctx, a.command, cmdArgs...)
	if a.workdir != "" {
		cmd.Dir = a.workdir
	}
	return cmd
}

// writeRequest sends the pragmatic request JSON to stdin for non-run modes.
func (a *ACPSubAgent) writeRequest(stdin io.WriteCloser, message string) error {
	req := map[string]any{
		"prompt": message,
	}
	if a.systemPrompt != "" {
		req["system_prompt"] = a.systemPrompt
	}
	if a.model != "" {
		req["model"] = a.model
	}
	reqBytes, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	if _, err := stdin.Write(append(reqBytes, '\n')); err != nil {
		return fmt.Errorf("write request: %w", err)
	}
	return stdin.Close()
}

type openCodeEvent struct {
	Type string `json:"type"`
	Part struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"part"`
}

// parseLine tries to interpret a stdout line as either an opencode JSON event
// or a pragmatic ACP event. It returns (isText, text, isDone, isError, error).
func parseLine(line string) (bool, string, bool, bool, error) {
	var oc openCodeEvent
	if err := json.Unmarshal([]byte(line), &oc); err == nil {
		switch oc.Type {
		case "text":
			return true, oc.Part.Text, false, false, nil
		case "step_finish":
			return false, "", true, false, nil
		}
	}

	var evt acpEvent
	if err := json.Unmarshal([]byte(line), &evt); err == nil {
		switch evt.Event {
		case "text":
			return true, evt.Content, false, false, nil
		case "done":
			return false, "", true, false, nil
		case "error":
			return false, "", false, true, fmt.Errorf("%s", evt.Message)
		default:
			return false, "", false, false, nil
		}
	}

	// Fallback: raw text.
	return true, line + "\n", false, false, nil
}

// Prompt sends a single prompt and returns the collected response.
func (a *ACPSubAgent) Prompt(ctx context.Context, message string) (*cobot.ProviderResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	cmd := a.buildCmd(ctx, message)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	cmd.Stderr = nil // discard stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start command: %w", err)
	}

	kill := func() {
		if cmd.Process != nil {
			_ = cmd.Process.Signal(os.Interrupt)
			time.AfterFunc(2*time.Second, func() { _ = cmd.Process.Kill() })
		}
		_ = cmd.Wait()
	}

	// Only write JSON request for non-run modes.
	if !strings.Contains(strings.Join(a.args, " "), "run") {
		if err := a.writeRequest(stdin, message); err != nil {
			kill()
			return nil, err
		}
	} else {
		_ = stdin.Close()
	}

	var result strings.Builder
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		isText, text, isDone, isErr, err := parseLine(line)
		if isErr {
			kill()
			return nil, err
		}
		if isDone {
			_ = cmd.Wait()
			return &cobot.ProviderResponse{Content: result.String(), StopReason: cobot.StopEndTurn}, nil
		}
		if isText {
			result.WriteString(text)
		}
	}

	if err := scanner.Err(); err != nil {
		kill()
		return nil, fmt.Errorf("read stdout: %w", err)
	}

	_ = cmd.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return &cobot.ProviderResponse{Content: result.String(), StopReason: cobot.StopEndTurn}, nil
}

// Stream sends a prompt and returns a channel of events.
func (a *ACPSubAgent) Stream(ctx context.Context, message string) (<-chan cobot.Event, error) {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)

	cmd := a.buildCmd(ctx, message)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	cmd.Stderr = nil // discard stderr

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start command: %w", err)
	}

	kill := func() {
		if cmd.Process != nil {
			_ = cmd.Process.Signal(os.Interrupt)
			time.AfterFunc(2*time.Second, func() { _ = cmd.Process.Kill() })
		}
		_ = cmd.Wait()
	}

	// Only write JSON request for non-run modes.
	if !strings.Contains(strings.Join(a.args, " "), "run") {
		if err := a.writeRequest(stdin, message); err != nil {
			kill()
			cancel()
			return nil, err
		}
	} else {
		_ = stdin.Close()
	}

	eventCh := make(chan cobot.Event, 16)
	go func() {
		defer close(eventCh)
		defer cancel()
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}
			isText, text, isDone, isErr, err := parseLine(line)
			if isErr {
				kill()
				select {
				case eventCh <- cobot.Event{Type: cobot.EventError, Error: err.Error()}:
				case <-ctx.Done():
				}
				return
			}
			if isDone {
				_ = cmd.Wait()
				select {
				case eventCh <- cobot.Event{Type: cobot.EventDone, Done: true}:
				case <-ctx.Done():
				}
				return
			}
			if isText {
				select {
				case eventCh <- cobot.Event{Type: cobot.EventText, Content: text}:
				case <-ctx.Done():
					kill()
					return
				}
			}
		}
		if err := scanner.Err(); err != nil {
			_ = cmd.Wait()
			return
		}
		_ = cmd.Wait()
		if ctx.Err() != nil {
			return
		}
		select {
		case eventCh <- cobot.Event{Type: cobot.EventDone, Done: true}:
		default:
		}
	}()

	return eventCh, nil
}

type acpEvent struct {
	Event   string `json:"event"`
	Content string `json:"content,omitempty"`
	Message string `json:"message,omitempty"`
}
