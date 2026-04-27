package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

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
			fmt.Println()
			fmt.Println("  Scan the QR code with your Feishu / Lark mobile app:")
			fmt.Println()
			fmt.Printf("  %s\n", qrURL)
			fmt.Println()
			fmt.Println("  Or open the URL above in your mobile browser.")
			fmt.Println()
			fmt.Print("  Waiting for scan... (Ctrl+C to cancel) ")

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

	// Ask for gateway address and auto-generate tokens.
	fmt.Println("  ── Gateway Configuration ──")
	fmt.Println()
	fmt.Print("  Gateway address (e.g. :8080 or mybot.com:8080) [press Enter for :8080]: ")
	gatewayAddr, _ := reader.ReadString('\n')
	gatewayAddr = strings.TrimSpace(gatewayAddr)
	if gatewayAddr == "" {
		gatewayAddr = ":8080"
	}

	cfg.VerificationToken = channel.GenerateVerificationToken()
	cfg.EncryptKey = channel.GenerateEncryptKey()
	webhookURL := channel.ComputeWebhookURL(gatewayAddr, channelName)

	fmt.Println()
	fmt.Println("  ✅ Generated security credentials (saved to config above):")
	fmt.Printf("     Verification Token: %s\n", cfg.VerificationToken)
	fmt.Printf("     Encrypt Key:       %s\n", cfg.EncryptKey)
	fmt.Println()
	fmt.Println("  ╔═══════════════════════════════════════════════════════════════╗")
	fmt.Printf("  ║  📡 Webhook URL (set this in Feishu Open Platform):         ║\n")
	fmt.Printf("  ║                                                              ║\n")
	fmt.Printf("  ║  %s\n", webhookURL)
	fmt.Printf("  ║                                                              ║")
	fmt.Println()
	fmt.Println("  ╚═══════════════════════════════════════════════════════════════╝")
	fmt.Println()
	fmt.Println("  Next steps:")
	fmt.Println("  1. Go to Feishu Open Platform → Events & Callbacks → Request URL")
	fmt.Printf("     Paste the webhook URL above and press Enter.\n")
	fmt.Println("  2. Enter the Verification Token when prompted by Feishu.")
	fmt.Println("  3. Enable AES encryption and paste the Encrypt Key.")
	fmt.Println("  4. Enable the \"Receive Messages\" event subscription.")
	fmt.Println()
	fmt.Println("  5. Run the gateway:")
	fmt.Printf("       cobot gateway --addr %s\n", gatewayAddr)
	fmt.Println()
	return nil
}
