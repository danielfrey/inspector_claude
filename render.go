package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"inspector_claude/internal/session"
)

// renderEntries turns a session's entries into display lines (unstyled). The
// TUI wraps and highlights these; the plain-CLI `show` mode prints them as-is.
func renderEntries(entries []session.Entry) []string {
	var lines []string
	add := func(ss ...string) { lines = append(lines, ss...) }

	for _, e := range entries {
		if e.Message == nil {
			continue
		}
		blocks := e.Message.Blocks()
		if len(blocks) == 0 {
			continue
		}
		for _, b := range blocks {
			switch b.Type {
			case "text":
				if strings.TrimSpace(b.Text) == "" {
					continue
				}
				if e.Type == "user" {
					add("", "▶ YOU")
				} else {
					add("", "● CLAUDE")
				}
				add(splitLines(b.Text)...)
			case "thinking":
				txt := b.PlainText()
				if strings.TrimSpace(txt) == "" {
					continue
				}
				add("", "  · thinking")
				add(indent(splitLines(txt), "  ")...)
			case "tool_use":
				add("", "  ⚙ "+b.Name+"  "+summarizeInput(b.Input))
			case "tool_result":
				txt := strings.TrimRight(b.PlainText(), "\n")
				if strings.TrimSpace(txt) == "" {
					continue
				}
				add("  ⎿ result:")
				add(indent(splitLines(truncate(txt, 4000)), "    ")...)
			}
		}
	}
	if len(lines) > 0 && lines[0] == "" {
		lines = lines[1:]
	}
	return lines
}

// summarizeInput renders a tool_use input compactly: prefer a "command" field
// (Bash), else the first string value, else the raw JSON, all single-lined.
func summarizeInput(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err == nil {
		for _, key := range []string{"command", "query", "file_path", "path", "pattern", "description"} {
			if v, ok := m[key].(string); ok && v != "" {
				return oneLine(truncate(v, 160))
			}
		}
	}
	return oneLine(truncate(string(raw), 160))
}

func splitLines(s string) []string {
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}

func indent(lines []string, pad string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = pad + l
	}
	return out
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + fmt.Sprintf(" …(+%d chars)", len(s)-n)
}
