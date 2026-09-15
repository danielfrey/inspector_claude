package main

import (
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
		{"▶ YOU", false},
		{"hello", false},
		{"  ⚙ Bash  echo hi", true},
		{"  ⎿ result:", true},
		{"    hi", true},
		{"● CLAUDE", false},
		{"world", false},
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
