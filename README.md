# inspector_claude

A terminal browser and full-text search over your local **Claude Code** session
transcripts — the JSONL files under `~/.claude/projects/<encoded-project>/<session-uuid>.jsonl`.

Single static Go binary (CGO off), so it cross-compiles trivially and has no
runtime dependencies. Interactive [Bubble Tea](https://github.com/charmbracelet/bubbletea)
TUI plus a pipe-friendly plain-CLI mode.

## Install / build

```sh
make build          # -> ./bin/inspector_claude
make install        # -> $GOBIN / $GOPATH/bin
make cross          # -> ./dist/inspector_claude-<os>-<arch> for darwin/linux/windows
```

Requires Go 1.24+ (the charmbracelet deps pull it in automatically via the
toolchain directive).

## Usage

```sh
inspector_claude                       # interactive TUI
inspector_claude --list                # list all sessions, newest first
inspector_claude --search "kamal 2.8"  # sessions matching a query, most hits first
inspector_claude --show <session-id>   # print one session (id, id-prefix, or path)
inspector_claude --show <id> --conversation  # conversation only (no tool calls/thinking)
inspector_claude --root /path/to/dir   # use a non-default transcripts root
```

### Two browse views

`tab` toggles between them; both share the search box, and `enter` opens the
chat from either:

- **List** — a flat, chronological list of every session across all projects.
- **Projects** — two panes: projects on the left (newest activity first, with
  session counts), the selected project's sessions on the right.

### TUI keys

| Context  | Keys | Action |
|----------|------|--------|
| List/Proj | type           | full-text search across **all** sessions |
| List/Proj | `tab`          | switch between list and projects view |
| List     | `↑`/`↓`, `ctrl+p`/`ctrl+n` | move selection |
| List     | `pgup`/`pgdn`   | page |
| List     | `enter`         | open session |
| List     | `esc`           | clear search / quit |
| Projects | `←`/`→`         | switch pane (projects ↔ sessions) |
| Projects | `↑`/`↓`         | move within the active pane |
| Projects | `enter`         | left: into sessions · right: open chat |
| Projects | `esc`           | clear search / quit |
| Detail  | `↑`/`↓`, `j`/`k` | scroll |
| Detail  | `space`, `pgup`/`pgdn` | page |
| Detail  | `n` / `N`       | next / previous match (of the active search) |
| Detail  | `t`             | toggle technical lines (tool calls/results/thinking) |
| Detail  | `g` / `G`       | top / bottom |
| Detail  | `esc` / `q`     | back to list |

The active search term is highlighted inside the detail view, and `n`/`N` jump
between its occurrences. `t` switches between the full transcript and a
conversation-only view that hides tool calls, tool results and thinking.

## How it works

Each transcript line is one JSON object with a `type` and, for turns, a
`message.content` that is either a string or an array of blocks
(`text` / `thinking` / `tool_use` / `tool_result`). On start-up every file is
indexed into lightweight metadata (project from `cwd`, git branch, timestamps,
message count, a searchable lowercased text blob); full entries are re-read
lazily only when you open a session.

The on-disk format is **not** an officially documented or stable schema, so the
parser is deliberately tolerant: unknown fields are ignored, missing fields are
zero, and an unparseable line is skipped. A future format change should at worst
degrade rendering — never crash the tool.

## Layout

```
main.go                     CLI entry, flags, plain-CLI modes (--list/--search/--show)
tui.go                      Bubble Tea model: list + detail views, search, highlight
render.go                   transcript entries -> display lines
internal/session/session.go tolerant JSONL reader + on-disk index (Scan/ReadEntries)
```
