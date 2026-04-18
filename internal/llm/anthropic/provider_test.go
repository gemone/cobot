package anthropic

import (
	"testing"

	cobot "github.com/cobot-agent/cobot/pkg"
)

func TestNewProvider(t *testing.T) {
	p := NewProvider("sk-test", "", nil)
	if p.Name() != ProviderName {
		t.Errorf("expected %s, got %s", ProviderName, p.Name())
	}
}

func TestNewProviderCustomBaseURL(t *testing.T) {
	p := NewProvider("key", "https://custom.api.com/", nil)
	if p.cfg.BaseURL != "https://custom.api.com" {
		t.Errorf("expected trimmed URL, got %s", p.cfg.BaseURL)
	}
}

func TestBuildRequest(t *testing.T) {
	p := NewProvider("key", "", nil)
	req := &cobot.ProviderRequest{
		Model: "claude-3-sonnet",
		Messages: []cobot.Message{
			{Role: cobot.RoleSystem, Content: "You are helpful."},
			{Role: cobot.RoleUser, Content: "Hello"},
		},
	}
	body := p.buildRequest(req, false)
	if body.Model != "claude-3-sonnet" {
		t.Errorf("expected claude-3-sonnet, got %s", body.Model)
	}
	if body.System != "You are helpful." {
		t.Errorf("expected system prompt, got %s", body.System)
	}
	if len(body.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(body.Messages))
	}
	if body.Stream {
		t.Error("expected stream false")
	}
	if body.MaxTokens != 4096 {
		t.Errorf("expected 4096 max tokens, got %d", body.MaxTokens)
	}
}

func TestBuildRequestModelPrefix(t *testing.T) {
	p := NewProvider("key", "", nil)
	req := &cobot.ProviderRequest{Model: "anthropic:claude-3-opus"}
	body := p.buildRequest(req, false)
	if body.Model != "claude-3-opus" {
		t.Errorf("expected prefix stripped, got %s", body.Model)
	}
}
