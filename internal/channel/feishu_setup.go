package channel

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// FeishuQRConfig holds the result of a successful QR registration.
type FeishuQRConfig struct {
	AppID     string
	AppSecret string
	Domain    string // "feishu" or "lark"
}

var feishuAccountsURLs = map[string]string{
	"feishu": "https://accounts.feishu.cn",
	"lark":   "https://accounts.larksuite.com",
}

var feishuOpenURLs = map[string]string{
	"feishu": "https://open.feishu.cn",
	"lark":   "https://open.larksuite.com",
}

const feishuRegistrationPath = "/oauth/v1/app/registration"

// httpClient is a shared client with a 15-second timeout for all Feishu API calls.
var httpClient = &http.Client{Timeout: 15 * time.Second}

// feishuPOST sends a POST to the Feishu registration endpoint with a timeout.
// It checks StatusCode and includes a snippet of non-2xx responses in the error.
func feishuPOST(base, path string, body url.Values) (map[string]any, error) {
	resp, err := httpClient.PostForm(base+path, body)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return nil, fmt.Errorf("request returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return result, nil
}

// initRegistration verifies the environment supports client_secret auth.
func initRegistration(domain string) error {
	base := feishuAccountsURLs[domain]
	if base == "" {
		base = feishuAccountsURLs["feishu"]
	}

	result, err := feishuPOST(base, feishuRegistrationPath, url.Values{"action": {"init"}})
	if err != nil {
		return fmt.Errorf("init: %w", err)
	}

	methods, _ := result["supported_auth_methods"].([]any)
	for _, m := range methods {
		if m == "client_secret" {
			return nil
		}
	}
	return fmt.Errorf("registration does not support client_secret auth; supported: %v", methods)
}

// beginRegistration starts the device-code flow and returns the QR URL and polling params.
func beginRegistration(domain string) (qrURL, deviceCode string, interval, expireIn int, err error) {
	base := feishuAccountsURLs[domain]
	if base == "" {
		base = feishuAccountsURLs["feishu"]
	}

	body := url.Values{
		"action":            {"begin"},
		"archetype":         {"PersonalAgent"},
		"auth_method":       {"client_secret"},
		"request_user_info": {"open_id"},
	}

	result, err := feishuPOST(base, feishuRegistrationPath, body)
	if err != nil {
		return "", "", 0, 0, fmt.Errorf("begin: %w", err)
	}

	dc, _ := result["device_code"].(string)
	if dc == "" {
		return "", "", 0, 0, fmt.Errorf("begin response missing device_code")
	}
	deviceCode = dc

	uri, _ := result["verification_uri_complete"].(string)
	if uri == "" {
		return "", "", 0, 0, fmt.Errorf("begin response missing verification_uri_complete")
	}
	if strings.Contains(uri, "?") {
		qrURL = uri + "&from=cobot&tp=cobot"
	} else {
		qrURL = uri + "?from=cobot&tp=cobot"
	}

	interval = 5
	if v, ok := result["interval"].(float64); ok {
		interval = int(v)
	}
	expireIn = 600
	if v, ok := result["expire_in"].(float64); ok {
		expireIn = int(v)
	}
	return
}

// pollRegistration polls until the user scans the QR code, times out, or denies access.
func pollRegistration(domain, deviceCode string, interval, expireIn int) (*FeishuQRConfig, error) {
	base := feishuAccountsURLs[domain]
	if base == "" {
		base = feishuAccountsURLs["feishu"]
	}

	deadline := time.Now().Add(time.Duration(expireIn) * time.Second)
	currentDomain := domain

	for time.Now().Before(deadline) {
		body := url.Values{
			"action":      {"poll"},
			"device_code": {deviceCode},
			"tp":          {"ob_app"},
		}

		result, err := feishuPOST(base, feishuRegistrationPath, body)
		if err != nil {
			// Network or non-2xx error — retry after interval.
			time.Sleep(time.Duration(interval) * time.Second)
			continue
		}

		// Auto-detect feishu → lark domain switch.
		if userInfo, ok := result["user_info"].(map[string]any); ok {
			if brand, _ := userInfo["tenant_brand"].(string); brand == "lark" && currentDomain != "lark" {
				currentDomain = "lark"
				base = feishuAccountsURLs["lark"]
			}
		}

		// Success.
		if appID, ok := result["client_id"].(string); ok && appID != "" {
			if appSecret, ok := result["client_secret"].(string); ok && appSecret != "" {
				return &FeishuQRConfig{
					AppID:     appID,
					AppSecret: appSecret,
					Domain:    currentDomain,
				}, nil
			}
		}

		// Terminal errors.
		if errStr, ok := result["error"].(string); ok {
			if errStr == "access_denied" || errStr == "expired_token" {
				return nil, fmt.Errorf("registration %s", errStr)
			}
		}

		time.Sleep(time.Duration(interval) * time.Second)
	}

	return nil, fmt.Errorf("registration timed out after %d seconds", expireIn)
}

// probeBot verifies bot connectivity and returns the bot name.
func probeBot(appID, appSecret, domain string) (botName string, err error) {
	openBase := feishuOpenURLs[domain]
	if openBase == "" {
		openBase = feishuOpenURLs["feishu"]
	}

	result, err := feishuPOST(openBase, "/open-apis/auth/v3/tenant_access_token/internal",
		url.Values{"app_id": {appID}, "app_secret": {appSecret}})
	if err != nil {
		return "", fmt.Errorf("auth request: %w", err)
	}

	tenantToken, _ := result["tenant_access_token"].(string)
	if tenantToken == "" {
		return "", fmt.Errorf("no tenant_access_token")
	}

	// Get bot info.
	req, _ := http.NewRequest("GET", openBase+"/open-apis/bot/v3/info", nil)
	req.Header.Set("Authorization", "Bearer "+tenantToken)

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("bot info request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return "", fmt.Errorf("bot info returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var botResult map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&botResult); err != nil {
		return "", fmt.Errorf("parse bot info: %w", err)
	}

	if code, ok := botResult["code"].(float64); ok && code != 0 {
		return "", fmt.Errorf("bot probe failed with code %d", int(code))
	}

	if data, ok := botResult["data"].(map[string]any); ok {
		if name, ok := data["bot_name"].(string); ok {
			return name, nil
		}
	}
	return "", nil
}

// QRRegister runs the full scan-to-create registration flow.
// domain is "feishu" or "lark". Returns credentials on success.
func QRRegister(domain string) (*FeishuQRConfig, error) {
	if domain == "" {
		domain = "feishu"
	}
	if err := initRegistration(domain); err != nil {
		return nil, fmt.Errorf("init: %w", err)
	}
	qrURL, deviceCode, interval, expireIn, err := beginRegistration(domain)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	_ = qrURL // caller uses qrURL for display
	cfg, err := pollRegistration(domain, deviceCode, interval, expireIn)
	if err != nil {
		return nil, fmt.Errorf("poll: %w", err)
	}
	return cfg, nil
}

// BuildQRPayload starts the flow and returns the QR URL for display.
// Call PollAfterQR after the user scans.
func BuildQRPayload(domain string) (qrURL, deviceCode string, interval, expireIn int, _ error) {
	var err error
	if err = initRegistration(domain); err != nil {
		return "", "", 0, 0, fmt.Errorf("init: %w", err)
	}
	qrURL, deviceCode, interval, expireIn, err = beginRegistration(domain)
	if err != nil {
		return "", "", 0, 0, fmt.Errorf("begin: %w", err)
	}
	return
}

// PollAfterQR polls for the device code result. Call after BuildQRPayload.
func PollAfterQR(domain, deviceCode string, interval, expireIn int) (*FeishuQRConfig, error) {
	return pollRegistration(domain, deviceCode, interval, expireIn)
}

// VerifyManualCredentials probes manually entered app_id/app_secret.
func VerifyManualCredentials(appID, appSecret, domain string) (string, error) {
	return probeBot(appID, appSecret, domain)
}

// SaveFeishuChannelConfig writes a Feishu channel entry to the YAML config file.
// It adds or replaces the feishu channel entry with the given name.
func SaveFeishuChannelConfig(configPath string, cfg *FeishuQRConfig, channelName string) error {
	// Read existing YAML content.
	var existing map[string]any
	if data, err := os.ReadFile(configPath); err == nil && len(data) > 0 {
		if err := yaml.Unmarshal(data, &existing); err != nil {
			existing = make(map[string]any)
		}
	} else {
		existing = make(map[string]any)
	}

	channels, ok := existing["channels"].([]any)
	if !ok {
		channels = []any{}
	}

	newChannel := map[string]any{
		"name": channelName,
		"type": "feishu",
		"config": map[string]any{
			"app_id":     cfg.AppID,
			"app_secret": cfg.AppSecret,
			"domain":     cfg.Domain,
		},
	}

	// Replace existing feishu channel with same name, or append.
	found := false
	for i, ch := range channels {
		if chMap, ok := ch.(map[string]any); ok {
			if chMap["name"] == channelName && chMap["type"] == "feishu" {
				channels[i] = newChannel
				found = true
				break
			}
		}
	}
	if !found {
		channels = append(channels, newChannel)
	}
	existing["channels"] = channels

	data, err := yaml.Marshal(existing)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}
