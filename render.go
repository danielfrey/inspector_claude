package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"inspector_claude/internal/session"
)

// Line is one rendered display line, tagged by whether it belongs to the plain
// user<->assistant conversation (Tech=false) or to the machinery around it —
// tool calls, tool results, and Claude's thinking (Tech=true). The flag lets
// the viewer hide the technical lines and show only the conversation.
type Line struct {
	Text string
	Tech bool
}

// renderEntries turns a session's entries into tagged display lines. The TUI
// wraps, filters and highlights these; the plain-CLI `show` mode prints them.
func renderEntries(entries []session.Entry) []Line {
	var lines []Line
	conv := func(ss ...string) {
		for _, s := range ss {
			lines = append(lines, Line{s, false})
		}
	}
	tech := func(ss ...string) {
		for _, s := range ss {
			lines = append(lines, Line{s, true})
		}
	}

	for _, e := range entries {
		if e.Message == nil {
			continue
		}
		for _, b := range e.Message.Blocks() {
			switch b.Type {
			case "text":
				if strings.TrimSpace(b.Text) == "" {
					continue
				}
				if e.Type == "user" {
					conv("", "▶ YOU")
				} else {
					conv("", "● CLAUDE")
				}
				conv(splitLines(b.Text)...)
			case "thinking":
				txt := b.PlainText()
				if strings.TrimSpace(txt) == "" {
					continue
				}
				tech("", "  · thinking")
				tech(indent(splitLines(txt), "  ")...)
			case "tool_use":
				tech("", "  ⚙ "+b.Name+"  "+summarizeInput(b.Input))
			case "tool_result":
				txt := strings.TrimRight(b.PlainText(), "\n")
				if strings.TrimSpace(txt) == "" {
					continue
				}
				tech("  ⎿ result:")
				tech(indent(splitLines(truncate(txt, 4000)), "    ")...)
			}
		}
	}
	if len(lines) > 0 && lines[0].Text == "" {
		lines = lines[1:]
	}
	return lines
}

// lineTexts flattens rendered lines to strings, optionally dropping the
// technical ones. A leading blank left behind by filtering is trimmed.
func lineTexts(lines []Line, hideTech bool) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if hideTech && l.Tech {
			continue
		}
		out = append(out, l.Text)
	}
	for len(out) > 0 && out[0] == "" {
		out = out[1:]
	}
	return out
}

// summarizeInput renders a tool_use input compactly: prefer a "command" field
// (Bash), else another common string field, else the raw JSON, single-lined.
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
