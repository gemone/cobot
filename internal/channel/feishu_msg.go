package channel

import (
	"encoding/json"
	"fmt"
	"strings"
)

func extractTextContent(msgType, content string) string {
	switch msgType {
	case "text":
		var tc struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(content), &tc); err != nil {
			return content
		}
		return strings.TrimSpace(tc.Text)
	case "post":
		return extractPostText(content)
	default:
		return content
	}
}

func extractPostText(content string) string {
	var post struct {
		Title   string          `json:"title"`
		Content [][]postElement `json:"content"`
	}
	if err := json.Unmarshal([]byte(content), &post); err != nil {
		return content
	}
	var parts []string
	if post.Title != "" {
		parts = append(parts, post.Title)
	}
	for _, paragraph := range post.Content {
		for _, elem := range paragraph {
			if elem.Text != "" {
				parts = append(parts, elem.Text)
			} else if elem.Tag == "at" {
				parts = append(parts, fmt.Sprintf("@%s", elem.UserID))
			}
		}
	}
	return strings.Join(parts, "\n")
}

type postElement struct {
	Tag    string `json:"tag"`
	Text   string `json:"text,omitempty"`
	UserID string `json:"user_id,omitempty"`
}

func stripMentionPrefix(text string) string {
	text = strings.TrimSpace(text)
	for strings.HasPrefix(text, "@_user_1") {
		text = strings.TrimSpace(text[len("@_user_1"):])
	}
	if idx := strings.Index(text, " "); idx > 0 && text[0] == '@' {
		text = strings.TrimSpace(text[idx+1:])
	}
	return text
}

func buildTextContent(text string) string {
	content, _ := json.Marshal(map[string]string{"text": text})
	return string(content)
}
