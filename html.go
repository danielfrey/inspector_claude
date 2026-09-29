package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"inspector_claude/internal/session"
)

// writeSessionHTML renders a session to a standalone HTML file (one per session
// id, overwritten on each call) under TMPDIR and returns its path.
func writeSessionHTML(s session.Session, entries []session.Entry) (string, error) {
	dir := filepath.Join(os.TempDir(), "inspector_claude")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, s.ID+".html")
	if err := os.WriteFile(path, []byte(renderSessionHTML(s, entries)), 0644); err != nil {
		return "", err
	}
	return path, nil
}

// renderSessionHTML produces a complete, self-contained HTML document (no
// external assets, so it works offline and when the binary is shared).
func renderSessionHTML(s session.Session, entries []session.Entry) string {
	var b strings.Builder
	title := escapeHTML(oneLine(s.Title()))
	b.WriteString("<!doctype html><html><head><meta charset=\"utf-8\">")
	b.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">")
	b.WriteString("<title>" + title + "</title>\n<style>\n" + htmlStyle + "</style></head><body>\n")
	b.WriteString("<header><h1>" + title + "</h1>")
	// Subtitle carries the project and the session id — the id is the argument
	// for `claude --resume <id>`, so it stays visible even though the heading is
	// now the human-readable title.
	b.WriteString("<div class=\"meta\">" + escapeHTML(s.Project) + " · " + escapeHTML(s.ID))
	if s.Branch != "" {
		b.WriteString(" · " + escapeHTML(s.Branch))
	}
	if !s.Start.IsZero() {
		b.WriteString(" · " + s.Start.Format("2006-01-02 15:04"))
	}
	b.WriteString("</div></header>\n<main>\n")

	// The technical blocks (thinking / tool calls / results) surrounding a
	// Claude turn are buffered and flushed as one group, so a single "expand
	// all" control can open every small block of that turn at once.
	var pending []string
	flushTech := func() {
		switch len(pending) {
		case 0:
		case 1:
			b.WriteString(pending[0])
		default:
			b.WriteString("<div class=\"tech-group\">\n")
			for _, d := range pending {
				b.WriteString(d)
			}
			b.WriteString("</div>\n")
		}
		pending = pending[:0]
	}

	for _, e := range entries {
		if e.Message == nil {
			continue
		}
		for _, blk := range e.Message.Blocks() {
			switch blk.Type {
			case "text":
				if strings.TrimSpace(blk.Text) == "" {
					continue
				}
				flushTech()
				role, cls := "Claude", "assistant"
				if e.Type == "user" {
					role, cls = "You", "user"
				}
				b.WriteString("<section class=\"turn " + cls + "\"><div class=\"role\">" + role)
				// The assistant role line carries a toggle icon on its right
				// edge; it opens/closes every collapsible block of the group that
				// follows this turn. Orphan buttons (no following group) are
				// hidden by the script.
				if cls == "assistant" {
					b.WriteString("<button class=\"toggle-all\" type=\"button\" title=\"alle Blöcke auf-/zuklappen\" aria-label=\"alle Blöcke auf-/zuklappen\">▸</button>")
				}
				b.WriteString("</div>\n")
				b.WriteString(mdToHTML(blk.Text))
				b.WriteString("</section>\n")
			case "thinking":
				if txt := strings.TrimSpace(blk.PlainText()); txt != "" {
					pending = append(pending, detailsBlock("· thinking", blk.PlainText()))
				}
			case "tool_use":
				pending = append(pending, detailsBlock("⚙ "+escapeHTML(blk.Name), plainInput(blk.Input)))
			case "tool_result":
				if txt := strings.TrimRight(blk.PlainText(), "\n"); strings.TrimSpace(txt) != "" {
					pending = append(pending, detailsBlock("⎿ result", txt))
				}
			}
		}
	}
	flushTech()
	b.WriteString("</main>\n<footer>rendered by inspector_claude</footer>\n" + htmlScript + "</body></html>")
	return b.String()
}

func plainInput(raw []byte) string {
	return summarizeInput(raw)
}

// detailsBlock renders a collapsible technical block (thinking / tool).
func detailsBlock(summary, body string) string {
	return "<details class=\"tech\"><summary>" + escapeHTML(summary) + "</summary><pre>" +
		escapeHTML(body) + "</pre></details>\n"
}

// mdToHTML converts a markdown text block to HTML, handling fenced code,
// tables, headings, lists, blockquotes, paragraphs and inline emphasis.
func mdToHTML(text string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	var b strings.Builder
	var para []string
	listOpen := ""

	flushPara := func() {
		if len(para) > 0 {
			b.WriteString("<p>" + strings.Join(para, "<br>") + "</p>\n")
			para = para[:0]
		}
	}
	closeList := func() {
		if listOpen != "" {
			b.WriteString("</" + listOpen + ">\n")
			listOpen = ""
		}
	}

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trim := strings.TrimSpace(line)

		switch {
		case strings.HasPrefix(trim, "```"):
			flushPara()
			closeList()
			var code []string
			i++
			for i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), "```") {
				code = append(code, lines[i])
				i++
			}
			b.WriteString("<pre class=\"code\"><code>" + escapeHTML(strings.Join(code, "\n")) + "</code></pre>\n")

		case strings.Contains(line, "|") && i+1 < len(lines) && isTableSep(lines[i+1]):
			flushPara()
			closeList()
			i += tableToHTML(lines[i:], &b) - 1

		case trim == "":
			flushPara()
			closeList()

		case isHeading(trim):
			flushPara()
			closeList()
			n := 0
			for n < len(trim) && trim[n] == '#' {
				n++
			}
			b.WriteString(fmt.Sprintf("<h%d>%s</h%d>\n", n, inlineHTML(headingText(trim)), n))

		case bulletPrefix(trim):
			flushPara()
			if listOpen != "ul" {
				closeList()
				b.WriteString("<ul>\n")
				listOpen = "ul"
			}
			b.WriteString("<li>" + inlineHTML(trim[2:]) + "</li>\n")

		case numberedPrefix(trim):
			flushPara()
			if listOpen != "ol" {
				closeList()
				b.WriteString("<ol>\n")
				listOpen = "ol"
			}
			b.WriteString("<li>" + inlineHTML(afterNumber(trim)) + "</li>\n")

		case strings.HasPrefix(trim, "> "):
			flushPara()
			closeList()
			b.WriteString("<blockquote>" + inlineHTML(strings.TrimPrefix(trim, "> ")) + "</blockquote>\n")

		default:
			closeList()
			para = append(para, inlineHTML(line))
		}
	}
	flushPara()
	closeList()
	return b.String()
}

// tableToHTML emits an HTML table for the markdown table at lines[0:] and
// returns how many source lines it consumed.
func tableToHTML(lines []string, b *strings.Builder) int {
	header := splitCellsRaw(lines[0])
	consumed := 2
	var rows [][]string
	for i := 2; i < len(lines); i++ {
		if !strings.Contains(lines[i], "|") {
			break
		}
		rows = append(rows, splitCellsRaw(lines[i]))
		consumed++
	}
	b.WriteString("<table><thead><tr>")
	for _, c := range header {
		b.WriteString("<th>" + inlineHTML(c) + "</th>")
	}
	b.WriteString("</tr></thead><tbody>\n")
	for _, r := range rows {
		b.WriteString("<tr>")
		for _, c := range r {
			b.WriteString("<td>" + inlineHTML(c) + "</td>")
		}
		b.WriteString("</tr>\n")
	}
	b.WriteString("</tbody></table>\n")
	return consumed
}

// splitCellsRaw splits a table row into trimmed cells, keeping inline markers
// (unlike splitCells, which strips them for the terminal's plain alignment).
func splitCellsRaw(line string) []string {
	t := strings.TrimSpace(line)
	t = strings.TrimPrefix(t, "|")
	t = strings.TrimSuffix(t, "|")
	parts := strings.Split(t, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// inlineHTML escapes text and applies inline emphasis: `code`, **bold**, *italic*.
func inlineHTML(s string) string {
	rs := []rune(s)
	var b strings.Builder
	for i := 0; i < len(rs); i++ {
		switch {
		case rs[i] == '`':
			if j := idxRune(rs, '`', i+1); j > i {
				b.WriteString("<code>" + escapeHTML(string(rs[i+1:j])) + "</code>")
				i = j
				continue
			}
		case rs[i] == '*' && i+1 < len(rs) && rs[i+1] == '*':
			if j := idxSeq(rs, i+2); j > i {
				b.WriteString("<strong>" + inlineHTML(string(rs[i+2:j])) + "</strong>")
				i = j + 1
				continue
			}
		case rs[i] == '*':
			if j := idxRune(rs, '*', i+1); j > i {
				b.WriteString("<em>" + inlineHTML(string(rs[i+1:j])) + "</em>")
				i = j
				continue
			}
		}
		b.WriteString(escapeHTML(string(rs[i])))
	}
	return b.String()
}

func escapeHTML(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func numberedPrefix(s string) bool {
	n := 0
	for n < len(s) && s[n] >= '0' && s[n] <= '9' {
		n++
	}
	return n > 0 && n+1 < len(s) && s[n] == '.' && s[n+1] == ' '
}

func afterNumber(s string) string {
	i := strings.Index(s, ". ")
	if i < 0 {
		return s
	}
	return s[i+2:]
}

const htmlStyle = `:root{--bg:#ffffff;--fg:#1f2328;--dim:#6a6a6a;--accent:#0059b3;--user:#0a7d1a;--claude:#8250df;--code-bg:#f2f2f5;--border:#d8dee4}
@media(prefers-color-scheme:dark){:root{--bg:#0f1115;--fg:#e6e6e6;--dim:#9aa0a6;--accent:#6cb6ff;--user:#7ee787;--claude:#c297ff;--code-bg:#1b1f27;--border:#333a42}}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--fg);font:16px/1.6 -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,Helvetica,Arial,sans-serif}
header,main,footer{max-width:820px;margin:0 auto;padding:0 24px}
header{padding-top:32px}
header h1{margin:0;font-size:22px;color:var(--accent)}
.meta{color:var(--dim);font-size:13px;margin-top:4px}
main{padding-top:16px;padding-bottom:64px}
.turn{padding:12px 0;border-top:1px solid var(--border)}
.turn .role{position:relative;font-weight:700;font-size:13px;text-transform:uppercase;letter-spacing:.04em;margin-bottom:6px}
.turn.user .role{color:var(--user)}
.turn.assistant .role{color:var(--claude)}
.turn p{margin:.5em 0}
h1,h2,h3,h4,h5,h6{line-height:1.3}
main h2{font-size:19px}main h3{font-size:17px}
code{background:var(--code-bg);color:var(--accent);padding:.1em .35em;border-radius:4px;font:13.5px/1.5 ui-monospace,SFMono-Regular,Menlo,Consolas,monospace}
pre.code{background:var(--code-bg);padding:12px 14px;border-radius:8px;overflow-x:auto}
pre.code code{background:none;color:var(--fg);padding:0}
blockquote{margin:.5em 0;padding:.2em 0 .2em 14px;border-left:3px solid var(--border);color:var(--dim)}
table{border-collapse:collapse;margin:.6em 0;width:100%;font-size:14.5px;overflow-x:auto;display:block}
th,td{border:1px solid var(--border);padding:6px 10px;text-align:left}
th{background:var(--code-bg)}
details.tech{margin:6px 0;color:var(--dim)}
details.tech summary{cursor:pointer;font-size:13px}
details.tech pre{background:var(--code-bg);padding:10px 12px;border-radius:6px;overflow-x:auto;font:12.5px/1.5 ui-monospace,Menlo,monospace;white-space:pre-wrap;word-break:break-word}
.tech-group{margin:6px 0}
.toggle-all{position:absolute;top:-4px;right:0;cursor:pointer;font-size:20px;line-height:1;color:var(--dim);background:none;border:none;padding:0 2px}
.toggle-all:hover{color:var(--accent)}
ul,ol{margin:.4em 0;padding-left:1.5em}
footer{color:var(--dim);font-size:12px;padding-bottom:32px;text-align:center}
`

// htmlScript toggles every collapsible block inside one tech-group at once. It
// is self-contained (no external assets) so the rendered file works offline.
const htmlScript = `<script>
function techGroupFor(btn){
  var g=btn.closest('.turn').nextElementSibling;
  return (g&&g.classList.contains('tech-group'))?g:null;
}
document.querySelectorAll('.toggle-all').forEach(function(btn){
  if(!techGroupFor(btn))btn.style.display='none';
});
document.addEventListener('click',function(ev){
  var btn=ev.target.closest('.toggle-all');
  if(!btn)return;
  var g=techGroupFor(btn);
  if(!g)return;
  var items=g.querySelectorAll('details.tech');
  var open=Array.prototype.some.call(items,function(d){return !d.open;});
  items.forEach(function(d){d.open=open;});
  btn.textContent=open?'▾':'▸';
});
</script>
`
