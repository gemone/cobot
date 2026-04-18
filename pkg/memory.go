package cobot

import "time"

// Wing is a top-level domain in the MemPalace hierarchy.
type Wing struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Keywords []string `json:"keywords,omitempty"`
}

// Room is a contextual space within a Wing.
type Room struct {
	ID       string `json:"id"`
	WingID   string `json:"wing_id"`
	Name     string `json:"name"`
	HallType string `json:"hall_type"`
}

// Drawer is a raw content entry within a Room.
type Drawer struct {
	ID        string    `json:"id"`
	RoomID    string    `json:"room_id"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// Closet is a summarized aggregation of Drawers.
type Closet struct {
	ID        string   `json:"id"`
	RoomID    string   `json:"room_id"`
	DrawerIDs []string `json:"drawer_ids"`
	Summary   string   `json:"summary"`
}

// SearchQuery specifies a memory search request.
type SearchQuery struct {
	Text  string `json:"text"`
	Tier1 string `json:"tier1,omitempty"`
	Tier2 string `json:"tier2,omitempty"`
	Tag   string `json:"tag,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

// SearchResult is a single memory search hit.
type SearchResult struct {
	ID      string  `json:"id"`
	Content string  `json:"content"`
	Tier1   string  `json:"tier1"`
	Tier2   string  `json:"tier2"`
	Score   float64 `json:"score"`
}

// Memory tag constants.
const (
	TagFacts = "facts"
	TagLog   = "log"
	TagCode  = "code"
)
