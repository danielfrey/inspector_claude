package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"inspector_claude/internal/session"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type fileStat struct {
	mod  time.Time
	size int64
}

func statFile(p string) (fileStat, error) {
	fi, err := os.Stat(p)
	if err != nil {
		return fileStat{}, err
	}
	return fileStat{fi.ModTime(), fi.Size()}, nil
}

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

	// inline markdown styles
	stPlain     = lipgloss.NewStyle()
	stBold      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "#111111", Dark: "#ffffff"})
	stItalic    = lipgloss.NewStyle().Italic(true)
	stCode      = lipgloss.NewStyle().Foreground(cAccent)
	stCodeBlock = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#6a737d", Dark: "#b7c0cc"})
	stHeading   = lipgloss.NewStyle().Bold(true).Foreground(cAccent)
	stBullet    = lipgloss.NewStyle().Foreground(cAccent)
	stQuote     = lipgloss.NewStyle().Foreground(cDim).Italic(true)
	stTableHead = lipgloss.NewStyle().Bold(true)
	stLive      = lipgloss.NewStyle().Bold(true).Foreground(cUser)
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
	rawLines   []Line // tagged source lines (kept to re-wrap/re-filter cheaply)
	dLines     []Line // wrapped display lines (after tech filter)
	dTop       int
	matches    []int // indices into dLines containing the query
	matchPos   int
	hideTech   bool // hide tool calls / results / thinking -> conversation only
	returnMode mode // browse mode to return to when leaving detail
	err        error

	// live-follow state (detail view tails the file as it grows; read-only)
	curPath string
	curMod  time.Time
	curSize int64
	follow  bool
}

func newModel(sessions []session.Session) model {
	ti := textinput.New()
	ti.Placeholder = "search all transcripts…"
	ti.Prompt = "🔍 "
	ti.Focus()
	m := model{all: sessions, input: ti, follow: true}
	m.refilter()
	return m
}

// tickMsg drives the live-follow poll while a session is open.
type tickMsg time.Time

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m model) Init() tea.Cmd { return tea.Batch(textinput.Blink, tick()) }

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
	case tickMsg:
		if m.mode == modeDetail && m.follow && m.fileChanged() {
			m.reloadDetail()
		}
		return m, tick() // keep the single poll loop alive
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
	case "r":
		m.reloadDetail() // manual reload (Cmd-R is captured by the terminal/OS)
	case "f":
		m.follow = !m.follow
		if m.follow && m.fileChanged() {
			m.reloadDetail()
		}
	}
	return m, nil
}

func (m *model) openDetail(s session.Session) {
	entries, err := session.ReadEntries(s.Path)
	m.err = err
	m.curPath = s.Path
	m.dTitle = fmt.Sprintf("%s  ·  %s  ·  %s", s.Project, s.ID, s.Branch)
	m.rawLines = renderEntries(entries)
	m.returnMode = m.mode
	m.mode = modeDetail
	m.dTop, m.matchPos = 0, 0
	m.rebuildDetail()
	if len(m.matches) > 0 {
		m.dTop = m.clampTop(m.matches[0])
	}
	m.snapshotStat()
}

// rebuildDetail re-filters and re-wraps rawLines into display lines. Prose is
// word-wrapped; code and table lines are kept intact (hard-cut if too wide).
func (m *model) rebuildDetail() {
	width := m.w - 2
	if width < 20 {
		width = 20
	}
	var out []Line
	for _, l := range m.rawLines {
		if m.hideTech && l.Kind.tech() {
			continue
		}
		switch l.Kind {
		case kCode, kTableHead, kTableRow, kTableSep:
			out = append(out, Line{fitPlain(l.Text, width), l.Kind})
		default:
			for _, w := range wrap(l.Text, width) {
				out = append(out, Line{w, l.Kind})
			}
		}
	}
	for len(out) > 0 && out[0].Text == "" {
		out = out[1:]
	}
	m.dLines = out

	m.matches = m.matches[:0]
	if q := m.query(); q != "" {
		for i, l := range m.dLines {
			if strings.Contains(strings.ToLower(l.Text), q) {
				m.matches = append(m.matches, i)
			}
		}
	}
}

// --- live follow (read-only file tailing) ----------------------------------

func (m *model) snapshotStat() {
	if fi, err := statFile(m.curPath); err == nil {
		m.curMod, m.curSize = fi.mod, fi.size
	}
}

// fileChanged reports whether the open transcript has grown or been touched
// since the last snapshot.
func (m *model) fileChanged() bool {
	fi, err := statFile(m.curPath)
	if err != nil {
		return false
	}
	return fi.size != m.curSize || !fi.mod.Equal(m.curMod)
}

// reloadDetail re-reads the transcript, preserving scroll — but sticking to the
// bottom if we were already there, so the view tails new turns live.
func (m *model) reloadDetail() {
	atBottom := m.dTop >= m.maxTop()
	entries, err := session.ReadEntries(m.curPath)
	m.err = err
	m.rawLines = renderEntries(entries)
	m.rebuildDetail()
	if atBottom {
		m.dTop = m.maxTop()
	} else {
		m.dTop = m.clampTop(m.dTop)
	}
	m.snapshotStat()
}

func (m *model) maxTop() int {
	if t := len(m.dLines) - m.detailRows(); t > 0 {
		return t
	}
	return 0
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
	status := stDim.Render(fmt.Sprintf("line %d/%d%s  ·  %s  ·  ", m.dTop+1, len(m.dLines), pos, viewMode))
	if m.follow {
		status += stLive.Render("● live")
	} else {
		status += stDim.Render("paused")
	}
	b.WriteString(status + "\n")
	b.WriteString(stDim.Render(strings.Repeat("─", m.w)) + "\n")

	rows := m.detailRows()
	q := m.query()
	end := min(m.dTop+rows, len(m.dLines))
	for i := m.dTop; i < end; i++ {
		b.WriteString(renderKindLine(m.dLines[i], q) + "\n")
	}
	for i := end; i < m.dTop+rows; i++ {
		b.WriteString("\n")
	}
	b.WriteString(stDim.Render(strings.Repeat("─", m.w)) + "\n")
	b.WriteString(stDim.Render("↑↓/jk scroll · space page · n/N match · t tech · f follow · r reload · esc back"))
	return b.String()
}

// --- line styling (markdown + search highlight) ----------------------------

// span is a run of text with one style, the unit the highlighter splits on.
type span struct {
	text  string
	style lipgloss.Style
}

// renderKindLine styles one display line by its kind, then overlays search
// highlighting for the active query.
func renderKindLine(l Line, q string) string {
	switch l.Kind {
	case kHeaderYou:
		return stUserHdr.Render(l.Text)
	case kHeaderClaude:
		return stClaudeHdr.Render(l.Text)
	case kThinking:
		return stThink.Render(l.Text)
	case kTool:
		if strings.HasPrefix(strings.TrimSpace(l.Text), "⚙") {
			return emit(overlay([]span{{l.Text, stToolHdr}}, q))
		}
		return emit(overlay([]span{{l.Text, stThink}}, q))
	case kCode:
		return emit(overlay([]span{{l.Text, stCodeBlock}}, q))
	case kTableSep:
		return stDim.Render(l.Text)
	case kTableHead:
		return emit(overlay(tableSpans(l.Text, true), q))
	case kTableRow:
		return emit(overlay(tableSpans(l.Text, false), q))
	default:
		return emit(overlay(inlineSpans(l.Text), q))
	}
}

// inlineSpans parses a prose line into styled spans, recognizing headings,
// bullets, blockquotes, bold, inline code and italics.
func inlineSpans(line string) []span {
	i := 0
	for i < len(line) && line[i] == ' ' {
		i++
	}
	indent, rest := line[:i], line[i:]
	lead := []span{{indent, stPlain}}

	switch {
	case strings.HasPrefix(rest, "> "):
		return append([]span{{indent, stPlain}, {"▏ ", stQuote}}, parseEmphasis(strings.TrimPrefix(rest, "> "), stQuote)...)
	case isHeading(rest):
		return append(lead, parseEmphasis(headingText(rest), stHeading)...)
	case bulletPrefix(rest):
		return append([]span{{indent, stPlain}, {"• ", stBullet}}, parseEmphasis(rest[2:], stPlain)...)
	default:
		return append(lead, parseEmphasis(rest, stPlain)...)
	}
}

// parseEmphasis splits text into spans on `code`, **bold** and *italic*.
func parseEmphasis(s string, base lipgloss.Style) []span {
	var spans []span
	rs := []rune(s)
	var buf []rune
	flush := func() {
		if len(buf) > 0 {
			spans = append(spans, span{string(buf), base})
			buf = buf[:0]
		}
	}
	for i := 0; i < len(rs); i++ {
		switch {
		case rs[i] == '`':
			if j := idxRune(rs, '`', i+1); j > i {
				flush()
				spans = append(spans, span{string(rs[i+1 : j]), stCode})
				i = j
				continue
			}
		case rs[i] == '*' && i+1 < len(rs) && rs[i+1] == '*':
			if j := idxSeq(rs, i+2); j > i {
				flush()
				spans = append(spans, span{string(rs[i+2 : j]), stBold})
				i = j + 1
				continue
			}
		case rs[i] == '*':
			if j := idxRune(rs, '*', i+1); j > i {
				flush()
				spans = append(spans, span{string(rs[i+1 : j]), stItalic})
				i = j
				continue
			}
		}
		buf = append(buf, rs[i])
	}
	flush()
	return spans
}

// tableSpans styles a pre-aligned table row: dim pipes, bold header cells.
func tableSpans(line string, head bool) []span {
	cell := stPlain
	if head {
		cell = stTableHead
	}
	var spans []span
	var buf []rune
	for _, r := range line {
		if r == '│' {
			if len(buf) > 0 {
				spans = append(spans, span{string(buf), cell})
				buf = buf[:0]
			}
			spans = append(spans, span{"│", stDim})
			continue
		}
		buf = append(buf, r)
	}
	if len(buf) > 0 {
		spans = append(spans, span{string(buf), cell})
	}
	return spans
}

// overlay splits spans on case-insensitive matches of q, styling matches with
// stMatch so search hits stay visible over any markdown styling.
func overlay(spans []span, q string) []span {
	if q == "" {
		return spans
	}
	var out []span
	for _, sp := range spans {
		low := strings.ToLower(sp.text)
		rest := sp.text
		for {
			idx := strings.Index(low, q)
			if idx < 0 {
				if rest != "" {
					out = append(out, span{rest, sp.style})
				}
				break
			}
			if idx > 0 {
				out = append(out, span{rest[:idx], sp.style})
			}
			out = append(out, span{rest[idx : idx+len(q)], stMatch})
			rest = rest[idx+len(q):]
			low = low[idx+len(q):]
		}
	}
	return out
}

func emit(spans []span) string {
	var b strings.Builder
	for _, sp := range spans {
		b.WriteString(sp.style.Render(sp.text))
	}
	return b.String()
}

func isHeading(s string) bool {
	n := 0
	for n < len(s) && s[n] == '#' {
		n++
	}
	return n >= 1 && n <= 6 && n < len(s) && s[n] == ' '
}

func headingText(s string) string {
	return strings.TrimLeft(strings.TrimLeft(s, "#"), " ")
}

func bulletPrefix(s string) bool {
	return len(s) >= 2 && (s[0] == '-' || s[0] == '*' || s[0] == '+') && s[1] == ' '
}

func idxRune(rs []rune, r rune, from int) int {
	for i := from; i < len(rs); i++ {
		if rs[i] == r {
			return i
		}
	}
	return -1
}

// idxSeq finds the next "**" at or after `from`, returning the index of its
// first '*'.
func idxSeq(rs []rune, from int) int {
	for i := from; i+1 < len(rs); i++ {
		if rs[i] == '*' && rs[i+1] == '*' {
			return i
		}
	}
	return -1
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
