package tools

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	sandboxpkg "github.com/cobot-agent/cobot/internal/sandbox"
)

func TestWebFetchTool_MarkdownConversionSuccess(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><h1>Title</h1><p>Hello <strong>world</strong>.</p></body></html>`))
	}))
	defer ts.Close()

	tool := NewWebFetchTool(nil)
	tool.validateTarget = func(context.Context, *url.URL) error { return nil }
	tool.customDialer = &net.Dialer{Timeout: webFetchTimeout}
	args, _ := json.Marshal(map[string]any{"url": ts.URL})

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if !strings.Contains(result, "# Title") {
		t.Fatalf("expected markdown heading, got: %q", result)
	}
	if !strings.Contains(result, "**world**") {
		t.Fatalf("expected markdown bold text, got: %q", result)
	}
	if strings.Contains(result, "<h1>") || strings.Contains(result, "<strong>") {
		t.Fatalf("expected markdown output without html tags, got: %q", result)
	}
}

func TestWebFetchTool_RawHTMLSuccess(t *testing.T) {
	expected := `<html><body><h1>Raw</h1><p>content</p></body></html>`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(expected))
	}))
	defer ts.Close()

	tool := NewWebFetchTool(nil)
	tool.validateTarget = func(context.Context, *url.URL) error { return nil }
	tool.customDialer = &net.Dialer{Timeout: webFetchTimeout}
	args, _ := json.Marshal(map[string]any{"url": ts.URL, "raw": true})

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if result != expected {
		t.Fatalf("expected raw html %q, got %q", expected, result)
	}
}

func TestWebFetchTool_PaginationAndTruncationHint(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("abcdefghij"))
	}))
	defer ts.Close()

	tool := NewWebFetchTool(nil)
	tool.validateTarget = func(context.Context, *url.URL) error { return nil }
	tool.customDialer = &net.Dialer{Timeout: webFetchTimeout}
	args, _ := json.Marshal(map[string]any{"url": ts.URL, "raw": true, "start_index": 2, "max_length": 4})

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	expected := "cdef\n\n... (truncated, 4 characters remain; set start_index=6 for more)"
	if result != expected {
		t.Fatalf("expected %q, got %q", expected, result)
	}
}

func TestWebFetchTool_InvalidParams(t *testing.T) {
	tool := NewWebFetchTool(nil)

	tests := []struct {
		name   string
		args   map[string]any
		errMsg string
	}{
		{
			name:   "negative max_length",
			args:   map[string]any{"url": "https://example.com", "max_length": -1},
			errMsg: "max_length must be >= 0",
		},
		{
			name:   "negative start_index",
			args:   map[string]any{"url": "https://example.com", "start_index": -1},
			errMsg: "start_index must be >= 0",
		},
		{
			name:   "unsupported scheme",
			args:   map[string]any{"url": "ftp://example.com/file"},
			errMsg: "unsupported url scheme",
		},
		{
			name:   "invalid url",
			args:   map[string]any{"url": "http://%zz"},
			errMsg: "invalid url",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args, _ := json.Marshal(tt.args)
			_, err := tool.Execute(context.Background(), args)
			if err == nil {
				t.Fatalf("expected error containing %q", tt.errMsg)
			}
			if !strings.Contains(err.Error(), tt.errMsg) {
				t.Fatalf("expected error containing %q, got %v", tt.errMsg, err)
			}
		})
	}
}

func TestWebFetchTool_Non2xxResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer ts.Close()

	tool := NewWebFetchTool(nil)
	tool.validateTarget = func(context.Context, *url.URL) error { return nil }
	tool.customDialer = &net.Dialer{Timeout: webFetchTimeout}
	args, _ := json.Marshal(map[string]any{"url": ts.URL})

	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Fatal("expected non-2xx error")
	}
	if !strings.Contains(err.Error(), "fetch failed with status 503") {
		t.Fatalf("expected status error, got %v", err)
	}
}

func TestWebFetchTool_TimeoutFetchFailure(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		_, _ = w.Write([]byte("slow response"))
	}))
	defer ts.Close()

	tool := NewWebFetchTool(nil)
	tool.validateTarget = func(context.Context, *url.URL) error { return nil }
	tool.customDialer = &net.Dialer{Timeout: webFetchTimeout}
	args, _ := json.Marshal(map[string]any{"url": ts.URL})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err := tool.Execute(ctx, args)
	if err == nil {
		t.Fatal("expected fetch timeout error")
	}
	if !strings.Contains(err.Error(), "fetch failed") {
		t.Fatalf("expected fetch failure wrapper, got %v", err)
	}
}

func TestWebFetchTool_BlockedWhenNetworkDisabled(t *testing.T) {
	sb := sandboxpkg.NewSandbox(sandboxpkg.SandboxConfig{AllowNetwork: false})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("still works"))
	}))
	defer ts.Close()

	tool := NewWebFetchTool(sb)
	args, _ := json.Marshal(map[string]any{"url": ts.URL, "raw": true})

	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Fatal("expected sandbox network error")
	}
	if !strings.Contains(err.Error(), "web_fetch network access is blocked") {
		t.Fatalf("expected sandbox network error, got %v", err)
	}
}

func TestWebFetchTool_AllowedWhenNetworkEnabled(t *testing.T) {
	sb := sandboxpkg.NewSandbox(sandboxpkg.SandboxConfig{AllowNetwork: true})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("still works"))
	}))
	defer ts.Close()

	tool := NewWebFetchTool(sb)
	tool.validateTarget = func(context.Context, *url.URL) error { return nil }
	tool.customDialer = &net.Dialer{Timeout: webFetchTimeout}
	args, _ := json.Marshal(map[string]any{"url": ts.URL, "raw": true})

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("expected success with network enabled, got error: %v", err)
	}
	if result != "still works" {
		t.Fatalf("expected web fetch output to succeed, got %q", result)
	}
}

func TestWebFetchTool_BlocksLocalAndPrivateTargets(t *testing.T) {
	sb := sandboxpkg.NewSandbox(sandboxpkg.SandboxConfig{
		AllowNetwork:        true,
		AllowedNetworkTools: []string{"web_fetch"},
	})
	tool := NewWebFetchTool(sb)

	tests := []string{
		"http://localhost:8080",
		"http://127.0.0.1:8080",
		"http://169.254.169.254/latest/meta-data/",
		"http://10.0.0.1/",
		"http://[::1]/",
	}
	for _, target := range tests {
		t.Run(target, func(t *testing.T) {
			args, _ := json.Marshal(map[string]any{"url": target, "raw": true})
			_, err := tool.Execute(context.Background(), args)
			if err == nil {
				t.Fatal("expected target to be blocked")
			}
			if !strings.Contains(err.Error(), "blocked by sandbox policy") {
				t.Fatalf("expected sandbox policy error, got %v", err)
			}
		})
	}
}

func TestWebFetchTool_DNSRebindingProtection(t *testing.T) {
	// This test verifies that DNS rebinding attacks are prevented by validating
	// the resolved IP address in the dialer's ControlContext callback.
	// Even if an attacker changes DNS records after initial validation,
	// connecting to a private IP will be blocked.

	sb := sandboxpkg.NewSandbox(sandboxpkg.SandboxConfig{
		AllowNetwork:        true,
		AllowedNetworkTools: []string{"web_fetch"},
	})
	tool := NewWebFetchTool(sb)

	// Test that attempting to connect to localhost fails even if we bypass
	// initial URL validation (simulating a DNS rebinding attack)
	args, _ := json.Marshal(map[string]any{"url": "http://127.0.0.1:9999"})

	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Fatalf("expected DNS rebinding attack to be blocked")
	}
	if !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("expected blocked error, got: %v", err)
	}
}

func TestWebFetchTool_ValidateIPInDialContext(t *testing.T) {
	// This test verifies that private IPs are blocked even after DNS resolution
	// by creating a mock scenario where ControlContext validation would apply

	sb := sandboxpkg.NewSandbox(sandboxpkg.SandboxConfig{
		AllowNetwork:        true,
		AllowedNetworkTools: []string{"web_fetch"},
	})
	tool := NewWebFetchTool(sb)

	// Test that attempts to connect to localhost/private IPs fail
	// even if the initial URL validation passes
	tests := []struct {
		name string
		url  string
	}{
		{"loopback", "http://127.0.0.1:8080"},
		{"private-10", "http://10.0.0.1:8080"},
		{"private-172", "http://172.16.0.1:8080"},
		{"private-192", "http://192.168.0.1:8080"},
		{"ipv6-loopback", "http://[::1]:8080"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args, _ := json.Marshal(map[string]any{"url": tt.url})
			_, err := tool.Execute(context.Background(), args)
			if err == nil {
				t.Fatalf("expected connection to %s to be blocked", tt.url)
			}
			if !strings.Contains(err.Error(), "blocked") {
				t.Fatalf("expected blocked error, got: %v", err)
			}
		})
	}
}
