package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/cobot-agent/cobot/internal/memory"
	"github.com/cobot-agent/cobot/internal/textutil"
	cobot "github.com/cobot-agent/cobot/pkg"
)

var memoryCmd = &cobra.Command{
	Use:   "memory",
	Short: "Search and inspect memory palace",
}

var memorySearchCmd = &cobra.Command{
	Use:   "search [query]",
	Short: "Search memory",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ws, err := resolveWorkspace()
		if err != nil {
			return err
		}

		store, err := memory.OpenStore(ws.MemoryDir(), ws.SessionsDir())
		if err != nil {
			return err
		}
		defer store.Close()

		wingID, _ := cmd.Flags().GetString("wing")
		results, err := store.Search(context.Background(), &cobot.SearchQuery{
			Text:  args[0],
			Tier1: wingID,
			Limit: 10,
		})
		if err != nil {
			return err
		}

		if len(results) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No results found.")
			return nil
		}

		for _, r := range results {
			fmt.Fprintf(cmd.OutOrStdout(), "[%s] %.2f %s\n", r.ID, r.Score, textutil.Truncate(r.Content, 120))
		}
		return nil
	},
}

var memoryStoreCmd = &cobra.Command{
	Use:   "store [content] [wing_name] [room_name]",
	Short: "Store information in memory palace",
	Args:  cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		ws, err := resolveWorkspace()
		if err != nil {
			return err
		}

		store, err := memory.OpenStore(ws.MemoryDir(), ws.SessionsDir())
		if err != nil {
			return err
		}
		defer store.Close()

		content := args[0]
		wingName := args[1]
		roomName := args[2]
		hallType, _ := cmd.Flags().GetString("hall_type")
		if hallType == "" {
			hallType = cobot.TagFacts
		}

		wingID, err := store.CreateWingIfNotExists(context.Background(), wingName)
		if err != nil {
			return fmt.Errorf("creating wing: %w", err)
		}

		roomID, err := store.CreateRoomIfNotExists(context.Background(), wingID, roomName, hallType)
		if err != nil {
			return fmt.Errorf("creating room: %w", err)
		}

		drawerID, err := store.Store(context.Background(), content, wingID, roomID)
		if err != nil {
			return fmt.Errorf("storing content: %w", err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Stored in drawer %s (wing: %s, room: %s)\n", drawerID, wingName, roomName)
		return nil
	},
}

var memoryStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show memory palace overview",
	RunE: func(cmd *cobra.Command, args []string) error {
		ws, err := resolveWorkspace()
		if err != nil {
			return err
		}

		store, err := memory.OpenStore(ws.MemoryDir(), ws.SessionsDir())
		if err != nil {
			return err
		}
		defer store.Close()

		wings, err := store.GetWings(context.Background())
		if err != nil {
			return err
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Memory Palace for workspace '%s': %d wings\n", ws.Config.Name, len(wings))
		for _, w := range wings {
			rooms, _ := store.GetRooms(context.Background(), w.ID)
			fmt.Fprintf(cmd.OutOrStdout(), "  Wing: %s (%s) — %d rooms\n", w.Name, w.ID, len(rooms))
			for _, r := range rooms {
				fmt.Fprintf(cmd.OutOrStdout(), "    Room: %s [%s]\n", r.Name, r.HallType)
			}
		}
		return nil
	},
}

func init() {
	memorySearchCmd.Flags().String("wing", "", "Filter by wing ID")
	memoryStoreCmd.Flags().String("hall_type", cobot.TagFacts, "Room type: facts, log, or code")

	memoryCmd.AddCommand(memorySearchCmd)
	memoryCmd.AddCommand(memoryStoreCmd)
	memoryCmd.AddCommand(memoryStatusCmd)
	rootCmd.AddCommand(memoryCmd)
}
