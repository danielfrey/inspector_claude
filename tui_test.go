package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"inspector_claude/internal/session"

	tea "github.com/charmbracelet/bubbletea"
)

// fakeSessions builds two projects with a few sessions each (newest first), so
// the projects view has something to group without touching the real disk.
func fakeSessions() []session.Session {
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	mk := func(proj, id, title string, dayOffset int) session.Session {
		return session.Session{
			ID:          id,
			Project:     proj,
			ProjectPath: "/home/dev/" + proj,
			FirstPrompt: title,
			End:         base.AddDate(0, 0, dayOffset),
			ModTime:     base.AddDate(0, 0, dayOffset),
			MsgCount:    10,
		}
	}
	// alpha is the newer project (higher offsets)
	return []session.Session{
		mk("alpha", "a2", "newest alpha chat", 5),
		mk("alpha", "a1", "older alpha chat", 3),
		mk("beta", "b2", "newer beta chat", 2),
		mk("beta", "b1", "oldest beta chat", 1),
	}
}

func key(t tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: t} }

func drive(m model, msg tea.Msg) model {
	nm, _ := m.Update(msg)
	return nm.(model)
}

func TestProjectsViewNavigation(t *testing.T) {
	m := newModel(fakeSessions())
	m = drive(m, tea.WindowSizeMsg{Width: 120, Height: 40})

	if len(m.groups) < 2 {
		t.Fatalf("expected >= 2 project groups, got %d", len(m.groups))
	}
	// groups must be newest-first
	for i := 1; i < len(m.groups); i++ {
		if m.groups[i-1].Latest.Before(m.groups[i].Latest) {
			t.Fatalf("groups not sorted newest-first at %d", i)
		}
	}

	// switch to projects view
	m = drive(m, key(tea.KeyTab))
	if m.mode != modeProjects {
		t.Fatalf("tab did not enter projects view (mode=%d)", m.mode)
	}
	out := m.View()
	if !strings.Contains(out, "by project") {
		t.Fatalf("projects view header missing:\n%s", out)
	}

	// move down a project, then into the right pane, down a session, open it
	m = drive(m, key(tea.KeyDown))
	if m.projCursor != 1 {
		t.Fatalf("expected projCursor 1, got %d", m.projCursor)
	}
	m = drive(m, key(tea.KeyRight))
	if m.activePane != 1 {
		t.Fatalf("right arrow did not activate session pane")
	}
	m = drive(m, key(tea.KeyEnter))
	if m.mode != modeDetail {
		t.Fatalf("enter did not open detail (mode=%d)", m.mode)
	}
	if m.returnMode != modeProjects {
		t.Fatalf("detail should return to projects view, got %d", m.returnMode)
	}
	if len(m.View()) == 0 {
		t.Fatalf("empty detail render")
	}

	// esc returns to the projects view, not the flat list
	m = drive(m, key(tea.KeyEsc))
	if m.mode != modeProjects {
		t.Fatalf("esc from detail should return to projects, got %d", m.mode)
	}
}

func TestConversationFilter(t *testing.T) {
	lines := []Line{
		{"▶ YOU", kHeaderYou},
		{"hello", kText},
		{"  ⚙ Bash  echo hi", kTool},
		{"  ⎿ result:", kTool},
		{"    hi", kTool},
		{"● CLAUDE", kHeaderClaude},
		{"world", kText},
	}
	full := lineTexts(lines, false)
	conv := lineTexts(lines, true)
	if len(full) != 7 {
		t.Fatalf("full expected 7 lines, got %d", len(full))
	}
	if len(conv) != 4 {
		t.Fatalf("conversation expected 4 lines, got %d", len(conv))
	}
	for _, l := range conv {
		if strings.HasPrefix(l, "  ⚙") || strings.HasPrefix(l, "  ⎿") {
			t.Fatalf("technical line leaked into conversation view: %q", l)
		}
	}
}

func TestLiveFollow(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "projects", "-tmp-live")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "live.jsonl")
	head := `{"type":"user","sessionId":"live1","cwd":"/tmp/live","gitBranch":"main","timestamp":"2026-09-15T10:00:00.000Z","message":{"role":"user","content":"hi"}}` + "\n"
	if err := os.WriteFile(path, []byte(head), 0644); err != nil {
		t.Fatal(err)
	}

	ss, _ := session.Scan(filepath.Dir(dir))
	if len(ss) != 1 {
		t.Fatalf("expected 1 session, got %d", len(ss))
	}
	m := newModel(ss)
	m = drive(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m = drive(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.mode != modeDetail {
		t.Fatal("did not open detail")
	}
	if m.fileChanged() {
		t.Fatal("file reported changed immediately after open")
	}
	before := len(m.dLines)

	// a new turn is appended by some other Claude Code window
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	f.WriteString(`{"type":"assistant","sessionId":"live1","timestamp":"2026-09-15T10:00:05.000Z","message":{"role":"assistant","content":[{"type":"text","text":"a brand new answer line"}]}}` + "\n")
	f.Close()

	if !m.fileChanged() {
		t.Fatal("fileChanged did not detect the append")
	}
	m = drive(m, tickMsg(time.Now()))
	if len(m.dLines) <= before {
		t.Fatalf("follow did not grow the view: before=%d after=%d", before, len(m.dLines))
	}
	found := false
	for _, l := range m.dLines {
		if strings.Contains(l.Text, "brand new answer") {
			found = true
		}
	}
	if !found {
		t.Fatal("appended turn not visible after follow reload")
	}
}

func runes(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

func TestInChatSearchIndependent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "projects", "-tmp-search")
	os.MkdirAll(dir, 0755)
	content := `{"type":"user","sessionId":"s1","cwd":"/tmp/s","gitBranch":"main","timestamp":"2026-09-15T10:00:00.000Z","message":{"role":"user","content":"alpha question please"}}` + "\n" +
		`{"type":"assistant","sessionId":"s1","timestamp":"2026-09-15T10:00:01.000Z","message":{"role":"assistant","content":[{"type":"text","text":"beta answer with alpha again"}]}}` + "\n"
	os.WriteFile(filepath.Join(dir, "s1.jsonl"), []byte(content), 0644)

	ss, _ := session.Scan(filepath.Dir(dir))
	m := newModel(ss)
	m = drive(m, tea.WindowSizeMsg{Width: 80, Height: 24})

	// global search for "alpha", then open the (only) matching session
	m = drive(m, runes("alpha"))
	if len(m.filtered) != 1 {
		t.Fatalf("global search expected 1 session, got %d", len(m.filtered))
	}
	m = drive(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.mode != modeDetail || m.detailQuery != "alpha" {
		t.Fatalf("detail did not adopt global query: mode=%d q=%q", m.mode, m.detailQuery)
	}
	alphaMatches := len(m.matches)

	// in-chat search: "/" then type "beta"
	m = drive(m, runes("/"))
	if !m.isearch {
		t.Fatal("/ did not start in-chat search")
	}
	m = drive(m, runes("beta"))
	if m.detailQuery != "beta" {
		t.Fatalf("in-chat query not applied: %q", m.detailQuery)
	}
	if len(m.matches) == 0 || len(m.matches) >= alphaMatches {
		t.Fatalf("beta should match fewer lines than alpha (alpha=%d beta=%d)", alphaMatches, len(m.matches))
	}
	// global search must be untouched
	if m.input.Value() != "alpha" {
		t.Fatalf("global search was modified by in-chat search: %q", m.input.Value())
	}
	m = drive(m, tea.KeyMsg{Type: tea.KeyEnter}) // commit
	if m.isearch {
		t.Fatal("enter did not close in-chat search")
	}

	// back to list: the global filter still applies
	m = drive(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.mode != modeList || len(m.filtered) != 1 {
		t.Fatalf("global filter lost after leaving chat: mode=%d n=%d", m.mode, len(m.filtered))
	}

	// re-open: in-chat search cancel (esc) restores the adopted global query
	m = drive(m, tea.KeyMsg{Type: tea.KeyEnter})
	m = drive(m, runes("/"))
	m = drive(m, runes("beta"))
	m = drive(m, tea.KeyMsg{Type: tea.KeyEsc}) // cancel
	if m.detailQuery != "alpha" {
		t.Fatalf("esc did not restore adopted query, got %q", m.detailQuery)
	}
}

func TestMarkdownRendering(t *testing.T) {
	// table detection
	block := markdownBlock("| A | B |\n|---|---|\n| 1 | 2 |")
	var kinds []lineKind
	for _, l := range block {
		kinds = append(kinds, l.Kind)
	}
	if len(kinds) != 3 || kinds[0] != kTableHead || kinds[1] != kTableSep || kinds[2] != kTableRow {
		t.Fatalf("table not recognized: %+v", block)
	}

	// fenced code
	code := markdownBlock("text\n```go\nx := 1\n```\nmore")
	var codeLines int
	for _, l := range code {
		if l.Kind == kCode {
			codeLines++
			if strings.Contains(l.Text, "```") {
				t.Fatalf("fence marker leaked into code line: %q", l.Text)
			}
		}
	}
	if codeLines != 1 {
		t.Fatalf("expected 1 code line, got %d", codeLines)
	}

	// inline bold + code produce styled spans and search overlay splits them
	spans := overlay(inlineSpans("use **bold** and `tab` now"), "tab")
	var hasMatch bool
	for _, sp := range spans {
		if sp.text == "tab" {
			hasMatch = true
		}
	}
	if !hasMatch {
		t.Fatalf("search term not isolated into its own span: %+v", spans)
	}
}
