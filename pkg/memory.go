package cobot

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
