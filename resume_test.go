package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"inspector_claude/internal/session"
)

func TestResumeCommand(t *testing.T) {
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude not in PATH")
	}
	dir := t.TempDir()
	c, err := resumeCommand(session.Session{ID: "abc-123", ProjectPath: dir})
	if err != nil {
		t.Fatalf("resumeCommand: %v", err)
	}
	if c.Dir != dir {
		t.Errorf("Dir = %q, want %q", c.Dir, dir)
	}
	if got := strings.Join(c.Args[1:], " "); got != "--resume abc-123" {
		t.Errorf("args = %q, want %q", got, "--resume abc-123")
	}
}

func TestResumeCommandRejectsMissingDir(t *testing.T) {
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude not in PATH")
	}
	gone := t.TempDir() + "/removed"
	if err := os.Mkdir(gone, 0o755); err != nil {
		t.Fatal(err)
	}
	os.Remove(gone)
	if _, err := resumeCommand(session.Session{ID: "x", ProjectPath: gone}); err == nil {
		t.Error("want error for a vanished project directory, got nil")
	}
	if _, err := resumeCommand(session.Session{ID: "x"}); err == nil {
		t.Error("want error for a session without cwd, got nil")
	}
}
