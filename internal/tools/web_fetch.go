package tools

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/cobot-agent/cobot/internal/sandbox"
	cobot "github.com/cobot-agent/cobot/pkg"
)

//go:embed schemas/embed_web_fetch_params.json
var webFetchParamsJSON []byte

const (
	defaultWebFetchMaxLength = 5000
	maxWebFetchMaxLength     = 20000
	webFetchBodyLimitBytes   = 4 * 1024 * 1024
	webFetchTimeout          = 15 * time.Second
	webFetchMaxRedirects     = 5
	webFetchUserAgent        = "cobot-web-fetch/1.0 (+https://github.com/cobot-agent/cobot)"
)

type webFetchArgs struct {
	URL        string `json:"url"`
	MaxLength  int    `json:"max_length,omitempty"`
	StartIndex int    `json:"start_index,omitempty"`
	Raw        bool   `json:"raw,omitempty"`
}

type WebFetchTool struct {
	BasicTool
	validateTarget func(context.Context, *url.URL) error
	customDialer   *net.Dialer
}

func NewWebFetchTool(sb *sandbox.Sandbox) *WebFetchTool {
	return &WebFetchTool{
		BasicTool: BasicTool{
			sandboxTool: sandboxTool{sandbox: sb},
			name:        "web_fetch",
			desc:        "Fetch a static web page over HTTP(S), return markdown by default, or raw HTML when raw=true.",
			params:      webFetchParamsJSON,
		},
		validateTarget: validateWebFetchTarget,
	}
}

func (t *WebFetchTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a webFetchArgs
	if err := decodeArgs(args, &a); err != nil {
		return "", err
	}

	a.URL = strings.TrimSpace(a.URL)
	if a.URL == "" {
		return "", fmt.Errorf("url is required")
	}
	if a.MaxLength < 0 {
		return "", fmt.Errorf("max_length must be >= 0")
	}
	if a.MaxLength == 0 {
		a.MaxLength = defaultWebFetchMaxLength
	}
	if a.MaxLength > maxWebFetchMaxLength {
		return "", fmt.Errorf("max_length must be <= %d", maxWebFetchMaxLength)
	}
	if a.StartIndex < 0 {
		return "", fmt.Errorf("start_index must be >= 0")
	}
	if t.sandbox != nil && !t.sandbox.AllowsNetworkTool("web_fetch") {
		return "", fmt.Errorf("web_fetch network access is blocked by sandbox policy")
	}

	reqCtx, cancel := context.WithTimeout(ctx, webFetchTimeout)
	defer cancel()

	parsedURL, err := url.Parse(a.URL)
	if err != nil {
		return "", fmt.Errorf("invalid url: %w", err)
	}
	validator := t.validateTarget
	if validator == nil {
		validator = validateWebFetchTarget
	}
	if err := validator(reqCtx, parsedURL); err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, a.URL, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", webFetchUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	// Create a custom dialer that validates the resolved IP address
	// to prevent DNS rebinding attacks. Verification happens again
	// right before connecting, after DNS resolution completes.
	var dialer *net.Dialer
	if t.customDialer != nil {
		dialer = t.customDialer
	} else {
		dialer = &net.Dialer{
			Timeout: webFetchTimeout,
			ControlContext: func(ctx context.Context, network, address string, c syscall.RawConn) error {
				// Extract IP from address (format: "IP:port")
				host, _, err := net.SplitHostPort(address)
				if err != nil {
					// If no port, treat entire address as host
					host = address
				}
				ip := net.ParseIP(host)
				if ip == nil {
					return fmt.Errorf("failed to parse resolved IP %q", host)
				}
				if parsedIP, ok := netip.AddrFromSlice(ip); ok {
					if isBlockedFetchAddr(parsedIP) {
						return fmt.Errorf("connection to blocked IP %s (from DNS resolution) blocked by sandbox policy", ip)
					}
				}
				return nil
			},
		}
	}

	client := &http.Client{
		Timeout: webFetchTimeout,
		Transport: &http.Transport{
			DialContext: dialer.DialContext,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= webFetchMaxRedirects {
				return fmt.Errorf("stopped after %d redirects", webFetchMaxRedirects)
			}
			return validator(reqCtx, req.URL)
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("fetch failed with status %d (%s)", resp.StatusCode, resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, webFetchBodyLimitBytes+1))
	if err != nil {
		return "", fmt.Errorf("read response body: %w", err)
	}
	if len(body) > webFetchBodyLimitBytes {
		return "", fmt.Errorf("response body exceeds safe limit (%d bytes)", webFetchBodyLimitBytes)
	}

	content := string(body)
	if !a.Raw {
		content, err = htmltomarkdown.ConvertString(content)
		if err != nil {
			return "", fmt.Errorf("convert html to markdown: %w", err)
		}
	}

	return paginateWebFetchContent(content, a.StartIndex, a.MaxLength), nil
}

func paginateWebFetchContent(content string, startIndex, maxLength int) string {
	runes := []rune(content)
	total := len(runes)

	if startIndex >= total {
		return fmt.Sprintf("no more content (start_index=%d, content_length=%d)", startIndex, total)
	}

	end := startIndex + maxLength
	if end > total {
		end = total
	}

	result := string(runes[startIndex:end])
	if end < total {
		result += fmt.Sprintf("\n\n... (truncated, %d characters remain; set start_index=%d for more)", total-end, end)
	}
	return result
}

func validateWebFetchTarget(ctx context.Context, u *url.URL) error {
	if u == nil {
		return fmt.Errorf("invalid url: empty target")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported url scheme %q: only http and https are supported", u.Scheme)
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" {
		return fmt.Errorf("fetch target must include a host")
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return fmt.Errorf("fetch target host %q is blocked by sandbox policy", u.Hostname())
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if isBlockedFetchAddr(ip) {
			return fmt.Errorf("fetch target host %q is blocked by sandbox policy", u.Hostname())
		}
		return nil
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("resolve fetch target host %q: %w", u.Hostname(), err)
	}
	for _, addr := range addrs {
		if ip, ok := netip.AddrFromSlice(addr.IP); ok && isBlockedFetchAddr(ip) {
			return fmt.Errorf("fetch target host %q is blocked by sandbox policy", u.Hostname())
		}
	}
	return nil
}

func isBlockedFetchAddr(ip netip.Addr) bool {
	if !ip.IsValid() {
		return true
	}
	if ip.IsLoopback() || ip.IsMulticast() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	if ip.Is4() {
		v4 := ip.As4()
		switch {
		case v4[0] == 10:
			return true
		case v4[0] == 127:
			return true
		case v4[0] == 169 && v4[1] == 254:
			return true
		case v4[0] == 172 && v4[1] >= 16 && v4[1] <= 31:
			return true
		case v4[0] == 192 && v4[1] == 168:
			return true
		case v4[0] == 0:
			return true
		}
		return false
	}
	if ip.Is6() {
		prefixes := []netip.Prefix{
			netip.MustParsePrefix("fc00::/7"),
			netip.MustParsePrefix("fe80::/10"),
		}
		for _, p := range prefixes {
			if p.Contains(ip) {
				return true
			}
		}
	}
	return false
}

var _ cobot.Tool = (*WebFetchTool)(nil)
