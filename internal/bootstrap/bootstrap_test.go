package bootstrap

import (
	"context"
	"testing"

	"github.com/cobot-agent/cobot/internal/agent"
	brokersqlite "github.com/cobot-agent/cobot/internal/broker"
	"github.com/cobot-agent/cobot/internal/channel"
	"github.com/cobot-agent/cobot/internal/command"
	"github.com/cobot-agent/cobot/internal/cron"
	"github.com/cobot-agent/cobot/internal/tools"
	cobot "github.com/cobot-agent/cobot/pkg"
)

func TestReplaceSkillsSection(t *testing.T) {
	tests := []struct {
		name       string
		current    string
		newSection string
		want       string
	}{
		{
			name:       "no existing section appends",
			current:    "system prompt",
			newSection: "## Skills (mandatory)\nskill content",
			want:       "system prompt\n\n## Skills (mandatory)\nskill content",
		},
		{
			name:       "replaces section at end",
			current:    "system prompt\n## Skills (mandatory)\nold skills",
			newSection: "## Skills (mandatory)\nnew skills",
			want:       "system prompt\n## Skills (mandatory)\nnew skills",
		},
		{
			name:       "replaces section in middle",
			current:    "system prompt\n## Skills (mandatory)\nold skills\n## Other\nmore",
			newSection: "## Skills (mandatory)\nnew skills",
			want:       "system prompt\n## Skills (mandatory)\nnew skills\n## Other\nmore",
		},
		{
			name:       "replaces section at start",
			current:    "## Skills (mandatory)\nold skills\n## Other\nmore",
			newSection: "## Skills (mandatory)\nnew skills",
			want:       "## Skills (mandatory)\nnew skills\n## Other\nmore",
		},
		{
			name:       "does not match inside code block",
			current:    "system\n```\n## Skills (mandatory)\nfake\n```\n## Other\nmore",
			newSection: "## Skills (mandatory)\nreal",
			want:       "system\n```\n## Skills (mandatory)\nfake\n```\n## Other\nmore\n\n## Skills (mandatory)\nreal",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := replaceSkillsSection(tt.current, tt.newSection)
			if got != tt.want {
				t.Errorf("replaceSkillsSection() =\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}

func TestConfigureGateway_WiresCronSchedulerIntoCommandRegistry(t *testing.T) {
	dir := t.TempDir()
	br, err := brokersqlite.NewSQLiteBroker(dir + "/broker.db")
	if err != nil {
		t.Fatalf("new broker: %v", err)
	}
	defer br.Close()

	store := cron.NewStore(dir + "/cron")
	runStore := cron.NewRunStore(dir + "/runs")
	scheduler := cron.NewScheduler(store, func(_ context.Context, _, _, _ string) (string, error) { return "", nil }, runStore, br, nil)

	a := agent.New(&cobot.Config{}, tools.NewRegistry())
	a.SetCronScheduler(scheduler)
	res := &Result{
		Agent:      a,
		ChannelMgr: channel.NewManager(),
	}

	gw, err := ConfigureGateway(res, cobot.GatewayConfig{Addr: "127.0.0.1:0"}, nil)
	if err != nil {
		t.Fatalf("ConfigureGateway: %v", err)
	}
	defer gw.Shutdown(context.Background())

	reg, ok := res.CommandRegistry.(*command.Registry)
	if !ok || reg == nil {
		t.Fatalf("expected *command.Registry, got %T", res.CommandRegistry)
	}

	handled, err := reg.Execute(context.Background(), cobot.CommandContext{
		Text: "/cron list",
		Reply: func(msg *cobot.OutboundMessage) (*cobot.SendResult, error) {
			if msg == nil {
				t.Fatal("expected reply message")
			}
			if msg.Text == "Cron scheduler not available." {
				t.Fatal("cron scheduler should be injected into command registry")
			}
			return &cobot.SendResult{Success: true}, nil
		},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !handled {
		t.Fatal("expected /cron list to be handled")
	}
}
