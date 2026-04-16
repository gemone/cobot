package memory

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"

	cobot "github.com/cobot-agent/cobot/pkg"
)

func (s *Store) WakeUp(ctx context.Context) (string, error) {
	return s.WakeUpWithDeepSearch(ctx, false)
}

func (s *Store) WakeUpWithDeepSearch(ctx context.Context, deepSearch bool) (string, error) {
	identity := cobot.DefaultSystemPrompt

	wings, err := s.GetWings(ctx)
	if err != nil {
		return "", err
	}

	var sections []string
	sections = append(sections, identity)

	// Basic wakeup: collect facts and room context
	facts := s.collectFacts(ctx, wings)
	if len(facts) > 0 {
		sections = append(sections, "## Known Facts")
		sections = append(sections, facts...)
	}

	roomContexts := s.collectRoomRecall(ctx, wings)
	if len(roomContexts) > 0 {
		sections = append(sections, "## Room Context")
		sections = append(sections, roomContexts...)
	}

	// Deep search: semantic search across all memory
	if deepSearch {
		deepResults := s.collectDeepSearch(ctx, wings, "")
		if len(deepResults) > 0 {
			sections = append(sections, "## Deep Search")
			sections = append(sections, deepResults...)
		}
	}

	var b strings.Builder
	for i, sec := range sections {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(sec)
	}
	return b.String(), nil
}

func (s *Store) collectFacts(ctx context.Context, wings []*cobot.Wing) []string {
	var facts []string
	for _, w := range wings {
		rooms, err := s.GetRooms(ctx, w.ID)
		if err != nil {
			slog.Warn("failed to get rooms", "wing", w.ID, "error", err)
			continue
		}
		for _, r := range rooms {
			if r.HallType != cobot.TagFacts {
				continue
			}
			closets, err := s.GetClosets(ctx, r.ID)
			if err != nil {
				slog.Warn("failed to get closets", "room", r.ID, "error", err)
				continue
			}
			for _, c := range closets {
				if c.Summary != "" {
					facts = append(facts, "- "+c.Summary)
				}
			}
		}
	}
	return facts
}

func (s *Store) collectRoomRecall(ctx context.Context, wings []*cobot.Wing) []string {
	var contexts []string
	for _, w := range wings {
		rooms, err := s.GetRooms(ctx, w.ID)
		if err != nil {
			slog.Warn("failed to get rooms", "wing", w.ID, "error", err)
			continue
		}
		for _, r := range rooms {
			var b strings.Builder
			b.WriteString("### ")
			b.WriteString(w.Name)
			b.WriteString(" / ")
			b.WriteString(r.Name)
			b.WriteString(" (")
			b.WriteString(r.HallType)
			b.WriteString(")")

			drawers, err := s.searchDrawers(ctx, &cobot.SearchQuery{
				Tier2: r.ID,
				Limit: 5,
			})
			if err == nil && len(drawers) > 0 {
				for _, d := range drawers {
					content := d.Content
					if len(content) > 100 {
						content = content[:100] + "..."
					}
					b.WriteString("\n- ")
					b.WriteString(content)
				}
			}
			contexts = append(contexts, b.String())
		}
	}
	return contexts
}

// WakeUpSTM returns short-term memory context for the current session,
// grouped by the five STM rooms (context, todo, notes, observation, compressed).
// This should be called every turn to get fresh STM context.
// It returns an empty string if there are no STM items.
func (s *Store) WakeUpSTM(ctx context.Context, sessionID string) (string, error) {
	stmDB, err := s.getSTMDB(sessionID)
	if err != nil {
		return "", nil // no STM DB, return empty
	}

	// Look up the session wing in the STM DB.
	var w cobot.Wing
	var kwJSON string
	row := stmDB.QueryRowContext(ctx, sqlSelectWingByName, stmWingName)
	if err := row.Scan(&w.ID, &w.Name, &w.Type, &kwJSON); err == sql.ErrNoRows {
		return "", nil
	} else if err != nil {
		return "", nil
	}

	rooms, err := func() ([]*cobot.Room, error) {
		rows, err := stmDB.QueryContext(ctx, sqlSelectRooms, w.ID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var rooms []*cobot.Room
		for rows.Next() {
			var r cobot.Room
			if err := rows.Scan(&r.ID, &r.WingID, &r.Name, &r.HallType); err != nil {
				return nil, err
			}
			rooms = append(rooms, &r)
		}
		return rooms, rows.Err()
	}()
	if err != nil {
		return "", err
	}

	// Define the order and labels for STM rooms.
	roomOrder := []struct {
		name  string
		label string
	}{
		{stmRoomContext, "Context"},
		{stmRoomTodo, "TODO"},
		{stmRoomNotes, "Notes"},
		{stmRoomObservation, "Observations"},
		{stmRoomCompressed, "Session Summary"},
	}

	// Index rooms by name for quick lookup.
	roomByName := make(map[string]*cobot.Room)
	for _, room := range rooms {
		roomByName[room.Name] = room
	}

	var b strings.Builder
	fmt.Fprintf(&b, "## Short-Term Memory (Session %s)\n", sessionID)

	hasContent := false

	for _, ro := range roomOrder {
		room, ok := roomByName[ro.name]
		if !ok {
			continue
		}

		rows, err := stmDB.QueryContext(ctx, sqlSelectDrawersByRoomOrdered, room.ID)
		if err != nil {
			continue
		}
		var count int
		for rows.Next() {
			var d cobot.Drawer
			if err := rows.Scan(&d.ID, &d.RoomID, &d.Content, &d.CreatedAt); err != nil {
				rows.Close()
				continue
			}
			if count == 0 {
				fmt.Fprintf(&b, "### %s\n", ro.label)
				hasContent = true
			}
			fmt.Fprintf(&b, "- %s\n", d.Content)
			count++
		}
		rows.Close()
	}

	if !hasContent {
		return "", nil
	}
	return b.String(), nil
}
