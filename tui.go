package main

import (
	"fmt"
	"strings"

	"inspector_claude/internal/session"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// --- styles (adaptive: readable in light and dark terminals) ---------------

var (
	cAccent = lipgloss.AdaptiveColor{Light: "#0059b3", Dark: "#6cb6ff"}
	cDim    = lipgloss.AdaptiveColor{Light: "#6a6a6a", Dark: "#9a9a9a"}
	cUser   = lipgloss.AdaptiveColor{Light: "#0a7d1a", Dark: "#7ee787"}
	cClaude = lipgloss.AdaptiveColor{Light: "#8250df", Dark: "#c297ff"}
	cTool   = lipgloss.AdaptiveColor{Light: "#b35900", Dark: "#ffab70"}

	stTitle     = lipgloss.NewStyle().Bold(true).Foreground(cAccent)
	stDim       = lipgloss.NewStyle().Foreground(cDim)
	stSel       = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ffffff")).Background(cAccent)
	stHit       = lipgloss.NewStyle().Bold(true)
	stMatch     = lipgloss.NewStyle().Foreground(lipgloss.Color("#000000")).Background(lipgloss.AdaptiveColor{Light: "#ffe066", Dark: "#ffd33d"})
	stUserHdr   = lipgloss.NewStyle().Bold(true).Foreground(cUser)
	stClaudeHdr = lipgloss.NewStyle().Bold(true).Foreground(cClaude)
	stToolHdr   = lipgloss.NewStyle().Foreground(cTool)
	stThink     = lipgloss.NewStyle().Foreground(cDim).Italic(true)
)

type mode int

const (
	modeList mode = iota
	modeDetail
)

type model struct {
	all      []session.Session
	filtered []session.Session
	input    textinput.Model
	cursor   int // index into filtered
	top      int // first visible row in list
	mode     mode
	w, h     int

	// detail state
	dTitle   string
	rawLines []string // unstyled, unwrapped source lines (kept to re-wrap on resize)
	dLines   []string // unstyled, wrapped display lines
	dTop     int
	matches  []int // indices into dLines containing the query
	matchPos int
	err      error
}

func newModel(sessions []session.Session) model {
	ti := textinput.New()
	ti.Placeholder = "search all transcripts…"
	ti.Prompt = "🔍 "
	ti.Focus()
	return model{
		all:      sessions,
		filtered: sessions,
		input:    ti,
	}
}

func (m model) Init() tea.Cmd { return textinput.Blink }

func (m *model) query() string { return strings.ToLower(strings.TrimSpace(m.input.Value())) }

func (m *model) refilter() {
	m.filtered = filterSessions(m.all, m.input.Value())
	m.cursor, m.top = 0, 0
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		if m.mode == modeDetail {
			m.rebuildDetail() // re-wrap to new width
		}
		return m, nil
	case tea.KeyMsg:
		if m.mode == modeDetail {
			return m.updateDetail(msg)
		}
		return m.updateList(msg)
	}
	return m, nil
}

func (m model) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		if m.input.Value() != "" {
			m.input.SetValue("")
			m.refilter()
			return m, nil
		}
		return m, tea.Quit
	case "up", "ctrl+p":
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil
	case "down", "ctrl+n":
		if m.cursor < len(m.filtered)-1 {
			m.cursor++
		}
		return m, nil
	case "pgup":
		m.cursor -= m.listRows()
		if m.cursor < 0 {
			m.cursor = 0
		}
		return m, nil
	case "pgdown":
		m.cursor += m.listRows()
		if m.cursor > len(m.filtered)-1 {
			m.cursor = len(m.filtered) - 1
		}
		return m, nil
	case "enter":
		if len(m.filtered) > 0 {
			m.openDetail(m.filtered[m.cursor])
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.refilter()
	return m, cmd
}

func (m model) updateDetail(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	page := m.detailRows()
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "q", "backspace", "left", "h":
		m.mode = modeList
		return m, nil
	case "up", "k":
		m.dTop = max(0, m.dTop-1)
	case "down", "j":
		m.dTop = m.clampTop(m.dTop + 1)
	case "pgup", "ctrl+b":
		m.dTop = max(0, m.dTop-page)
	case "pgdown", "ctrl+f", " ":
		m.dTop = m.clampTop(m.dTop + page)
	case "g", "home":
		m.dTop = 0
	case "G", "end":
		m.dTop = m.clampTop(len(m.dLines))
	case "n":
		m.jumpMatch(1)
	case "N":
		m.jumpMatch(-1)
	}
	return m, nil
}

func (m *model) openDetail(s session.Session) {
	entries, err := session.ReadEntries(s.Path)
	m.err = err
	m.dTitle = fmt.Sprintf("%s  ·  %s  ·  %s", s.Project, s.ID, s.Branch)
	m.rawLines = renderEntries(entries)
	m.mode = modeDetail
	m.dTop, m.matchPos = 0, 0
	m.rebuildDetail()
	if len(m.matches) > 0 {
		m.dTop = m.clampTop(m.matches[0])
	}
}

// rawLines is kept so width changes can re-wrap without re-reading the file.
// (declared here to keep openDetail readable)
func (m *model) rebuildDetail() {
	width := m.w - 2
	if width < 20 {
		width = 20
	}
	var wrapped []string
	for _, l := range m.rawLines {
		wrapped = append(wrapped, wrap(l, width)...)
	}
	m.dLines = wrapped
	m.matches = m.matches[:0]
	if q := m.query(); q != "" {
		for i, l := range m.dLines {
			if strings.Contains(strings.ToLower(l), q) {
				m.matches = append(m.matches, i)
			}
		}
	}
}

func (m *model) jumpMatch(dir int) {
	if len(m.matches) == 0 {
		return
	}
	m.matchPos = (m.matchPos + dir + len(m.matches)) % len(m.matches)
	m.dTop = m.clampTop(m.matches[m.matchPos])
}

// --- view ------------------------------------------------------------------

func (m model) View() string {
	if m.w == 0 {
		return "loading…"
	}
	if m.mode == modeDetail {
		return m.viewDetail()
	}
	return m.viewList()
}

func (m model) viewList() string {
	var b strings.Builder
	header := stTitle.Render("inspector — Claude Code transcripts")
	count := stDim.Render(fmt.Sprintf("  %d / %d sessions", len(m.filtered), len(m.all)))
	b.WriteString(header + count + "\n")
	b.WriteString(m.input.View() + "\n")
	b.WriteString(stDim.Render(strings.Repeat("─", m.w)) + "\n")

	rows := m.listRows()
	if m.cursor < m.top {
		m.top = m.cursor
	}
	if m.cursor >= m.top+rows {
		m.top = m.cursor - rows + 1
	}
	q := m.query()
	end := min(m.top+rows, len(m.filtered))
	for i := m.top; i < end; i++ {
		b.WriteString(m.renderRow(m.filtered[i], i == m.cursor, q) + "\n")
	}
	for i := end; i < m.top+rows; i++ {
		b.WriteString("\n")
	}
	b.WriteString(stDim.Render(strings.Repeat("─", m.w)) + "\n")
	b.WriteString(stDim.Render("↑↓ move · enter open · type to search · esc clear/quit"))
	return b.String()
}

func (m model) renderRow(s session.Session, selected bool, q string) string {
	date := "          "
	if !s.SortTime().IsZero() {
		date = s.SortTime().Format("2006-01-02")
	}
	hits := ""
	if q != "" {
		hits = stHit.Render(fmt.Sprintf(" [%d]", s.Matches(q)))
	}
	meta := fmt.Sprintf("%s  %-16s %3dm", date, trunc(s.Project, 16), s.MsgCount)
	title := oneLine(s.Title())
	// budget the title to the remaining width
	avail := m.w - lipgloss.Width(meta) - lipgloss.Width(stripANSI(hits)) - 4
	if avail < 10 {
		avail = 10
	}
	line := fmt.Sprintf("%s  %s%s", meta, trunc(title, avail), hits)
	if selected {
		return stSel.Render(trunc(line, m.w))
	}
	return stDim.Render(meta) + "  " + trunc(title, avail) + hits
}

func (m model) viewDetail() string {
	var b strings.Builder
	b.WriteString(stTitle.Render(trunc(m.dTitle, m.w)) + "\n")
	pos := ""
	if len(m.matches) > 0 {
		pos = fmt.Sprintf("  match %d/%d", m.matchPos+1, len(m.matches))
	}
	b.WriteString(stDim.Render(fmt.Sprintf("line %d/%d%s", m.dTop+1, len(m.dLines), pos)) + "\n")
	b.WriteString(stDim.Render(strings.Repeat("─", m.w)) + "\n")

	rows := m.detailRows()
	q := m.query()
	end := min(m.dTop+rows, len(m.dLines))
	for i := m.dTop; i < end; i++ {
		b.WriteString(styleLine(m.dLines[i], q) + "\n")
	}
	for i := end; i < m.dTop+rows; i++ {
		b.WriteString("\n")
	}
	b.WriteString(stDim.Render(strings.Repeat("─", m.w)) + "\n")
	b.WriteString(stDim.Render("↑↓/jk scroll · space page · n/N next/prev match · g/G top/bottom · esc back"))
	return b.String()
}

// styleLine colors a rendered transcript line by its role marker and highlights
// query matches within it.
func styleLine(l, q string) string {
	styled := l
	switch {
	case strings.HasPrefix(l, "▶ YOU"):
		styled = stUserHdr.Render(l)
	case strings.HasPrefix(l, "● CLAUDE"):
		styled = stClaudeHdr.Render(l)
	case strings.HasPrefix(l, "  ⚙"):
		styled = stToolHdr.Render(l)
	case strings.HasPrefix(l, "  · thinking") || strings.HasPrefix(l, "  ⎿"):
		styled = stThink.Render(l)
	}
	if q == "" {
		return styled
	}
	return highlight(l, q) // highlight overrides role color to keep matches visible
}

// highlight wraps every case-insensitive occurrence of q in l with stMatch.
func highlight(l, q string) string {
	low := strings.ToLower(l)
	var b strings.Builder
	for {
		idx := strings.Index(low, q)
		if idx < 0 {
			b.WriteString(l)
			break
		}
		b.WriteString(l[:idx])
		b.WriteString(stMatch.Render(l[idx : idx+len(q)]))
		l = l[idx+len(q):]
		low = low[idx+len(q):]
	}
	return b.String()
}

// --- geometry helpers ------------------------------------------------------

func (m model) listRows() int   { return max(1, m.h-5) }
func (m model) detailRows() int { return max(1, m.h-5) }

func (m model) clampTop(t int) int {
	maxTop := len(m.dLines) - m.detailRows()
	if maxTop < 0 {
		maxTop = 0
	}
	if t > maxTop {
		t = maxTop
	}
	if t < 0 {
		t = 0
	}
	return t
}

// wrap word-wraps s to width, preserving leading indentation of the source line.
func wrap(s string, width int) []string {
	if s == "" {
		return []string{""}
	}
	indent := s[:len(s)-len(strings.TrimLeft(s, " "))]
	if len(indent) >= width {
		indent = ""
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return []string{""}
	}
	var out []string
	line := indent
	for _, w := range words {
		switch {
		case line == indent && line == "":
			line = w
		case len(line)+1+len(w) <= width || strings.TrimSpace(line) == "":
			if strings.TrimSpace(line) == "" {
				line += w
			} else {
				line += " " + w
			}
		default:
			out = append(out, line)
			line = indent + w
		}
		// hard-break a single word longer than width
		for len(line) > width {
			out = append(out, line[:width])
			line = indent + line[width:]
		}
	}
	out = append(out, line)
	return out
}

func stripANSI(s string) string { return s } // hits already width-safe; placeholder

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
