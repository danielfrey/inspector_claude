# inspector_claude

A terminal browser and full-text search over your local **Claude Code** session
transcripts — the JSONL files under `~/.claude/projects/<encoded-project>/<session-uuid>.jsonl`.

Single static Go binary (CGO off), so it cross-compiles trivially and has no
runtime dependencies. Interactive [Bubble Tea](https://github.com/charmbracelet/bubbletea)
TUI plus a pipe-friendly plain-CLI mode.

## Install

macOS, via Homebrew:

```sh
brew tap danielfrey/tap
brew trust danielfrey/tap          # Homebrew 7+ requires this for third-party taps
brew install inspector_claude
```

## Build from source

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
inspector_claude --resume <session-id>  # continue that chat in claude, in its own cwd
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
| List/Proj | `ctrl+r`       | resume the selected session in `claude` (its own cwd) |
| List     | `esc`           | clear search / quit |
| Projects | `←`/`→`         | switch pane (projects ↔ sessions) |
| Projects | `↑`/`↓`         | move within the active pane |
| Projects | `enter`         | left: into sessions · right: open chat |
| Projects | `esc`           | clear search / quit |
| Detail  | `↑`/`↓`, `j`/`k` | scroll |
| Detail  | `space`, `pgup`/`pgdn` | page |
| Detail  | `/`             | search within this chat (own term; `enter` keep, `esc` cancel) |
| Detail  | `n` / `N`       | next / previous match |
| Detail  | `t`             | toggle technical lines (tool calls/results/thinking) |
| Detail  | `f`             | toggle live-follow (auto-reload as the file grows) |
| Detail  | `ctrl+r` / `R`  | resume this session in `claude` (its own cwd) |
| Detail  | `r`             | reload now (Cmd-R is captured by the terminal/OS) |
| List/Proj/Detail | `ctrl+o` | open this chat in the browser (standalone HTML) |
| Detail  | `g` / `G`       | top / bottom |
| Detail  | `esc` / `q`     | back to list |

### Two independent searches

The **global** search (the box in the list/projects views) filters which
sessions are shown. When you open a chat it **adopts** that term — the matches
are already highlighted and `n`/`N` walk them. Inside the chat, `/` starts a
**local** search that overrides the highlight for that chat only (`enter` keeps
it, `esc` restores the adopted term). Leaving the chat, the global search is
untouched, so the list filter is exactly as you left it.

`t` switches between the full transcript and a conversation-only view that hides
tool calls, tool results and thinking.

### Markdown & live view

The chat renders inline markdown — **bold**, `inline code`, *italics*,
headings, bullet/numbered lists, blockquotes, fenced code blocks and aligned
tables — while keeping search matches highlighted on top.

### Open in the browser (no server)

`ctrl+o` renders the current chat to a **standalone HTML file** (markdown as real
HTML — headings, bold, code, tables, collapsible thinking/tool blocks; light and
dark) under your temp dir and opens it in the default browser. No web server, no
port — it works offline and from a shared binary. From the CLI:

```sh
inspector_claude --open <id>          # render + open in the browser
inspector_claude --html <id> > x.html # render HTML to stdout (to save/share)
```

A `● live` indicator shows that the open session is being followed: because
transcripts only ever grow, the app polls the file once a second (read-only)
and, if you are scrolled to the bottom, tails new turns as they are written —
so you can watch a session happening in another Claude Code window in real time.
Press `f` to pause/resume following, `r` to reload immediately.

### Source badge

Each session shows how it was launched, read from the transcript's `entrypoint`
field: **`cli`** for the Claude Code CLI, **`tw`** (orange) for the SDK/ACP
entrypoint that drives Claude from Tidewave. The badge appears in the list, the
projects pane, the chat header and `--list`.

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
