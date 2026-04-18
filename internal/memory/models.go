package memory

import "time"

// Wing is a top-level domain in the memory hierarchy.
type Wing struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Keywords []string `json:"keywords,omitempty"`
}

// Room is a contextual space within a wing.
type Room struct {
	ID       string `json:"id"`
	WingID   string `json:"wing_id"`
	Name     string `json:"name"`
	HallType string `json:"hall_type"`
}

// Drawer is a raw content entry within a room.
type Drawer struct {
	ID        string    `json:"id"`
	RoomID    string    `json:"room_id"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// Closet is a summarized aggregation of drawers.
type Closet struct {
	ID        string   `json:"id"`
	RoomID    string   `json:"room_id"`
	DrawerIDs []string `json:"drawer_ids"`
	Summary   string   `json:"summary"`
}
