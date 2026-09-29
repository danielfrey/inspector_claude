// Package session reads Claude Code session transcripts (the JSONL files
// under ~/.claude/projects/<encoded-project>/<session-uuid>.jsonl).
//
// The on-disk format is NOT an officially documented/stable schema, so every
// struct here is deliberately tolerant: unknown fields are ignored, missing
// fields are zero, and a malformed line is skipped rather than fatal. A future
// format change should at worst degrade rendering, never crash the tool.
package session

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Entry is one line of a transcript. Many line types exist (mode,
// permission-mode, system, file-history-snapshot, attachment, summary, user,
// assistant, ...); we only lean on a handful of fields.
type Entry struct {
	Type        string   `json:"type"`
	SessionID   string   `json:"sessionId"`
	UUID        string   `json:"uuid"`
	Timestamp   string   `json:"timestamp"`
	CWD         string   `json:"cwd"`
	GitBranch   string   `json:"gitBranch"`
	Version     string   `json:"version"`
	Entrypoint  string   `json:"entrypoint"` // "cli" (Claude Code) or "sdk-ts" (SDK/ACP, e.g. Tidewave)
	IsSidechain bool     `json:"isSidechain"`
	Summary     string   `json:"summary"` // present on type=="summary" lines
	AITitle     string   `json:"aiTitle"` // present on type=="ai-title" lines
	Message     *Message `json:"message"`
}

// Message holds a user or assistant turn. Content is either a JSON string or an
// array of blocks, so we keep it raw and normalize via Blocks.
type Message struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// Block is a normalized content block. Different block types populate different
// fields (text/thinking -> Text; tool_use -> Name+Input; tool_result -> Content).
type Block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	Content   json.RawMessage `json:"content"`
	ToolUseID string          `json:"tool_use_id"`
}

// Blocks normalizes Content into a slice of blocks. A bare string becomes a
// single text block; anything unparseable yields nil.
func (m *Message) Blocks() []Block {
	if m == nil || len(m.Content) == 0 {
		return nil
	}
	var s string
	if err := json.Unmarshal(m.Content, &s); err == nil {
		return []Block{{Type: "text", Text: s}}
	}
	var bs []Block
	if err := json.Unmarshal(m.Content, &bs); err == nil {
		return bs
	}
	return nil
}

// PlainText returns the human-readable text of a block (best effort). thinking
// content lives in either Thinking or Text depending on version; tool_result
// Content may itself be a string or a nested block array.
func (b Block) PlainText() string {
	switch b.Type {
	case "text":
		return b.Text
	case "thinking":
		if b.Thinking != "" {
			return b.Thinking
		}
		return b.Text
	case "tool_result":
		return rawToText(b.Content)
	case "tool_use":
		return string(b.Input)
	}
	return b.Text
}

// rawToText flattens a tool_result content field (string, or array of
// {type,text} blocks) into plain text.
func rawToText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var bs []Block
	if err := json.Unmarshal(raw, &bs); err == nil {
		var sb strings.Builder
		for _, b := range bs {
			if b.Text != "" {
				sb.WriteString(b.Text)
				sb.WriteByte('\n')
			}
		}
		return sb.String()
	}
	return ""
}

// Session is the indexed metadata for one transcript file. The full entry list
// is read lazily (see ReadEntries) so the search index stays lightweight.
type Session struct {
	Path        string
	ID          string
	ProjectPath string // absolute cwd of the session
	Project     string // basename of ProjectPath, for display
	Branch      string
	Summary     string
	AITitle     string
	FirstPrompt string
	Entrypoint  string // "cli", "sdk-ts", ...
	Start       time.Time
	End         time.Time
	MsgCount    int
	ModTime     time.Time

	firstAny string // first user line of any kind (fallback title)
	plain    string // lowercased concatenated text, for substring search
}

// Source is a human label for how the session was launched: "cli" (Claude Code
// CLI) or "tidewave" (SDK/ACP entrypoint, which is how Tidewave drives Claude).
func (s Session) Source() string {
	switch s.Entrypoint {
	case "sdk-ts":
		return "tidewave"
	case "":
		return ""
	default:
		return s.Entrypoint
	}
}

// Badge is a compact tag for the source, for table columns: "tw", "cli", "?".
func (s Session) Badge() string {
	switch s.Entrypoint {
	case "sdk-ts":
		return "tw"
	case "":
		return "?"
	default:
		return s.Entrypoint
	}
}

// Title is the best available one-line label for the session.
func (s Session) Title() string {
	if s.AITitle != "" {
		return s.AITitle
	}
	if s.Summary != "" {
		return s.Summary
	}
	if s.FirstPrompt != "" {
		return s.FirstPrompt
	}
	if s.firstAny != "" {
		return s.firstAny
	}
	return s.ID
}

// SortTime is the key used to order sessions newest-first.
func (s Session) SortTime() time.Time {
	if !s.End.IsZero() {
		return s.End
	}
	return s.ModTime
}

// Matches reports the number of case-insensitive occurrences of q in the
// session's indexed text. q must already be lowercased.
func (s Session) Matches(qLower string) int {
	if qLower == "" {
		return 0
	}
	return strings.Count(s.plain, qLower)
}

// DefaultRoot is ~/.claude/projects.
func DefaultRoot() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "projects")
}

// Scan indexes every *.jsonl transcript under root, newest session first.
func Scan(root string) ([]Session, error) {
	dirs, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var sessions []Session
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		files, _ := filepath.Glob(filepath.Join(root, d.Name(), "*.jsonl"))
		for _, f := range files {
			s, err := scanFile(f, d.Name())
			if err != nil {
				continue // skip unreadable files rather than abort the whole scan
			}
			sessions = append(sessions, s)
		}
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].SortTime().After(sessions[j].SortTime())
	})
	return sessions, nil
}

// scanFile builds the metadata + search index for a single transcript.
func scanFile(path, dirName string) (Session, error) {
	f, err := os.Open(path)
	if err != nil {
		return Session{}, err
	}
	defer f.Close()

	s := Session{Path: path}
	if fi, err := f.Stat(); err == nil {
		s.ModTime = fi.ModTime()
	}

	var plain strings.Builder
	sc := bufio.NewScanner(f)
	// Transcript lines can be large (embedded tool output), so grow the buffer.
	sc.Buffer(make([]byte, 1024*1024), 32*1024*1024)

	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			continue
		}
		if s.ID == "" && e.SessionID != "" {
			s.ID = e.SessionID
		}
		if s.ProjectPath == "" && e.CWD != "" {
			s.ProjectPath = e.CWD
		}
		if s.Branch == "" && e.GitBranch != "" {
			s.Branch = e.GitBranch
		}
		if s.Entrypoint == "" && e.Entrypoint != "" {
			s.Entrypoint = e.Entrypoint
		}
		if e.Type == "summary" && e.Summary != "" {
			s.Summary = e.Summary
		}
		// Claude Code records an auto-generated session title on type=="ai-title"
		// lines; it can appear several times and be refined, so last-wins.
		if e.Type == "ai-title" && e.AITitle != "" {
			s.AITitle = e.AITitle
		}
		if ts := parseTime(e.Timestamp); !ts.IsZero() {
			if s.Start.IsZero() {
				s.Start = ts
			}
			s.End = ts
		}
		if e.Message == nil {
			continue
		}
		s.MsgCount++
		for _, b := range e.Message.Blocks() {
			txt := b.PlainText()
			if txt == "" {
				continue
			}
			plain.WriteString(txt)
			plain.WriteByte('\n')
			if e.Type == "user" && b.Type == "text" {
				if s.firstAny == "" {
					s.firstAny = firstMeaningfulLine(txt)
				}
				if s.FirstPrompt == "" {
					s.FirstPrompt = cleanPrompt(txt)
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		return Session{}, err
	}

	if s.ProjectPath != "" {
		s.Project = filepath.Base(s.ProjectPath)
	} else {
		s.Project = strings.TrimPrefix(dirName, "-")
	}
	if s.ID == "" {
		s.ID = strings.TrimSuffix(filepath.Base(path), ".jsonl")
	}
	s.plain = strings.ToLower(plain.String())
	return s, nil
}

// ReadEntries re-reads and returns every parseable entry of a transcript, in
// order. Used when opening a session for detailed rendering.
func ReadEntries(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 32*1024*1024)
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			continue
		}
		out = append(out, e)
	}
	return out, sc.Err()
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// firstMeaningfulLine returns the first line of s that looks like real prose,
// skipping empty lines and command/markup wrappers, so a session's title is not
// "<command-name>" or a lone slash-command.
func firstMeaningfulLine(s string) string {
	for _, ln := range strings.Split(s, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "<") || strings.HasPrefix(ln, "/") {
			continue
		}
		return ln
	}
	return strings.TrimSpace(strings.SplitN(s, "\n", 2)[0])
}

// cleanPrompt returns the first line of a user turn that reads like a real
// prompt, skipping the wrapper noise that /resume and slash-commands inject
// (<local-command-caveat>, <command-name>, <system-reminder>, "Caveat:",
// interrupt notices). Returns "" when the whole turn is such noise.
func cleanPrompt(s string) string {
	for _, ln := range strings.Split(s, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "<") || strings.HasPrefix(ln, "/") ||
			strings.HasPrefix(ln, "Caveat:") || strings.HasPrefix(ln, "[Request interrupted") ||
			strings.HasPrefix(ln, "command-") || strings.HasPrefix(ln, "DO NOT") {
			continue
		}
		return ln
	}
	return ""
}
