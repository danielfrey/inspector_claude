package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"

	"inspector_claude/internal/session"
)

// resumedMsg carries the outcome of a `claude --resume` run back into the TUI.
type resumedMsg struct{ err error }

// resumeCommand builds `claude --resume <id>` for one session. The cwd matters
// as much as the id: Claude Code looks for the transcript under the project
// directory derived from where it was started, so a resume only resolves when
// the process runs in the session's own ProjectPath.
func resumeCommand(s session.Session) (*exec.Cmd, error) {
	bin, err := exec.LookPath("claude")
	if err != nil {
		return nil, errors.New("`claude` not found in PATH")
	}
	if s.ProjectPath == "" {
		return nil, errors.New("session has no recorded cwd")
	}
	if fi, err := os.Stat(s.ProjectPath); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("directory gone: %s", s.ProjectPath)
	}
	c := exec.Command(bin, "--resume", s.ID)
	c.Dir = s.ProjectPath
	return c, nil
}

// resumeCmd suspends the TUI, hands the terminal to Claude, and restores the
// TUI once Claude exits — so resuming a chat is a round trip, not a quit.
func resumeCmd(s session.Session) tea.Cmd {
	c, err := resumeCommand(s)
	if err != nil {
		return func() tea.Msg { return resumedMsg{err} }
	}
	return tea.ExecProcess(c, func(err error) tea.Msg { return resumedMsg{err} })
}

// cliResume replaces the CLI run with an interactive Claude on the referenced
// session (`inspector_claude --resume <id>`), inheriting this terminal.
func cliResume(sessions []session.Session, ref string) {
	s := findSession(sessions, ref)
	if s == nil {
		fmt.Fprintf(os.Stderr, "inspector_claude: no session matching %q\n", ref)
		os.Exit(1)
	}
	c, err := resumeCommand(*s)
	if err != nil {
		fmt.Fprintln(os.Stderr, "inspector_claude:", err)
		os.Exit(1)
	}
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	fmt.Fprintf(os.Stderr, "→ claude --resume %s  (in %s)\n", s.ID, s.ProjectPath)
	if err := c.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			os.Exit(ee.ExitCode())
		}
		fmt.Fprintln(os.Stderr, "inspector_claude:", err)
		os.Exit(1)
	}
}
