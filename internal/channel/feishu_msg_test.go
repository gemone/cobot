package channel

import "testing"

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
