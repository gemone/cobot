package channel

import "testing"

// ============================================================================
// ExtractTextContent tests (original)
// ============================================================================

func TestExtractTextContent(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		expected string
	}{
		{
			name:     "empty string",
			raw:      "",
			expected: "",
		},
		{
			name:     "plain text message",
			raw:      `{"text":"hello world"}`,
			expected: "hello world",
		},
		{
			name:     "plain text with special chars",
			raw:      `{"text":"hello\nworld\t!"}`,
			expected: "hello\nworld\t!",
		},
		{
			name:     "post with title only",
			raw:      `{"title":"Meeting Notes","content":[[]]}`,
			expected: "Meeting Notes",
		},
		{
			name:     "post with text elements",
			raw:      `{"title":"","content":[[{"tag":"text","text":"hello"}]]}`,
			expected: "hello",
		},
		{
			name:     "post with title and text",
			raw:      `{"title":"Header","content":[[{"tag":"text","text":"body text"}]]}`,
			expected: "Header body text",
		},
		{
			name:     "post with link element",
			raw:      `{"title":"","content":[[{"tag":"a","text":"click here","href":"https://example.com"}]]}`,
			expected: "click here",
		},
		{
			name:     "post with mixed text and link",
			raw:      `{"title":"Doc","content":[[{"tag":"text","text":"see"},{"tag":"a","text":"this link"}]]}`,
			expected: "Doc see this link",
		},
		{
			name:     "post multiple paragraphs",
			raw:      `{"title":"","content":[[{"tag":"text","text":"para1"}],[{"tag":"text","text":"para2"}]]}`,
			expected: "para1 para2",
		},
		{
			name:     "unknown format returns raw",
			raw:      `{"unknown":"value"}`,
			expected: `{"unknown":"value"}`,
		},
		{
			name:     "malformed json returns raw",
			raw:      `not valid json`,
			expected: `not valid json`,
		},
		{
			name:     "text with emoji",
			raw:      `{"text":"hello 👋 world"}`,
			expected: "hello 👋 world",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractTextContent(tt.raw)
			if got != tt.expected {
				t.Errorf("ExtractTextContent(%q) = %q, want %q", tt.raw, got, tt.expected)
			}
		})
	}
}

// ============================================================================
// ExtractMediaKeys tests
// ============================================================================

func TestExtractMediaKeys(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		msgType  string
		expected []ImageKey
	}{
		{
			name:     "empty",
			raw:      "",
			msgType:  "text",
			expected: nil,
		},
		{
			name:    "direct image message",
			raw:     `{"image_key":"img_v1_xxx","text":"screenshot"}`,
			msgType: "image",
			expected: []ImageKey{{Key: "img_v1_xxx", Alt: "screenshot"}},
		},
		{
			name:    "direct image with alt",
			raw:     `{"image_key":"img_v1_yyy","alt":"photo alt"}`,
			msgType: "image",
			expected: []ImageKey{{Key: "img_v1_yyy", Alt: "photo alt"}},
		},
		{
			name:    "post with img element",
			raw:     `{"title":"","content":[[{"tag":"img","image_key":"img_v1_xxx","text":"alt text"}]]}`,
			msgType: "post",
			expected: []ImageKey{{Key: "img_v1_xxx", Alt: "alt text"}},
		},
		{
			name:    "post with image element",
			raw:     `{"title":"","content":[[{"tag":"image","image_key":"img_v1_yyy"}]]}`,
			msgType: "post",
			expected: []ImageKey{{Key: "img_v1_yyy", Alt: ""}},
		},
		{
			name:    "post with multiple images deduplicated",
			raw:     `{"title":"","content":[[{"tag":"img","image_key":"img_v1_a","text":"a"}],[{"tag":"img","image_key":"img_v1_a","text":"a again"}]]}`,
			msgType: "post",
			expected: []ImageKey{{Key: "img_v1_a", Alt: "a"}},
		},
		{
			name:    "text message has no keys",
			raw:     `{"text":"hello"}`,
			msgType: "text",
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractMediaKeys(tt.raw, tt.msgType)
			if len(got) != len(tt.expected) {
				t.Errorf("ExtractMediaKeys(%q, %q) returned %d keys, want %d", tt.raw, tt.msgType, len(got), len(tt.expected))
				return
			}
			for i := range got {
				if got[i].Key != tt.expected[i].Key {
					t.Errorf("ExtractMediaKeys[%d].Key = %q, want %q", i, got[i].Key, tt.expected[i].Key)
				}
				if got[i].Alt != tt.expected[i].Alt {
					t.Errorf("ExtractMediaKeys[%d].Alt = %q, want %q", i, got[i].Alt, tt.expected[i].Alt)
				}
			}
		})
	}
}

// ============================================================================
// ExtractMentions tests
// ============================================================================

func TestExtractMentions(t *testing.T) {
	tests := []struct {
		name         string
		raw          string
		mentionsJSON []byte
		expected     []Mention
	}{
		{
			name:         "empty",
			raw:          "",
			mentionsJSON: nil,
			expected:     nil,
		},
		{
			name:  "from mentions array",
			raw:   `{"title":"","content":[[]]}`,
			mentionsJSON: []byte(`[{"type":"user","id":{"open_id":"ou_aaa"},"name":"Alice"}]`),
			expected: []Mention{{UserID: "ou_aaa", UserName: "Alice"}},
		},
		{
			name:  "from post at tag",
			raw:   `{"title":"","content":[[{"tag":"at","user_id":"ou_bbb","user_name":"Bob"}]]}`,
			mentionsJSON: nil,
			expected: []Mention{{UserID: "ou_bbb", UserName: "Bob"}},
		},
		{
			name:  "from both array and at tag",
			raw:   `{"title":"","content":[[{"tag":"at","user_id":"ou_bbb","user_name":"Bob"}]]}`,
			mentionsJSON: []byte(`[{"type":"user","id":{"open_id":"ou_aaa"},"name":"Alice"}]`),
			expected: []Mention{
				{UserID: "ou_aaa", UserName: "Alice"},
				{UserID: "ou_bbb", UserName: "Bob"},
			},
		},
		{
			name:         "all mention",
			raw:          `{"title":"","content":[[{"tag":"at","user_id":"@_all"}]]}`,
			mentionsJSON: nil,
			expected:     nil, // @_all is skipped
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractMentions(tt.raw, tt.mentionsJSON)
			if len(got) != len(tt.expected) {
				t.Errorf("ExtractMentions(%q, %q) returned %d mentions, want %d", tt.raw, tt.mentionsJSON, len(got), len(tt.expected))
				return
			}
			for i := range got {
				if got[i].UserID != tt.expected[i].UserID {
					t.Errorf("ExtractMentions[%d].UserID = %q, want %q", i, got[i].UserID, tt.expected[i].UserID)
				}
				if got[i].UserName != tt.expected[i].UserName {
					t.Errorf("ExtractMentions[%d].UserName = %q, want %q", i, got[i].UserName, tt.expected[i].UserName)
				}
			}
		})
	}
}
