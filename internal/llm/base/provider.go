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
		return nil, fmt.Errorf("%s: API error %d: %s", cfg.Name, resp.StatusCode, string(data))
	}

	slog.Debug("http response", "provider", cfg.Name, "status", resp.StatusCode, "elapsed", elapsed.Round(time.Millisecond))
	return resp.Body, nil
}

// DefaultTransport is based on http.DefaultTransport (preserving proxy,
// HTTP/2, and connection pool settings) with overridden timeout values.
var DefaultTransport = func() *http.Transport {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		base = &http.Transport{}
	}
	t := base.Clone()
	t.DialContext = (&net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext
	t.TLSHandshakeTimeout = 10 * time.Second
	t.ResponseHeaderTimeout = 5 * time.Minute
	return t
}()

// NewHTTPClient returns a client with the shared transport.
func NewHTTPClient() *http.Client {
	return &http.Client{Transport: DefaultTransport}
}
