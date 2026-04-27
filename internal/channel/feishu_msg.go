package channel

import (
	"encoding/json"
	"strings"
)

// ExtractTextContent parses the content field of a Feishu message event.
// The content is a JSON string whose structure depends on message_type:
//   - text:      {"text": "hello"}
//   - post:      {"title":"...","content":[[{"tag":"text","text":"..."}]]}
func ExtractTextContent(raw string) string {
	if raw == "" {
		return ""
	}

	// Try text format first.
	var textMsg struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(raw), &textMsg); err == nil && textMsg.Text != "" {
		return textMsg.Text
	}

	// Try post (rich text) format.
	var postMsg struct {
		Title   string                     `json:"title"`
		Content [][]map[string]interface{} `json:"content"`
	}
	if err := json.Unmarshal([]byte(raw), &postMsg); err == nil && len(postMsg.Content) > 0 {
		return extractPostText(postMsg)
	}

	// Fallback: return raw content for unknown formats.
	return raw
}

// extractPostText concatenates text elements from a Feishu post message.
func extractPostText(postMsg struct {
	Title   string                     `json:"title"`
	Content [][]map[string]interface{} `json:"content"`
}) string {
	var parts []string
	if postMsg.Title != "" {
		parts = append(parts, postMsg.Title)
	}
	for _, paragraph := range postMsg.Content {
		for _, element := range paragraph {
			if tag, ok := element["tag"].(string); ok && tag == "text" {
				if text, ok := element["text"].(string); ok && text != "" {
					parts = append(parts, text)
				}
			}
			// Handle link elements — extract their text content.
			if tag, ok := element["tag"].(string); ok && tag == "a" {
				if text, ok := element["text"].(string); ok && text != "" {
					parts = append(parts, text)
				}
			}
		}
	}
	result := &strings.Builder{}
	for i, p := range parts {
		if i > 0 {
			result.WriteString(" ")
		}
		result.WriteString(p)
	}
	return result.String()
}
