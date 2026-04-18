// Package base provides shared infrastructure for LLM provider implementations.
package base

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/cobot-agent/cobot/internal/debuglog"
)

// ProviderConfig holds the common fields every LLM HTTP provider needs.
type ProviderConfig struct {
	Name    string // provider name for logging and errors (e.g. "openai", "anthropic")
	APIKey  string
	BaseURL string // trimmed, no trailing slash
}

// PrepareBaseURL trims trailing slashes and returns the default if empty.
func PrepareBaseURL(raw, defaultURL string) string {
	u := strings.TrimRight(raw, "/")
	if u == "" {
		return defaultURL
	}
	return u
}

// DoRequest sends a JSON POST to path and returns the response body on success.
// extraHeaders are applied after Content-Type and the provider-specific auth header.
func DoRequest(client *http.Client, cfg ProviderConfig, ctx context.Context, path string, body any, extraHeaders map[string]string) (io.ReadCloser, error) {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("%s: marshal request: %w", cfg.Name, err)
	}

	url := cfg.BaseURL + path
	slog.Debug("http request", "provider", cfg.Name, "method", "POST", "url", url, "body_bytes", len(jsonBody))
	debuglog.LogRequest(ctx, cfg.Name, url, jsonBody)

	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("%s: create request: %w", cfg.Name, err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: request failed: %w", cfg.Name, err)
	}

	elapsed := time.Since(start)

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		slog.Debug("http response", "provider", cfg.Name, "status", resp.StatusCode, "body_bytes", len(data), "elapsed", elapsed.Round(time.Millisecond))
		debuglog.LogResponse(ctx, cfg.Name, resp.StatusCode, data, elapsed)
		return nil, fmt.Errorf("%s: API error %d: %s", cfg.Name, resp.StatusCode, string(data))
	}

	slog.Debug("http response", "provider", cfg.Name, "status", resp.StatusCode, "elapsed", elapsed.Round(time.Millisecond))

	ct := resp.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "text/event-stream") {
		return resp.Body, nil
	}
	return newLoggingReadCloser(ctx, cfg.Name, resp.Body, start), nil
}

// NewTransport creates an http.Transport based on http.DefaultTransport.
// nil timeout disables ResponseHeaderTimeout; non-nil sets it.
func NewTransport(timeout *time.Duration) *http.Transport {
	b, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		b = &http.Transport{}
	}
	t := b.Clone()
	t.DialContext = (&net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext
	t.TLSHandshakeTimeout = 10 * time.Second
	if timeout != nil {
		t.ResponseHeaderTimeout = *timeout
	} else {
		t.ResponseHeaderTimeout = 0
	}
	return t
}

// NewHTTPClient returns a client with no response-header timeout.
func NewHTTPClient() *http.Client {
	return &http.Client{Transport: NewTransport(nil)}
}

// NewHTTPClientWithTimeout returns a client with the given response-header timeout.
func NewHTTPClientWithTimeout(timeout *time.Duration) *http.Client {
	return &http.Client{Transport: NewTransport(timeout)}
}

type loggingReadCloser struct {
	ctx      context.Context
	provider string
	inner    io.ReadCloser
	buf      bytes.Buffer
	start    time.Time
}

func newLoggingReadCloser(ctx context.Context, provider string, rc io.ReadCloser, start time.Time) io.ReadCloser {
	if !debuglog.Enabled() {
		return rc
	}
	return &loggingReadCloser{ctx: ctx, provider: provider, inner: rc, start: start}
}

func (l *loggingReadCloser) Read(p []byte) (int, error) {
	n, err := l.inner.Read(p)
	if n > 0 {
		l.buf.Write(p[:n])
	}
	return n, err
}

func (l *loggingReadCloser) Close() error {
	err := l.inner.Close()
	if l.buf.Len() > 0 {
		debuglog.LogResponse(l.ctx, l.provider, http.StatusOK, l.buf.Bytes(), time.Since(l.start))
	}
	return err
}
