package memory

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"strings"
	"text/template"

	cobot "github.com/cobot-agent/cobot/pkg"
)

//go:embed embed_search.sql.tmpl
var searchSQLTmpl string

var searchTemplates = template.Must(
	template.New("search").Parse(searchSQLTmpl),
)

// searchTmplData holds the conditional flags for SQL template rendering.
type searchTmplData struct {
	Tier1 bool
	Tier2 bool
	Tag   bool
}

// renderSearchSQL renders a named section from search.sql.tmpl.
// The template file contains sections separated by "-- name: <name>".
func renderSearchSQL(name string, data searchTmplData) (string, error) {
	// Parse sections from template output.
	var buf bytes.Buffer
	if err := searchTemplates.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render search SQL: %w", err)
	}
	full := buf.String()

	// Find the named section.
	marker := "-- name: " + name
	_, section, found := strings.Cut(full, marker)
	if !found {
		return "", fmt.Errorf("search SQL section not found: %s", name)
	}
	// Trim until next section or end.
	if nextIdx := strings.Index(section, "\n-- name: "); nextIdx >= 0 {
		section = section[:nextIdx]
	}
	return strings.TrimSpace(section), nil
}

// searchDrawers performs FTS5 full-text search on drawers, with optional
// filtering by tier1 (wing) and tier2 (room). Results are scored using bm25.
func (s *Store) searchDrawers(ctx context.Context, query *cobot.SearchQuery) ([]*cobot.SearchResult, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = 10
	}

	// No text query — return recent drawers with optional tier filters.
	if query.Text == "" {
		return s.listDrawers(ctx, query, limit)
	}

	data := searchTmplData{
		Tier1: query.Tier1 != "",
		Tier2: query.Tier2 != "",
		Tag:   query.Tag != "",
	}
	sql, err := renderSearchSQL("search_drawers", data)
	if err != nil {
		return nil, err
	}

	args := []any{query.Text}
	if query.Tier1 != "" {
		args = append(args, query.Tier1)
	}
	if query.Tier2 != "" {
		args = append(args, query.Tier2)
	}
	if query.Tag != "" {
		args = append(args, query.Tag)
	}
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []*cobot.SearchResult
	for rows.Next() {
		var r cobot.SearchResult
		if err := rows.Scan(&r.ID, &r.Content, &r.Tier1, &r.Tier2, &r.Score); err != nil {
			return nil, err
		}
		results = append(results, &r)
	}
	return results, rows.Err()
}

// listDrawers returns recent drawers without full-text search.
func (s *Store) listDrawers(ctx context.Context, query *cobot.SearchQuery, limit int) ([]*cobot.SearchResult, error) {
	data := searchTmplData{
		Tier1: query.Tier1 != "",
		Tier2: query.Tier2 != "",
		Tag:   query.Tag != "",
	}
	sql, err := renderSearchSQL("list_drawers", data)
	if err != nil {
		return nil, err
	}

	var args []any
	if query.Tier1 != "" {
		args = append(args, query.Tier1)
	}
	if query.Tier2 != "" {
		args = append(args, query.Tier2)
	}
	if query.Tag != "" {
		args = append(args, query.Tag)
	}
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []*cobot.SearchResult
	for rows.Next() {
		var r cobot.SearchResult
		if err := rows.Scan(&r.ID, &r.Content, &r.Tier1, &r.Tier2); err != nil {
			return nil, err
		}
		results = append(results, &r)
	}
	return results, rows.Err()
}

// --- L3 Deep Search ---

// L3DeepSearch performs comprehensive full-text search across all memory.
func (s *Store) L3DeepSearch(ctx context.Context, query string, limit int) ([]*cobot.SearchResult, error) {
	if limit <= 0 {
		limit = 10
	}
	return s.searchDrawers(ctx, &cobot.SearchQuery{
		Text:  query,
		Limit: limit,
	})
}

// collectDeepSearch performs context-aware deep search for WakeUp.
func (s *Store) collectDeepSearch(ctx context.Context, wings []*cobot.Wing, contextHint string) []string {
	var results []string

	queries := generateDeepQueries(contextHint)

	for _, query := range queries {
		searchResults, err := s.L3DeepSearch(ctx, query, 3)
		if err != nil || len(searchResults) == 0 {
			continue
		}

		var b strings.Builder
		b.WriteString("### Related: ")
		b.WriteString(query)
		for _, r := range searchResults {
			content := cobot.Truncate(r.Content, 150)
			b.WriteString("\n- [")
			b.WriteString(r.Tier1)
			b.WriteString("] ")
			b.WriteString(content)
		}
		results = append(results, b.String())
	}

	return results
}

// generateDeepQueries creates search queries based on context hint.
func generateDeepQueries(contextHint string) []string {
	if contextHint != "" {
		return []string{
			contextHint,
			contextHint + " recent",
		}
	}
	return []string{
		"important decision",
		"key insight",
		"lesson learned",
		"TODO",
		"important",
	}
}

// SummarizeContent generates a summary for a drawer using simple extraction.
func (s *Store) SummarizeContent(content string) string {
	lines := strings.Split(content, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if len(line) > 10 {
			return cobot.Truncate(line, 200)
		}
	}

	return cobot.Truncate(content, 200)
}

// AutoSummarizeRoom generates summaries for all closets in a room.
func (s *Store) AutoSummarizeRoom(ctx context.Context, wingID, roomID string) error {
	results, err := s.searchDrawers(ctx, &cobot.SearchQuery{
		Tier1: wingID,
		Tier2: roomID,
		Limit: 10,
	})
	if err != nil {
		return err
	}

	if len(results) == 0 {
		return nil
	}

	var summaries []string
	for _, r := range results {
		summary := s.SummarizeContent(r.Content)
		if summary != "" {
			summaries = append(summaries, summary)
		}
	}

	if len(summaries) == 0 {
		return nil
	}

	combinedSummary := cobot.Truncate(strings.Join(summaries, "; "), 500)

	closet := &cobot.Closet{
		RoomID:  roomID,
		Summary: combinedSummary,
	}

	return s.CreateCloset(ctx, closet)
}
