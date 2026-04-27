package channel

import (
	"encoding/json"
	"strings"
)

// ImageKey represents a Feishu image key with optional alt text.
type ImageKey struct {
	Key  string
	Alt  string
}

// Mention represents a parsed Feishu @mention element.
type Mention struct {
	UserID   string
	UserName string
	IsAll    bool
}

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

// ExtractMediaKeys extracts image keys from a Feishu message content JSON.
// It handles both top-level "image_key" (for message_type=image) and
// post-content img tag elements.
func ExtractMediaKeys(raw, msgType string) []ImageKey {
	if raw == "" {
		return nil
	}

	var keys []ImageKey

	// Direct image_key at top level (image message type).
	if msgType == "image" {
		var imgMsg struct {
			ImageKey string `json:"image_key"`
			Text     string `json:"text"`
			Alt      string `json:"alt"`
		}
		if json.Unmarshal([]byte(raw), &imgMsg) == nil && imgMsg.ImageKey != "" {
			alt := imgMsg.Text
			if alt == "" {
				alt = imgMsg.Alt
			}
			keys = append(keys, ImageKey{Key: imgMsg.ImageKey, Alt: alt})
			return keys
		}
	}

	// Post rich text content with img/image tags.
	var postMsg struct {
		Content [][]map[string]interface{} `json:"content"`
	}
	if json.Unmarshal([]byte(raw), &postMsg) != nil || len(postMsg.Content) == 0 {
		return keys
	}

	seen := make(map[string]bool)
	for _, paragraph := range postMsg.Content {
		for _, element := range paragraph {
			tag, _ := element["tag"].(string)
			if tag != "img" && tag != "image" {
				continue
			}
			key, _ := element["image_key"].(string)
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			alt, _ := element["text"].(string)
			if alt == "" {
				alt, _ = element["alt"].(string)
			}
			keys = append(keys, ImageKey{Key: key, Alt: alt})
		}
	}
	return keys
}

// ExtractMentions parses @mention elements from a Feishu post message content JSON.
// mentionsJSON is the top-level mentions array from the Feishu event payload,
// e.g. [{"type":"user","id":{"open_id":"ou_xxx"},"name":"Alice"}].
func ExtractMentions(raw string, mentionsJSON []byte) []Mention {
	var mentions []Mention
	seen := make(map[string]bool)

	// Parse the mentions array from the event payload.
	if len(mentionsJSON) > 0 {
		var mentionsData []struct {
			Type string `json:"type"`
			ID   struct {
				OpenID string `json:"open_id"`
				UserID string `json:"user_id"`
			} `json:"id"`
			Name string `json:"name"`
		}
		if json.Unmarshal(mentionsJSON, &mentionsData) == nil {
			for _, m := range mentionsData {
				id := m.ID.OpenID
				if id == "" {
					id = m.ID.UserID
				}
				if id == "" || seen[id] {
					continue
				}
				seen[id] = true
				mentions = append(mentions, Mention{UserID: id, UserName: m.Name})
			}
		}
	}

	// Also scan post content for <at> tags (some events don't include mentions array).
	var postMsg struct {
		Content [][]map[string]interface{} `json:"content"`
	}
	if json.Unmarshal([]byte(raw), &postMsg) != nil || len(postMsg.Content) == 0 {
		return mentions
	}

	for _, paragraph := range postMsg.Content {
		for _, element := range paragraph {
			if tag, _ := element["tag"].(string); tag != "at" {
				continue
			}
			userID, _ := element["user_id"].(string)
			if userID == "" || userID == "@_all" || seen[userID] {
				continue
			}
			seen[userID] = true
			userName, _ := element["user_name"].(string)
			mentions = append(mentions, Mention{UserID: userID, UserName: userName})
		}
	}
	return mentions
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
