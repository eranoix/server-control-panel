package jira

import (
	"encoding/json"
	"strings"
)

func textToADF(text string) map[string]any {
	if text == "" {
		return map[string]any{
			"type":    "doc",
			"version": 1,
			"content": []any{},
		}
	}
	lines := strings.Split(text, "\n")
	content := make([]any, 0, len(lines))
	for _, line := range lines {
		stop := map[string]any{
			"type": "paragraph",
		}
		if line != "" {
			stop["content"] = []any{
				map[string]any{"type": "text", "text": line},
			}
		}
		content = append(content, stop)
	}
	return map[string]any{
		"type":    "doc",
		"version": 1,
		"content": content,
	}
}

func adfToText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
		return ""
	}
	var b strings.Builder
	walkADF(doc, &b)
	return strings.TrimSpace(b.String())
}

func walkADF(node any, b *strings.Builder) {
	switch n := node.(type) {
	case map[string]any:
		switch n["type"] {
		case "text":
			if s, ok := n["text"].(string); ok {
				var href string
				if marks, ok := n["marks"].([]any); ok {
					for _, m := range marks {
						if mm, ok := m.(map[string]any); ok && mm["type"] == "link" {
							if attrs, ok := mm["attrs"].(map[string]any); ok {
								if h, ok := attrs["href"].(string); ok {
									href = h
								}
							}
						}
					}
				}
				if href != "" {
					b.WriteString(s)
					b.WriteString(" [")
					b.WriteString(href)
					b.WriteString("]")
				} else {
					b.WriteString(s)
				}
			}
		case "hardBreak":
			b.WriteString("\n")
		case "paragraph", "heading":
			if c, ok := n["content"].([]any); ok {
				for _, child := range c {
					walkADF(child, b)
				}
			}
			b.WriteString("\n")
		case "bulletList", "orderedList":
			if c, ok := n["content"].([]any); ok {
				for _, child := range c {
					b.WriteString("- ")
					walkADF(child, b)
				}
			}
		case "codeBlock":
			b.WriteString("```\n")
			if c, ok := n["content"].([]any); ok {
				for _, child := range c {
					walkADF(child, b)
				}
			}
			b.WriteString("```\n")
		case "blockquote":
			var inner strings.Builder
			if c, ok := n["content"].([]any); ok {
				for _, child := range c {
					walkADF(child, &inner)
				}
			}
			for _, line := range strings.Split(strings.TrimRight(inner.String(), "\n"), "\n") {
				b.WriteString("> ")
				b.WriteString(line)
				b.WriteString("\n")
			}
		case "mention":
			if attrs, ok := n["attrs"].(map[string]any); ok {
				if t, ok := attrs["text"].(string); ok {
					b.WriteString(t)
				} else if id, ok := attrs["id"].(string); ok {
					b.WriteString("@")
					b.WriteString(id)
				}
			}
		case "inlineCard":
			if attrs, ok := n["attrs"].(map[string]any); ok {
				if u, ok := attrs["url"].(string); ok {
					b.WriteString(u)
				}
			}
		default:
			if c, ok := n["content"].([]any); ok {
				for _, child := range c {
					walkADF(child, b)
				}
			}
		}
	case []any:
		for _, child := range n {
			walkADF(child, b)
		}
	}
}
