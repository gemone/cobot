package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewACPSubAgent_Defaults(t *testing.T) {
	a := NewACPSubAgent("", nil, "", 0)
	if a.command != "opencode" {
		t.Errorf("command = %q, want opencode", a.command)
	}
	if a.timeout != 10*time.Minute {
		t.Errorf("timeout = %v, want 10m", a.timeout)
	}
}

func TestNewACPSubAgent_CustomValues(t *testing.T) {
	a := NewACPSubAgent("myagent", []string{"--foo"}, "/tmp", 5*time.Minute)
	if a.command != "myagent" {
		t.Errorf("command = %q, want myagent", a.command)
	}
	if len(a.args) != 1 || a.args[0] != "--foo" {
		t.Errorf("args = %v, want [--foo]", a.args)
	}
	if a.workdir != "/tmp" {
		t.Errorf("workdir = %q, want /tmp", a.workdir)
	}
	if a.timeout != 5*time.Minute {
		t.Errorf("timeout = %v, want 5m", a.timeout)
	}
}

func TestACPSubAgent_SetModelAndSystemPrompt(t *testing.T) {
	a := NewACPSubAgent("opencode", nil, "", 0)
	if err := a.SetModel("openai:gpt-4o"); err != nil {
		t.Fatalf("SetModel error: %v", err)
	}
	if a.model != "openai:gpt-4o" {
		t.Errorf("model = %q, want openai:gpt-4o", a.model)
	}
	if err := a.SetSystemPrompt("be concise"); err != nil {
		t.Fatalf("SetSystemPrompt error: %v", err)
	}
	if a.systemPrompt != "be concise" {
		t.Errorf("systemPrompt = %q, want be concise", a.systemPrompt)
	}
}

// startMockACPServer starts a mock ACP HTTP server and returns (server, scriptPath).
// The script, when executed, prints the server URL to stdout and then sleeps.
// Caller must call server.Close() and os.Remove(scriptPath).
func startMockACPServer(handler http.HandlerFunc) (*http.Server, string, error) {
	// Listen on random port
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/rpc", handler)
	server := &http.Server{Handler: mux}
	go server.Serve(ln)

	port := ln.Addr().(*net.TCPAddr).Port
	url := fmt.Sprintf("http://127.0.0.1:%d", port)

	// Create a shell script that prints the URL to stdout (simulating server startup output)
	script := fmt.Sprintf(`#!/bin/sh
echo '%s'
sleep 60
`, url)

	f, err := os.CreateTemp("", "acp_mock_*.sh")
	if err != nil {
		server.Close()
		return nil, "", err
	}
	if _, err := f.WriteString(script); err != nil {
		f.Close()
		os.Remove(f.Name())
		server.Close()
		return nil, "", err
	}
	f.Close()
	if err := os.Chmod(f.Name(), 0755); err != nil {
		os.Remove(f.Name())
		server.Close()
		return nil, "", err
	}

	return server, f.Name(), nil
}

func TestACPSubAgent_Prompt_EchoDone(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}

	var requestID int64
	handler := func(w http.ResponseWriter, r *http.Request) {
		var req jsonRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		atomic.AddInt64(&requestID, 1)

		result := rpcResult{
			Content:    "hello",
			StopReason: "end_turn",
		}
		resp := jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
		}
		resp.Result, _ = json.Marshal(result)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}

	server, script, err := startMockACPServer(handler)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	defer os.Remove(script)

	a := NewACPSubAgent("/bin/sh", []string{script}, "", 5*time.Second)
	defer a.Close()
	if err := a.SetSystemPrompt("sys"); err != nil {
		t.Fatal(err)
	}

	resp, err := a.Prompt(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Prompt error: %v", err)
	}
	if resp.Content != "hello" {
		t.Errorf("Content = %q, want hello", resp.Content)
	}
}

func TestACPSubAgent_Prompt_ErrorEvent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}

	handler := func(w http.ResponseWriter, r *http.Request) {
		resp := jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      1,
			Error:   &jsonRPCError{Code: -32000, Message: "boom"},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}

	server, script, err := startMockACPServer(handler)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	defer os.Remove(script)

	a := NewACPSubAgent("/bin/sh", []string{script}, "", 5*time.Second)
	defer a.Close()
	_, err = a.Prompt(context.Background(), "hi")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error = %q, want it to contain boom", err.Error())
	}
}

func TestACPSubAgent_Prompt_Timeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}

	handler := func(w http.ResponseWriter, r *http.Request) {
		// Block until timeout
		time.Sleep(30 * time.Second)
	}

	server, script, err := startMockACPServer(handler)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	defer os.Remove(script)

	a := NewACPSubAgent("/bin/sh", []string{script}, "", 200*time.Millisecond)
	defer a.Close()
	_, err = a.Prompt(context.Background(), "hi")
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestACPSubAgent_Stream_EchoDone(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}

	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)

		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("response writer does not support flushing")
			return
		}

		// Send text notifications via SSE
		for _, content := range []string{"chunk1", "chunk2"} {
			params := rpcNotifyParams{Type: "text", Content: content}
			paramsJSON, _ := json.Marshal(params)
			evt := sseEvent{JSONRPC: "2.0", Method: "notify", Params: paramsJSON}
			evtJSON, _ := json.Marshal(evt)
			fmt.Fprintf(w, "data: %s\n\n", evtJSON)
			flusher.Flush()
		}

		// Send final result
		result := rpcResult{Content: "", StopReason: "end_turn"}
		resultJSON, _ := json.Marshal(result)
		evt := sseEvent{JSONRPC: "2.0", ID: 1, Result: resultJSON}
		evtJSON, _ := json.Marshal(evt)
		fmt.Fprintf(w, "data: %s\n\n", evtJSON)
		flusher.Flush()
	}

	server, script, err := startMockACPServer(handler)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	defer os.Remove(script)

	a := NewACPSubAgent("/bin/sh", []string{script}, "", 5*time.Second)
	defer a.Close()
	ch, err := a.Stream(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Stream error: %v", err)
	}

	var texts []string
	var done bool
	for evt := range ch {
		switch evt.Type {
		case "text":
			texts = append(texts, evt.Content)
		case "done":
			done = true
		}
	}

	if !done {
		t.Error("expected done event")
	}
	if len(texts) != 2 || texts[0] != "chunk1" || texts[1] != "chunk2" {
		t.Errorf("texts = %v, want [chunk1 chunk2]", texts)
	}
}

func TestACPSubAgent_Stream_ErrorEvent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}

	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)

		flusher, ok := w.(http.Flusher)
		if !ok {
			return
		}

		params := rpcNotifyParams{Type: "error", Content: "stream boom"}
		paramsJSON, _ := json.Marshal(params)
		evt := sseEvent{JSONRPC: "2.0", Method: "notify", Params: paramsJSON}
		evtJSON, _ := json.Marshal(evt)
		fmt.Fprintf(w, "data: %s\n\n", evtJSON)
		flusher.Flush()
	}

	server, script, err := startMockACPServer(handler)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	defer os.Remove(script)

	a := NewACPSubAgent("/bin/sh", []string{script}, "", 5*time.Second)
	defer a.Close()
	ch, err := a.Stream(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Stream error: %v", err)
	}

	var errEvt bool
	for evt := range ch {
		if evt.Type == "error" {
			errEvt = true
			if evt.Error != "stream boom" {
				t.Errorf("Error = %q, want stream boom", evt.Error)
			}
		}
	}
	if !errEvt {
		t.Error("expected error event")
	}
}

func TestACPSubAgent_Stream_ContextCancel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}

	// Server sends one chunk then blocks
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)

		flusher, ok := w.(http.Flusher)
		if !ok {
			return
		}

		params := rpcNotifyParams{Type: "text", Content: "first"}
		paramsJSON, _ := json.Marshal(params)
		evt := sseEvent{JSONRPC: "2.0", Method: "notify", Params: paramsJSON}
		evtJSON, _ := json.Marshal(evt)
		fmt.Fprintf(w, "data: %s\n\n", evtJSON)
		flusher.Flush()

		// Block until client disconnects
		<-r.Context().Done()
	}

	server, script, err := startMockACPServer(handler)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	defer os.Remove(script)

	a := NewACPSubAgent("/bin/sh", []string{script}, "", 10*time.Minute)
	defer a.Close()

	// First start the server with a generous context
	startCtx, startCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := a.start(startCtx); err != nil {
		startCancel()
		t.Fatalf("start error: %v", err)
	}
	startCancel()

	// Now stream with a short-lived context to test cancellation
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	ch, err := a.Stream(ctx, "hi")
	if err != nil {
		t.Fatalf("Stream error: %v", err)
	}

	done := make(chan struct{})
	go func() {
		for range ch {
		}
		close(done)
	}()

	select {
	case <-done:
		// expected
	case <-time.After(5 * time.Second):
		t.Fatal("Stream did not close after context cancellation")
	}
}

func TestACPSubAgent_Integration_OpenCode(t *testing.T) {
	if os.Getenv("COBOT_ACP_INTEGRATION") == "" {
		t.Skip("skipping integration test; set COBOT_ACP_INTEGRATION=1 to run")
	}
	if _, err := exec.LookPath("opencode"); err != nil {
		t.Skip("opencode not installed, skipping integration test")
	}

	acp := NewACPSubAgent("opencode", []string{"acp", "--port", "0"}, "", 30*time.Second)
	defer acp.Close()
	ctx := context.Background()
	resp, err := acp.Prompt(ctx, "Respond with exactly: ACP_INTEGRATION_OK")
	if err != nil {
		t.Fatalf("prompt failed: %v", err)
	}
	if !strings.Contains(resp.Content, "ACP_INTEGRATION_OK") {
		t.Fatalf("expected response to contain ACP_INTEGRATION_OK, got: %q", resp.Content)
	}
	t.Logf("opencode integration response: %q", resp.Content)
}

func TestExtractURL(t *testing.T) {
	tests := []struct {
		line string
		want string
	}{
		{"http://127.0.0.1:12345", "http://127.0.0.1:12345"},
		{"ACP server listening on :8080", "http://127.0.0.1:8080"},
		{"listening on 9090", "http://127.0.0.1:9090"},
		{`{"url":"http://127.0.0.1:3000"}`, "http://127.0.0.1:3000"},
		{`{"port":4567}`, "http://127.0.0.1:4567"},
		{"no url here", ""},
	}

	for _, tt := range tests {
		got := extractURL(tt.line)
		if got != tt.want {
			t.Errorf("extractURL(%q) = %q, want %q", tt.line, got, tt.want)
		}
	}
}

func TestACPSubAgent_Prompt_PreservesToolCallsAndUsage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}

	handler := func(w http.ResponseWriter, r *http.Request) {
		var req jsonRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}

		result := map[string]any{
			"content":     "used tool",
			"stop_reason": "end_turn",
			"tool_calls": []map[string]any{
				{"id": "tc1", "name": "read", "arguments": map[string]any{"path": "/tmp"}},
			},
			"usage": map[string]any{
				"prompt_tokens":     100,
				"completion_tokens": 50,
				"total_tokens":      150,
			},
		}
		resultJSON, _ := json.Marshal(result)
		resp := jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
		}
		resp.Result = resultJSON
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}

	server, script, err := startMockACPServer(handler)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	defer os.Remove(script)

	a := NewACPSubAgent("/bin/sh", []string{script}, "", 5*time.Second)
	defer a.Close()

	resp, err := a.Prompt(context.Background(), "read /tmp")
	if err != nil {
		t.Fatalf("Prompt error: %v", err)
	}
	if resp.Content != "used tool" {
		t.Errorf("Content = %q, want used tool", resp.Content)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "read" {
		t.Errorf("ToolCalls = %v, want 1 call named 'read'", resp.ToolCalls)
	}
	if resp.Usage.TotalTokens != 150 {
		t.Errorf("Usage.TotalTokens = %d, want 150", resp.Usage.TotalTokens)
	}
}

func TestACPSubAgent_ReusesServer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}

	callCount := int32(0)
	handler := func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		result := rpcResult{Content: "ok", StopReason: "end_turn"}
		resultJSON, _ := json.Marshal(result)
		resp := jsonRPCResponse{JSONRPC: "2.0", ID: 1}
		resp.Result = resultJSON
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}

	server, script, err := startMockACPServer(handler)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	defer os.Remove(script)

	a := NewACPSubAgent("/bin/sh", []string{script}, "", 5*time.Second)
	defer a.Close()

	// First call starts the server
	_, err = a.Prompt(context.Background(), "first")
	if err != nil {
		t.Fatalf("First prompt error: %v", err)
	}

	// Second call should reuse the same server
	_, err = a.Prompt(context.Background(), "second")
	if err != nil {
		t.Fatalf("Second prompt error: %v", err)
	}

	if atomic.LoadInt32(&callCount) != 2 {
		t.Errorf("callCount = %d, want 2 (server reused)", callCount)
	}
}

func TestACPSubAgent_Stream_ReadsFromCorrectEndpoint(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}

	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
			http.Error(w, "method not allowed", 405)
			return
		}
		if r.URL.Path != "/rpc" {
			t.Errorf("expected /rpc endpoint, got %s", r.URL.Path)
			http.Error(w, "not found", 404)
			return
		}

		// Verify stream=true in params
		buf := new(bytes.Buffer)
		buf.ReadFrom(r.Body)
		body := buf.Bytes()
		var req map[string]any
		json.Unmarshal(body, &req)
		params, _ := req["params"].(map[string]any)
		if stream, ok := params["stream"].(bool); !ok || !stream {
			t.Errorf("expected stream=true in params, got %v", params["stream"])
		}

		w.Header().Set("Content-Type", "text/event-stream")
		result := rpcResult{Content: "", StopReason: "end_turn"}
		resultJSON, _ := json.Marshal(result)
		evt := sseEvent{JSONRPC: "2.0", ID: 1, Result: resultJSON}
		evtJSON, _ := json.Marshal(evt)
		fmt.Fprintf(w, "data: %s\n\n", evtJSON)
		w.(http.Flusher).Flush()
	}

	server, script, err := startMockACPServer(handler)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	defer os.Remove(script)

	a := NewACPSubAgent("/bin/sh", []string{script}, "", 5*time.Second)
	defer a.Close()

	ch, err := a.Stream(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Stream error: %v", err)
	}
	for range ch {
	}
}

func TestACPSubAgent_Prompt_SendsModelAndSystemPrompt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}

	handler := func(w http.ResponseWriter, r *http.Request) {
		var req jsonRPCRequest
		json.NewDecoder(r.Body).Decode(&req)

		// Verify params
		paramsBytes, _ := json.Marshal(req.Params)
		var params map[string]any
		json.Unmarshal(paramsBytes, &params)

		if sp, ok := params["system_prompt"].(string); !ok || sp != "test system" {
			t.Errorf("system_prompt = %v, want 'test system'", params["system_prompt"])
		}
		if m, ok := params["model"].(string); !ok || m != "test-model" {
			t.Errorf("model = %v, want 'test-model'", params["model"])
		}

		result := rpcResult{Content: "ok", StopReason: "end_turn"}
		resultJSON, _ := json.Marshal(result)
		resp := jsonRPCResponse{JSONRPC: "2.0", ID: req.ID}
		resp.Result = resultJSON
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}

	server, script, err := startMockACPServer(handler)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	defer os.Remove(script)

	a := NewACPSubAgent("/bin/sh", []string{script}, "", 5*time.Second)
	defer a.Close()
	a.SetSystemPrompt("test system")
	a.SetModel("test-model")

	resp, err := a.Prompt(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Prompt error: %v", err)
	}
	if resp.Content != "ok" {
		t.Errorf("Content = %q, want ok", resp.Content)
	}
}

func TestACPSubAgent_ServerStartupTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}

	// Script that never prints a URL
	script := `#!/bin/sh
echo "no url here"
sleep 60
`
	f, err := os.CreateTemp("", "acp_mock_*.sh")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.WriteString(script)
	f.Close()
	os.Chmod(f.Name(), 0755)

	a := NewACPSubAgent("/bin/sh", []string{f.Name()}, "", 5*time.Second)
	defer a.Close()
	// Use a short context to avoid waiting the full 5-second URL discovery timeout
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()

	_, err = a.Prompt(ctx, "hi")
	if err == nil {
		t.Fatal("expected error for missing server URL")
	}
	if !strings.Contains(err.Error(), "timed out") && !strings.Contains(err.Error(), "ACP server URL") {
		t.Errorf("error = %q, want error about timeout or URL", err.Error())
	}
}
