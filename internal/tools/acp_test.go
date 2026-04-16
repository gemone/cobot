package tools

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
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

func TestACPSubAgent_Prompt_EchoDone(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}

	script := `#!/bin/sh
while read line; do
  echo '{"event":"text","content":"hello"}'
  echo '{"event":"done"}'
  break
done
`
	f, err := os.CreateTemp("", "acp_mock_*.sh")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(script); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := os.Chmod(f.Name(), 0755); err != nil {
		t.Fatal(err)
	}

	a := NewACPSubAgent("/bin/sh", []string{f.Name()}, "", 5*time.Second)
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

	script := `#!/bin/sh
while read line; do
  echo '{"event":"error","message":"boom"}'
  break
done
`
	f, err := os.CreateTemp("", "acp_mock_*.sh")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(script); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := os.Chmod(f.Name(), 0755); err != nil {
		t.Fatal(err)
	}

	a := NewACPSubAgent("/bin/sh", []string{f.Name()}, "", 5*time.Second)
	_, err = a.Prompt(context.Background(), "hi")
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "boom" {
		t.Errorf("error = %q, want boom", err.Error())
	}
}

func TestACPSubAgent_Prompt_Timeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}

	script := `#!/bin/sh
while read line; do
  sleep 5
done
`
	f, err := os.CreateTemp("", "acp_mock_*.sh")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(script); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := os.Chmod(f.Name(), 0755); err != nil {
		t.Fatal(err)
	}

	a := NewACPSubAgent("/bin/sh", []string{f.Name()}, "", 100*time.Millisecond)
	_, err = a.Prompt(context.Background(), "hi")
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestACPSubAgent_Stream_EchoDone(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}

	script := `#!/bin/sh
while read line; do
  echo '{"event":"text","content":"chunk1"}'
  echo '{"event":"text","content":"chunk2"}'
  echo '{"event":"done"}'
  break
done
`
	f, err := os.CreateTemp("", "acp_mock_*.sh")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(script); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := os.Chmod(f.Name(), 0755); err != nil {
		t.Fatal(err)
	}

	a := NewACPSubAgent("/bin/sh", []string{f.Name()}, "", 5*time.Second)
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

	script := `#!/bin/sh
while read line; do
  echo '{"event":"error","message":"stream boom"}'
  break
done
`
	f, err := os.CreateTemp("", "acp_mock_*.sh")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(script); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := os.Chmod(f.Name(), 0755); err != nil {
		t.Fatal(err)
	}

	a := NewACPSubAgent("/bin/sh", []string{f.Name()}, "", 5*time.Second)
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

func TestACPSubAgent_Prompt_FallbackPlainText(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}

	script := `#!/bin/sh
while read line; do
  echo 'plain text line'
  break
done
`
	f, err := os.CreateTemp("", "acp_mock_*.sh")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(script); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := os.Chmod(f.Name(), 0755); err != nil {
		t.Fatal(err)
	}

	a := NewACPSubAgent("/bin/sh", []string{f.Name()}, "", 5*time.Second)
	resp, err := a.Prompt(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Prompt error: %v", err)
	}
	if resp.Content != "plain text line\n" {
		t.Errorf("Content = %q, want 'plain text line\\n'", resp.Content)
	}
}

func TestACPSubAgent_Stream_ContextCancel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}

	script := `#!/bin/sh
while read line; do
  while :; do :; done
  break
done
`
	f, err := os.CreateTemp("", "acp_mock_*.sh")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(script); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := os.Chmod(f.Name(), 0755); err != nil {
		t.Fatal(err)
	}

	a := NewACPSubAgent("/bin/sh", []string{f.Name()}, "", 10*time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
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
	case <-time.After(3 * time.Second):
		t.Fatal("Stream did not close after context cancellation")
	}
}

func TestACPSubAgent_Integration_OpenCode(t *testing.T) {
	if _, err := exec.LookPath("opencode"); err != nil {
		t.Skip("opencode not installed, skipping integration test")
	}

	acp := NewACPSubAgent("opencode", []string{"run"}, "", 30*time.Second)
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
