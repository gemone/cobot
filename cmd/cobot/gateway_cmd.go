package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/cobot-agent/cobot/internal/bootstrap"
)

var gatewayCmd = &cobra.Command{
	Use:   "gateway",
	Short: "Start the gateway server for platform integrations",
	Long:  "Start the gateway HTTP server that listens for incoming messages from configured platforms (Feishu, Telegram, etc.) and routes them to the agent.",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}

		gwAddr, err := cmd.Flags().GetString("addr")
		if err != nil {
			return err
		}
		if gwAddr != "" {
			cfg.Gateway.Addr = gwAddr
		}

		res, err := bootstrap.InitAgent(cfg, false)
		if err != nil {
			return err
		}
		cleanup := res.Cleanup
		defer cleanup()

		gw, err := bootstrap.ConfigureGateway(res, cfg.Gateway)
		if err != nil {
			return err
		}
		slog.Info("gateway started", "addr", gw.Addr())

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		<-ctx.Done()
		slog.Info("shutting down gateway...")

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		return gw.Shutdown(shutdownCtx)
	},
}

func init() {
	rootCmd.AddCommand(gatewayCmd)
	gatewayCmd.Flags().String("addr", "", "gateway listen address (default from config or :8080)")
}
