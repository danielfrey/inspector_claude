// inspector_claude is a terminal browser and full-text search over local
// Claude Code session transcripts (~/.claude/projects/**/*.jsonl).
//
// Interactive:  inspector_claude
// Plain CLI:    inspector_claude --list
//
//	inspector_claude --search "kamal 2.8"
//	inspector_claude --show <session-id-or-path>
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"inspector_claude/internal/session"

	tea "github.com/charmbracelet/bubbletea"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	var (
		root       = flag.String("root", session.DefaultRoot(), "transcripts root directory")
		listFlag   = flag.Bool("list", false, "print sessions and exit (no TUI)")
		searchFlag = flag.String("search", "", "print sessions matching query and exit (no TUI)")
		showFlag   = flag.String("show", "", "print one session (by id or path) and exit (no TUI)")
		convFlag   = flag.Bool("conversation", false, "with --show: hide tool calls/results/thinking")
		versFlag   = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *versFlag {
		fmt.Println("inspector_claude", version)
		return
	}

	sessions, err := session.Scan(*root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "inspector_claude: cannot scan %s: %v\n", *root, err)
		os.Exit(1)
	}
	if len(sessions) == 0 {
		fmt.Fprintf(os.Stderr, "inspector_claude: no transcripts found under %s\n", *root)
		os.Exit(1)
	}

	switch {
	case *showFlag != "":
		cliShow(sessions, *showFlag, *convFlag)
	case *searchFlag != "":
		cliList(filterSessions(sessions, *searchFlag), strings.ToLower(*searchFlag))
	case *listFlag:
		cliList(sessions, "")
	default:
		p := tea.NewProgram(newModel(sessions), tea.WithAltScreen())
		if _, err := p.Run(); err != nil {
			fmt.Fprintln(os.Stderr, "inspector_claude:", err)
			os.Exit(1)
		}
	}
}

// filterSessions returns sessions whose indexed text contains q, most matches
// first. An empty q returns the input unchanged.
func filterSessions(all []session.Session, q string) []session.Session {
	ql := strings.ToLower(strings.TrimSpace(q))
	if ql == "" {
		return all
	}
	var out []session.Session
	for _, s := range all {
		if s.Matches(ql) > 0 {
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Matches(ql) > out[j].Matches(ql)
	})
	return out
}

func cliList(sessions []session.Session, ql string) {
	for _, s := range sessions {
		date := "          "
		if !s.SortTime().IsZero() {
			date = s.SortTime().Format("2006-01-02")
		}
		extra := ""
		if ql != "" {
			extra = fmt.Sprintf("  (%d hits)", s.Matches(ql))
		}
		fmt.Printf("%s  %-18s  %3d msg%s  %s\n", date, trunc(s.Project, 18), s.MsgCount, extra, trunc(oneLine(s.Title()), 70))
		fmt.Printf("            %s\n", s.ID)
	}
}

func cliShow(sessions []session.Session, ref string, conversationOnly bool) {
	s := findSession(sessions, ref)
	if s == nil {
		fmt.Fprintf(os.Stderr, "inspector_claude: no session matching %q\n", ref)
		os.Exit(1)
	}
	entries, err := session.ReadEntries(s.Path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "inspector_claude:", err)
		os.Exit(1)
	}
	fmt.Printf("# %s\n# project: %s  branch: %s\n# %s\n\n", s.ID, s.ProjectPath, s.Branch, s.Path)
	for _, l := range lineTexts(renderEntries(entries), conversationOnly) {
		fmt.Println(l)
	}
}

// findSession resolves a session by full/prefix id, or by file path/basename.
func findSession(sessions []session.Session, ref string) *session.Session {
	ref = strings.TrimSpace(ref)
	base := strings.TrimSuffix(filepath.Base(ref), ".jsonl")
	for i := range sessions {
		s := &sessions[i]
		if s.ID == ref || s.Path == ref || s.ID == base {
			return s
		}
	}
	for i := range sessions {
		if strings.HasPrefix(sessions[i].ID, ref) {
			return &sessions[i]
		}
	}
	return nil
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}
