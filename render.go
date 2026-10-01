package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"inspector_claude/internal/session"
)

// lineKind classifies a rendered line so the viewer can style and filter it.
type lineKind int

const (
	kText         lineKind = iota // plain conversation text (inline markdown at draw time)
	kHeaderYou                    // "▶ YOU" role header
	kHeaderClaude                 // "● CLAUDE" role header
	kThinking                     // Claude's thinking (technical)
	kTool                         // tool call / tool result (technical)
	kCode                         // fenced code block content (verbatim)
	kTableHead                    // markdown table header row
	kTableRow                     // markdown table data row
	kTableSep                     // markdown table separator row
)

func (k lineKind) tech() bool { return k == kThinking || k == kTool }

// Line is one rendered display line plus its kind. The kind drives styling
// (in the TUI) and the conversation-only filter (hide technical lines). Step
// marks lines that belong to intermediate Claude narration (not a final
// answer), so the TUI's most condensed detail level can drop them.
type Line struct {
	Text string
	Kind lineKind
	Step bool
}

// renderEntries turns a session's entries into tagged display lines.
func renderEntries(entries []session.Entry) []Line {
	isAnswer := answerTextBlocks(entries)
	asstN := 0
	var lines []Line
	add := func(ss []Line) { lines = append(lines, ss...) }
	one := func(text string, k lineKind) { lines = append(lines, Line{Text: text, Kind: k}) }

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
					one("", kText)
					one("▶ YOU", kHeaderYou)
					add(markdownBlock(b.Text))
				} else {
					// A Claude text block is either a final answer or intermediate
					// narration; narration lines are tagged Step so the TUI's most
					// condensed level can hide them (mirrors the HTML view).
					step := !isAnswer[asstN]
					asstN++
					start := len(lines)
					one("", kText)
					one("● CLAUDE", kHeaderClaude)
					add(markdownBlock(b.Text))
					if step {
						for i := start; i < len(lines); i++ {
							lines[i].Step = true
						}
					}
				}
			case "thinking":
				txt := b.PlainText()
				if strings.TrimSpace(txt) == "" {
					continue
				}
				one("", kThinking)
				one("  · thinking", kThinking)
				for _, l := range indent(splitLines(txt), "  ") {
					one(l, kThinking)
				}
			case "tool_use":
				one("", kTool)
				one("  ⚙ "+b.Name+"  "+summarizeInput(b.Input), kTool)
			case "tool_result":
				txt := strings.TrimRight(b.PlainText(), "\n")
				if strings.TrimSpace(txt) == "" {
					continue
				}
				one("  ⎿ result:", kTool)
				for _, l := range indent(splitLines(truncate(txt, 4000)), "    ") {
					one(l, kTool)
				}
			}
		}
	}
	for len(lines) > 0 && lines[0].Text == "" {
		lines = lines[1:]
	}
	return lines
}

// markdownBlock splits a text block into lines, recognizing fenced code blocks
// and markdown tables (which need multi-line context); inline markup (bold,
// code, headings, lists) is handled per-line at draw time.
func markdownBlock(text string) []Line {
	src := strings.Split(strings.TrimRight(text, "\n"), "\n")
	var out []Line
	inFence := false
	for i := 0; i < len(src); i++ {
		line := src[i]
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue // drop the fence markers themselves
		}
		if inFence {
			out = append(out, Line{Text: line, Kind: kCode})
			continue
		}
		if strings.Contains(line, "|") && i+1 < len(src) && isTableSep(src[i+1]) {
			tbl, consumed := buildTable(src[i:])
			out = append(out, tbl...)
			i += consumed - 1
			continue
		}
		out = append(out, Line{Text: line, Kind: kText})
	}
	return out
}

// isTableSep reports whether a line is a markdown table separator, e.g.
// "|---|:--:|" or "--- | ---".
func isTableSep(s string) bool {
	t := strings.TrimSpace(s)
	if t == "" || !strings.Contains(t, "-") {
		return false
	}
	for _, r := range t {
		if r != '|' && r != '-' && r != ':' && r != ' ' {
			return false
		}
	}
	return true
}

// buildTable formats a markdown table (starting at src[0], separator at src[1])
// into aligned, box-drawn lines. It returns the emitted lines and how many
// source lines it consumed.
func buildTable(src []string) ([]Line, int) {
	header := splitCells(src[0])
	var rows [][]string
	consumed := 2 // header + separator
	for i := 2; i < len(src); i++ {
		if !strings.Contains(src[i], "|") {
			break
		}
		rows = append(rows, splitCells(src[i]))
		consumed++
	}

	ncol := len(header)
	for _, r := range rows {
		if len(r) > ncol {
			ncol = len(r)
		}
	}
	widths := make([]int, ncol)
	measure := func(cells []string) {
		for i := 0; i < ncol; i++ {
			if i < len(cells) {
				if w := utf8.RuneCountInString(cells[i]); w > widths[i] {
					widths[i] = w
				}
			}
		}
	}
	measure(header)
	for _, r := range rows {
		measure(r)
	}

	pad := func(cells []string) string {
		parts := make([]string, ncol)
		for i := 0; i < ncol; i++ {
			c := ""
			if i < len(cells) {
				c = cells[i]
			}
			parts[i] = c + strings.Repeat(" ", widths[i]-utf8.RuneCountInString(c))
		}
		return strings.Join(parts, " │ ")
	}
	sepParts := make([]string, ncol)
	for i := range sepParts {
		sepParts[i] = strings.Repeat("─", widths[i])
	}

	out := []Line{
		{Text: pad(header), Kind: kTableHead},
		{Text: strings.Join(sepParts, "─┼─"), Kind: kTableSep},
	}
	for _, r := range rows {
		out = append(out, Line{Text: pad(r), Kind: kTableRow})
	}
	return out, consumed
}

// splitCells splits a markdown table row into trimmed, marker-stripped cells.
func splitCells(line string) []string {
	t := strings.TrimSpace(line)
	t = strings.TrimPrefix(t, "|")
	t = strings.TrimSuffix(t, "|")
	parts := strings.Split(t, "|")
	for i := range parts {
		parts[i] = stripMarkers(strings.TrimSpace(parts[i]))
	}
	return parts
}

// stripMarkers removes inline markdown emphasis markers, for width and for
// plain (table-cell) display.
func stripMarkers(s string) string {
	return strings.NewReplacer("**", "", "`", "", "*", "").Replace(s)
}

// lineTexts flattens rendered lines to strings, optionally dropping technical
// ones. A leading blank left behind by filtering is trimmed.
func lineTexts(lines []Line, hideTech bool) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if hideTech && l.Kind.tech() {
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
