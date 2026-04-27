package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/skip2/go-qrcode"
	"github.com/spf13/cobra"

	"github.com/cobot-agent/cobot/internal/channel"
	"github.com/cobot-agent/cobot/internal/workspace"
)

var setupFeishuCmd = &cobra.Command{
	Use:   "feishu",
	Short: "Set up Feishu / Lark bot via QR code or manual credentials",
	Long: `Guided setup for Feishu / Lark. Supports two modes:

1. QR scan (recommended): opens a URL you can scan with the Feishu/Lark mobile app.
   The app is created automatically and credentials are saved to your config.

2. Manual: enter an existing app_id and app_secret from the Feishu Open Platform.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runSetupFeishu(cmd)
	},
}

func init() {
	setupCmd.AddCommand(setupFeishuCmd)
	setupFeishuCmd.Flags().String("name", "default", "channel name in config")
	setupFeishuCmd.Flags().String("domain", "feishu", "feishu (China) or lark (International)")
	setupFeishuCmd.Flags().Bool("manual", false, "skip QR flow and prompt for manual credentials")
}

func runSetupFeishu(cmd *cobra.Command) error {
	channelName, _ := cmd.Flags().GetString("name")
	domain, _ := cmd.Flags().GetString("domain")
	manual, _ := cmd.Flags().GetBool("manual")

	if domain != "feishu" && domain != "lark" {
		domain = "feishu"
	}

	fmt.Println()
	fmt.Println("  ─── 🛰 Feishu / Lark Setup ───")
	fmt.Println()

	reader := bufio.NewReader(os.Stdin)
	var cfg *channel.FeishuQRConfig

	// ── QR scan flow ──
	var qrDone bool
	if !manual {
		fmt.Println("  Connecting to Feishu / Lark...")
		qrURL, deviceCode, interval, expireIn, err := channel.BuildQRPayload(domain)
		if err != nil {
			fmt.Printf("  ⚠ QR flow unavailable: %v\n", err)
			fmt.Println("  Falling back to manual input...")
		} else {
			// Generate ASCII QR code.
			fmt.Println()
			fmt.Println("  Scan the QR code with your Feishu / Lark mobile app:")
			fmt.Println()
			qr, err := qrcode.New(qrURL, qrcode.Medium)
			if err == nil {
				fmt.Print(qr.ToString(false))
			} else {
				// Fallback: display QR as ASCII art failed, show URL only.
				fmt.Printf("  [QR generation failed — open URL below]\n\n")
			}
			fmt.Println()
			fmt.Println("  Or open this URL in your mobile browser:")
			fmt.Printf("  %s\n", qrURL)
			fmt.Println()
			fmt.Println("  Waiting for scan... (Ctrl+C to cancel) ")

			cfg, err = channel.PollAfterQR(domain, deviceCode, interval, expireIn)
			if err != nil {
				fmt.Printf("\n  ⚠ %v\n", err)
				fmt.Println("  Falling back to manual input...")
			} else {
				fmt.Println()
				fmt.Printf("  ✅ Registration successful!\n")
				fmt.Printf("     App ID: %s\n", cfg.AppID)
				fmt.Printf("     Domain: %s\n", cfg.Domain)

				// Verify bot connectivity.
				fmt.Print("  Verifying bot... ")
				botName, err := channel.VerifyManualCredentials(cfg.AppID, cfg.AppSecret, cfg.Domain)
				if err != nil {
					fmt.Printf("⚠ %v\n", err)
				} else if botName != "" {
					fmt.Printf("✅ Bot: %s\n", botName)
				} else {
					fmt.Printf("✅ OK\n")
				}
				qrDone = true
			}
		}
	}

	// ── Manual credential input ──
	if !qrDone {
		fmt.Println("  ── Manual Setup ──")
		fmt.Println()
		fmt.Println("  1. Go to https://open.feishu.cn/app (or open.larksuite.com/app)")
		fmt.Println("  2. Create an app or select an existing one")
		fmt.Println("  3. Enable Bot capability")
		fmt.Println("  4. Copy App ID and App Secret from Credentials tab")
		fmt.Println()

		fmt.Print("  App ID: ")
		appID, _ := reader.ReadString('\n')
		appID = strings.TrimSpace(appID)
		if appID == "" {
			fmt.Println("  Skipped — Feishu setup cancelled.")
			return nil
		}

		fmt.Print("  App Secret: ")
		appSecret, _ := reader.ReadString('\n')
		appSecret = strings.TrimSpace(appSecret)
		if appSecret == "" {
			fmt.Println("  Skipped — Feishu setup cancelled.")
			return nil
		}

		// Probe to verify.
		fmt.Print("  Verifying credentials... ")
		botName, err := channel.VerifyManualCredentials(appID, appSecret, domain)
		if err != nil {
			fmt.Printf("⚠\n  ⚠ Could not verify: %v\n", err)
			fmt.Println("  Saving credentials anyway...")
		} else if botName != "" {
			fmt.Printf("✅ Bot: %s\n", botName)
		} else {
			fmt.Printf("✅ OK\n")
		}

		cfg = &channel.FeishuQRConfig{
			AppID:     appID,
			AppSecret: appSecret,
			Domain:    domain,
		}
	}

	// ── Save to config ──
	cfgDir := workspace.ConfigDir()
	configPath := cfgDir + "/config.yaml"

	// Ensure config file exists.
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		f, err := os.Create(configPath)
		if err != nil {
			return fmt.Errorf("create config: %w", err)
		}
		f.Close()
	}

	if err := channel.SaveFeishuChannelConfig(configPath, cfg, channelName); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	fmt.Println()
	fmt.Printf("  ✅ Feishu channel %q saved to %s\n", channelName, configPath)
	fmt.Println()
	fmt.Println("  ── WebSocket Mode ──")
	fmt.Println()
	fmt.Println("  Feishu will connect to your bot via WebSocket — no public URL required.")
	fmt.Println()
	fmt.Println("  Next steps:")
	fmt.Println("  1. In Feishu Open Platform, go to Event Subscriptions")
	fmt.Println("     and enable the \"Receive Messages\" event.")
	fmt.Println("  2. Run the gateway:")
	fmt.Println("       cobot gateway --addr :8080")
	fmt.Println()
	return nil
}
