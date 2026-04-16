package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	cobot "github.com/cobot-agent/cobot/pkg"
)

// ACPSubAgent implements cobot.SubAgent by launching an external ACP HTTP server
// and communicating via JSON-RPC over HTTP.
type ACPSubAgent struct {
	command      string
	args         []string
	workdir      string
	timeout      time.Duration
	systemPrompt string
	model        string

	// runtime state
	mu     sync.Mutex
	cmd    *exec.Cmd
	baseURL string
	nextID int64
	started bool
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

// SetModel stores the model spec for subsequent requests.
func (a *ACPSubAgent) SetModel(spec string) error {
	a.model = spec
	return nil
}

// SetSystemPrompt stores the system prompt for subsequent requests.
func (a *ACPSubAgent) SetSystemPrompt(prompt string) error {
	a.systemPrompt = prompt
	return nil
}

// nextRequestID returns the next JSON-RPC request ID.
func (a *ACPSubAgent) nextRequestID() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.nextID++
	return a.nextID
}

// start launches the ACP HTTP server subprocess and discovers its URL.
// If already started, it's a no-op.
func (a *ACPSubAgent) start(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.started {
		return nil
	}

	cmdArgs := append([]string{}, a.args...)
	cmd := exec.CommandContext(ctx, a.command, cmdArgs...)
	if a.workdir != "" {
		cmd.Dir = a.workdir
	}

	// Capture both stdout and stderr to find the server URL.
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start command %q: %w", a.command, err)
	}

	a.cmd = cmd

	// Read stdout and stderr concurrently to find the server URL.
	// We give up after 5 seconds.
	urlCh := make(chan string, 1)

	// scanAndDrain scans lines from a reader looking for a URL, then drains the rest.
	scanAndDrain := func(r io.Reader) {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := scanner.Text()
			if u := extractURL(line); u != "" {
				select {
				case urlCh <- u:
				default:
				}
				// Don't return — keep draining so the subprocess doesn't block on writes.
			}
		}
	}

	go scanAndDrain(stdout)
	go scanAndDrain(stderr)

	select {
	case url := <-urlCh:
		a.baseURL = url
		a.started = true
		return nil
	case <-time.After(5 * time.Second):
		_ = a.killLocked()
		return fmt.Errorf("timed out waiting for ACP server URL from %q (check that the server prints its URL on startup)", a.command)
	case <-ctx.Done():
		_ = a.killLocked()
		return fmt.Errorf("context cancelled while waiting for ACP server: %w", ctx.Err())
	}
}

// killLocked kills the subprocess. Must be called with a.mu held.
func (a *ACPSubAgent) killLocked() error {
	if a.cmd != nil && a.cmd.Process != nil {
		_ = a.cmd.Process.Signal(os.Interrupt)
		done := make(chan error, 1)
		go func() {
			done <- a.cmd.Wait()
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			_ = a.cmd.Process.Kill()
			<-done
		}
	}
	a.started = false
	a.baseURL = ""
	a.cmd = nil
	return nil
}

// Close terminates the ACP server subprocess.
func (a *ACPSubAgent) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.killLocked()
}

// --- URL discovery helpers ---

var (
	urlPattern       = regexp.MustCompile(`https?://127\.0\.0\.1:\d+`)
	listeningPattern = regexp.MustCompile(`listening\s+on\s+:?(\d+)`)
)

// extractURL tries to extract a server URL from a line of output.
func extractURL(line string) string {
	// Try direct URL match (e.g., "http://127.0.0.1:12345")
	if u := urlPattern.FindString(line); u != "" {
		return u
	}

	// Try "listening on :PORT" or "listening on PORT"
	if m := listeningPattern.FindStringSubmatch(line); len(m) >= 2 {
		port := m[1]
		return "http://127.0.0.1:" + port
	}

	// Try JSON with "url" or "port" field
	var obj map[string]json.RawMessage
	if json.Unmarshal([]byte(line), &obj) == nil {
		if raw, ok := obj["url"]; ok {
			var s string
			if json.Unmarshal(raw, &s) == nil && strings.HasPrefix(s, "http") {
				return s
			}
		}
		if raw, ok := obj["port"]; ok {
			var n json.Number
			if json.Unmarshal(raw, &n) == nil {
				return "http://127.0.0.1:" + string(n)
			}
		}
	}

	// Try "ACP server listening on :PORT"
	for _, part := range strings.Fields(line) {
		if strings.HasPrefix(part, ":") {
			if port, err := strconv.Atoi(part[1:]); err == nil && port > 0 {
				return "http://127.0.0.1:" + strconv.Itoa(port)
			}
		}
	}

	return ""
}

// --- JSON-RPC types ---

type jsonRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"jsonrpc_id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *jsonRPCError) Error() string {
	return fmt.Sprintf("JSON-RPC error %d: %s", e.Code, e.Message)
}

type rpcResult struct {
	Content    string           `json:"content"`
	ToolCalls  []cobot.ToolCall `json:"tool_calls,omitempty"`
	StopReason string           `json:"stop_reason"`
	Usage      *cobot.Usage     `json:"usage,omitempty"`
}

type rpcNotifyParams struct {
	Type     string          `json:"type"`
	Content  string          `json:"content,omitempty"`
	ToolCall *cobot.ToolCall `json:"tool_call,omitempty"`
}

type sseEvent struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method,omitempty"`
	ID      int64           `json:"id,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

// --- HTTP helpers ---

// rpcURL returns the JSON-RPC endpoint URL.
func (a *ACPSubAgent) rpcURL() string {
	return a.baseURL + "/rpc"
}

// sendRPC sends a non-streaming JSON-RPC request and returns the response.
func (a *ACPSubAgent) sendRPC(ctx context.Context, method string, params any) (*jsonRPCResponse, error) {
	reqBody := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      a.nextRequestID(),
		Method:  method,
		Params:  params,
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal rpc request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.rpcURL(), bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create http request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rpc request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("rpc request returned status %d: %s", resp.StatusCode, string(body))
	}

	var rpcResp jsonRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return nil, fmt.Errorf("decode rpc response: %w", err)
	}

	if rpcResp.Error != nil {
		return nil, rpcResp.Error
	}

	return &rpcResp, nil
}

// buildPromptParams builds the JSON-RPC params for a prompt request.
func (a *ACPSubAgent) buildPromptParams(message string) map[string]any {
	params := map[string]any{
		"messages": []map[string]string{
			{"role": "user", "content": message},
		},
	}
	if a.systemPrompt != "" {
		params["system_prompt"] = a.systemPrompt
	}
	if a.model != "" {
		params["model"] = a.model
	}
	return params
}

// Prompt sends a single prompt and returns the collected response.
func (a *ACPSubAgent) Prompt(ctx context.Context, message string) (*cobot.ProviderResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	if err := a.start(ctx); err != nil {
		return nil, fmt.Errorf("start ACP server: %w", err)
	}

	params := a.buildPromptParams(message)
	rpcResp, err := a.sendRPC(ctx, "prompt", params)
	if err != nil {
		return nil, fmt.Errorf("rpc prompt: %w", err)
	}

	var result rpcResult
	if err := json.Unmarshal(rpcResp.Result, &result); err != nil {
		return nil, fmt.Errorf("decode rpc result: %w", err)
	}

	stopReason := cobot.StopEndTurn
	if result.StopReason != "" {
		stopReason = cobot.StopReason(result.StopReason)
	}

	resp := &cobot.ProviderResponse{
		Content:    result.Content,
		ToolCalls:  result.ToolCalls,
		StopReason: stopReason,
	}
	if result.Usage != nil {
		resp.Usage = *result.Usage
	}
	return resp, nil
}

// Stream sends a prompt and returns a channel of streaming events.
func (a *ACPSubAgent) Stream(ctx context.Context, message string) (<-chan cobot.Event, error) {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)

	if err := a.start(ctx); err != nil {
		cancel()
		return nil, fmt.Errorf("start ACP server: %w", err)
	}

	params := a.buildPromptParams(message)
	params["stream"] = true

	reqBody := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      a.nextRequestID(),
		Method:  "prompt",
		Params:  params,
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("marshal rpc request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.rpcURL(), bytes.NewReader(bodyBytes))
	if err != nil {
		cancel()
		return nil, fmt.Errorf("create http request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("rpc stream request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		cancel()
		return nil, fmt.Errorf("rpc stream request returned status %d: %s", resp.StatusCode, string(body))
	}

	eventCh := make(chan cobot.Event, 16)
	go func() {
		defer close(eventCh)
		defer cancel()
		defer resp.Body.Close()

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()

			// SSE lines start with "data: "
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "" {
				continue
			}

			var evt sseEvent
			if err := json.Unmarshal([]byte(data), &evt); err != nil {
				// Skip malformed events
				continue
			}

			// Check for JSON-RPC error
			if evt.Error != nil {
				select {
				case eventCh <- cobot.Event{Type: cobot.EventError, Error: evt.Error.Error()}:
				case <-ctx.Done():
				}
				return
			}

			// Final result — stream is done
			if evt.Result != nil {
				var result rpcResult
				if err := json.Unmarshal(evt.Result, &result); err == nil && result.Content != "" {
					// If the result has leftover content, emit it
					select {
					case eventCh <- cobot.Event{Type: cobot.EventText, Content: result.Content}:
					case <-ctx.Done():
						return
					}
				}
				select {
				case eventCh <- cobot.Event{Type: cobot.EventDone, Done: true}:
				case <-ctx.Done():
				}
				return
			}

			// Notification event
			if evt.Method == "notify" && evt.Params != nil {
				var params rpcNotifyParams
				if err := json.Unmarshal(evt.Params, &params); err != nil {
					continue
				}

				switch params.Type {
				case "text":
					select {
					case eventCh <- cobot.Event{Type: cobot.EventText, Content: params.Content}:
					case <-ctx.Done():
						return
					}
				case "tool_call":
					select {
					case eventCh <- cobot.Event{Type: cobot.EventToolCall, ToolCall: params.ToolCall}:
					case <-ctx.Done():
						return
					}
				case "tool_start":
					select {
					case eventCh <- cobot.Event{Type: cobot.EventToolStart, ToolCall: params.ToolCall}:
					case <-ctx.Done():
						return
					}
				case "tool_result":
					select {
					case eventCh <- cobot.Event{Type: cobot.EventToolResult, ToolCall: params.ToolCall}:
					case <-ctx.Done():
						return
					}
				case "done":
					select {
					case eventCh <- cobot.Event{Type: cobot.EventDone, Done: true}:
					case <-ctx.Done():
					}
					return
				case "error":
					errMsg := params.Content
					if errMsg == "" {
						errMsg = "unknown stream error"
					}
					select {
					case eventCh <- cobot.Event{Type: cobot.EventError, Error: errMsg}:
					case <-ctx.Done():
					}
					return
				}
			}
		}

		// Scanner finished without explicit done — send done event
		if ctx.Err() == nil {
			select {
			case eventCh <- cobot.Event{Type: cobot.EventDone, Done: true}:
			default:
			}
		}
	}()

	return eventCh, nil
}

// freePort returns a random available TCP port on localhost.
func freePort() (int, error) {
	addr, err := net.ResolveTCPAddr("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
