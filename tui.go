package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

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
	stSelIdle   = lipgloss.NewStyle().Foreground(cAccent).Background(lipgloss.AdaptiveColor{Light: "#dbe7f3", Dark: "#33383f"})
	stHit       = lipgloss.NewStyle().Bold(true)
	stMatch     = lipgloss.NewStyle().Foreground(lipgloss.Color("#000000")).Background(lipgloss.AdaptiveColor{Light: "#ffe066", Dark: "#ffd33d"})
	stUserHdr   = lipgloss.NewStyle().Bold(true).Foreground(cUser)
	stClaudeHdr = lipgloss.NewStyle().Bold(true).Foreground(cClaude)
	stToolHdr   = lipgloss.NewStyle().Foreground(cTool)
	stThink     = lipgloss.NewStyle().Foreground(cDim).Italic(true)
)

type mode int

const (
	modeList     mode = iota // flat chronological list across all projects
	modeProjects             // two-pane: projects left, that project's sessions right
	modeDetail               // one session's chat
)

// projGroup is the sessions of one project (directory), for the projects view.
type projGroup struct {
	Name     string
	Path     string
	Latest   time.Time
	Sessions []session.Session
}

type model struct {
	all      []session.Session
	filtered []session.Session
	input    textinput.Model
	cursor   int // index into filtered
	top      int // first visible row in list
	mode     mode
	w, h     int

	// projects view
	groups     []projGroup
	projCursor int
	sessCursor int
	activePane int // 0 = projects (left), 1 = sessions (right)

	// detail state
	dTitle     string
	rawLines   []Line   // tagged source lines (kept to re-wrap/re-filter cheaply)
	dLines     []string // unstyled, wrapped display lines (after tech filter)
	dTop       int
	matches    []int // indices into dLines containing the query
	matchPos   int
	hideTech   bool // hide tool calls / results / thinking -> conversation only
	returnMode mode // browse mode to return to when leaving detail
	err        error
}

func newModel(sessions []session.Session) model {
	ti := textinput.New()
	ti.Placeholder = "search all transcripts…"
	ti.Prompt = "🔍 "
	ti.Focus()
	m := model{all: sessions, input: ti}
	m.refilter()
	return m
}

func (m model) Init() tea.Cmd { return textinput.Blink }

func (m *model) query() string { return strings.ToLower(strings.TrimSpace(m.input.Value())) }

func (m *model) refilter() {
	m.filtered = filterSessions(m.all, m.input.Value())
	m.cursor, m.top = 0, 0
	m.groups = groupsOf(m.filtered)
	m.projCursor, m.sessCursor, m.activePane = 0, 0, 0
}

// groupsOf buckets sessions by project directory, newest project first. Input
// is assumed already sorted newest-first, so each bucket stays newest-first too.
func groupsOf(sessions []session.Session) []projGroup {
	idx := map[string]int{}
	var groups []projGroup
	for _, s := range sessions {
		key := s.ProjectPath
		if key == "" {
			key = s.Project
		}
		i, ok := idx[key]
		if !ok {
			i = len(groups)
			idx[key] = i
			groups = append(groups, projGroup{Name: s.Project, Path: key})
		}
		groups[i].Sessions = append(groups[i].Sessions, s)
		if t := s.SortTime(); t.After(groups[i].Latest) {
			groups[i].Latest = t
		}
	}
	sort.SliceStable(groups, func(a, b int) bool { return groups[a].Latest.After(groups[b].Latest) })
	return groups
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
		switch m.mode {
		case modeDetail:
			return m.updateDetail(msg)
		case modeProjects:
			return m.updateProjects(msg)
		default:
			return m.updateList(msg)
		}
	}
	return m, nil
}

func (m model) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "tab":
		m.mode = modeProjects
		return m, nil
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

func (m model) updateProjects(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "tab":
		m.mode = modeList
		return m, nil
	case "esc":
		if m.input.Value() != "" {
			m.input.SetValue("")
			m.refilter()
			return m, nil
		}
		return m, tea.Quit
	case "left":
		m.activePane = 0
		return m, nil
	case "right":
		if m.curSessions() > 0 {
			m.activePane = 1
		}
		return m, nil
	case "up", "ctrl+p":
		m.moveProjects(-1)
		return m, nil
	case "down", "ctrl+n":
		m.moveProjects(1)
		return m, nil
	case "enter":
		if m.activePane == 0 {
			if m.curSessions() > 0 {
				m.activePane = 1
			}
			return m, nil
		}
		if m.curSessions() > 0 {
			m.openDetail(m.groups[m.projCursor].Sessions[m.sessCursor])
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.refilter()
	return m, cmd
}

// curSessions is the session count of the currently selected project (0-safe).
func (m *model) curSessions() int {
	if m.projCursor < 0 || m.projCursor >= len(m.groups) {
		return 0
	}
	return len(m.groups[m.projCursor].Sessions)
}

func (m *model) moveProjects(d int) {
	if len(m.groups) == 0 {
		return
	}
	if m.activePane == 0 {
		m.projCursor = clamp(m.projCursor+d, 0, len(m.groups)-1)
		m.sessCursor = 0 // reset session selection when switching projects
	} else {
		m.sessCursor = clamp(m.sessCursor+d, 0, m.curSessions()-1)
	}
}

func (m model) updateDetail(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	page := m.detailRows()
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "q", "backspace", "left", "h":
		m.mode = m.returnMode
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
	case "t":
		m.hideTech = !m.hideTech
		m.rebuildDetail()
		m.dTop = m.clampTop(m.dTop)
	}
	return m, nil
}

func (m *model) openDetail(s session.Session) {
	entries, err := session.ReadEntries(s.Path)
	m.err = err
	m.dTitle = fmt.Sprintf("%s  ·  %s  ·  %s", s.Project, s.ID, s.Branch)
	m.rawLines = renderEntries(entries)
	m.returnMode = m.mode
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
	for _, l := range lineTexts(m.rawLines, m.hideTech) {
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
	switch m.mode {
	case modeDetail:
		return m.viewDetail()
	case modeProjects:
		return m.viewProjects()
	default:
		return m.viewList()
	}
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
	b.WriteString(stDim.Render("↑↓ move · enter open · tab projects view · type to search · esc clear/quit"))
	return b.String()
}

// viewProjects is the two-pane browser: projects (newest first) on the left,
// the selected project's sessions on the right.
func (m model) viewProjects() string {
	var b strings.Builder
	header := stTitle.Render("inspector — by project")
	count := stDim.Render(fmt.Sprintf("  %d projects · %d sessions", len(m.groups), len(m.filtered)))
	b.WriteString(header + count + "\n")
	b.WriteString(m.input.View() + "\n")
	b.WriteString(stDim.Render(strings.Repeat("─", m.w)) + "\n")

	rows := m.listRows()
	leftW := m.w * 2 / 5
	leftW = clamp(leftW, 16, 40)
	rightW := m.w - leftW - 3
	if rightW < 10 {
		rightW = 10
	}

	left := m.projectColumn(rows, leftW)
	right := m.sessionColumn(rows, rightW)
	sep := stDim.Render("│")
	for i := 0; i < rows; i++ {
		b.WriteString(left[i] + " " + sep + " " + right[i] + "\n")
	}
	b.WriteString(stDim.Render(strings.Repeat("─", m.w)) + "\n")
	b.WriteString(stDim.Render("tab list · ←/→ pane · ↑↓ move · enter open · type search · esc quit"))
	return b.String()
}

// projectColumn renders exactly `rows` fixed-width (`w`) lines for the left pane.
func (m model) projectColumn(rows, w int) []string {
	out := make([]string, 0, rows)
	top := scrollTop(m.projCursor, rows, len(m.groups))
	end := min(top+rows, len(m.groups))
	for i := top; i < end; i++ {
		g := m.groups[i]
		line := fitPlain(fmt.Sprintf("%s  %s (%d)", g.Latest.Format("2006-01-02"), g.Name, len(g.Sessions)), w)
		out = append(out, m.styleCell(line, i == m.projCursor, 0))
	}
	return padCol(out, rows, w)
}

// sessionColumn renders the right pane: the selected project's sessions.
func (m model) sessionColumn(rows, w int) []string {
	out := make([]string, 0, rows)
	if m.curSessions() == 0 {
		return padCol(out, rows, w)
	}
	sessions := m.groups[m.projCursor].Sessions
	q := m.query()
	top := scrollTop(m.sessCursor, rows, len(sessions))
	end := min(top+rows, len(sessions))
	for i := top; i < end; i++ {
		s := sessions[i]
		hits := ""
		if q != "" {
			hits = fmt.Sprintf(" [%d]", s.Matches(q))
		}
		line := fitPlain(fmt.Sprintf("%s  %s%s", s.SortTime().Format("2006-01-02"), oneLine(s.Title()), hits), w)
		out = append(out, m.styleCell(line, i == m.sessCursor, 1))
	}
	return padCol(out, rows, w)
}

// styleCell highlights a selected cell, strongly if its pane is active, dimly if
// not (so you can see the remembered selection in the inactive pane).
func (m model) styleCell(line string, selected bool, pane int) string {
	if !selected {
		return line
	}
	if m.activePane == pane {
		return stSel.Render(line)
	}
	return stSelIdle.Render(line)
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
	viewMode := "full"
	if m.hideTech {
		viewMode = "conversation only"
	}
	b.WriteString(stDim.Render(fmt.Sprintf("line %d/%d%s  ·  %s", m.dTop+1, len(m.dLines), pos, viewMode)) + "\n")
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
	b.WriteString(stDim.Render("↑↓/jk scroll · space page · n/N match · t tech on/off · g/G top/bottom · esc back"))
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

// scrollTop keeps `cursor` visible within a window of `rows` over `n` items.
func scrollTop(cursor, rows, n int) int {
	if cursor < rows {
		return 0
	}
	top := cursor - rows + 1
	if top > n-rows {
		top = n - rows
	}
	if top < 0 {
		top = 0
	}
	return top
}

// fitPlain truncates or space-pads s to exactly w visible runes (assumes
// single-width runes, which holds for dates and Latin project/session names).
func fitPlain(s string, w int) string {
	if w <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) > w {
		if w == 1 {
			return string(r[:1])
		}
		return string(r[:w-1]) + "…"
	}
	return s + strings.Repeat(" ", w-len(r))
}

// padCol pads a column to `rows` lines, each a blank cell of width w.
func padCol(col []string, rows, w int) []string {
	blank := strings.Repeat(" ", w)
	for len(col) < rows {
		col = append(col, blank)
	}
	return col
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

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
