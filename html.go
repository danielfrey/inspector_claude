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
	// The body carries the initial detail level as a class (tech-0), so the
	// document renders correctly even before the script runs / without JS.
	b.WriteString("<title>" + title + "</title>\n<style>\n" + htmlStyle + "</style></head><body class=\"tech-0\">\n")
	// A fixed, semi-transparent toolbar above the title. On the right it offers
	// three detail levels for the Claude content; the active one is highlighted,
	// and the "t" key cycles through them (mirrors the TUI's "t" shortcut):
	//   0 = questions + answers   1 = + intermediate steps   2 = + tool calls
	b.WriteString("<div id=\"toolbar\"><span class=\"tb-label\">Tech</span>" +
		"<div class=\"tb-states\" role=\"group\" aria-label=\"detail level (press t to cycle)\">" +
		"<button class=\"tb-state\" type=\"button\" data-state=\"0\" title=\"questions + answers\">0</button>" +
		"<button class=\"tb-state\" type=\"button\" data-state=\"1\" title=\"+ intermediate steps\">1</button>" +
		"<button class=\"tb-state\" type=\"button\" data-state=\"2\" title=\"+ tool calls &amp; results\">2</button>" +
		"</div></div>\n")
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
	b.WriteString("</div></header>\n")

	// The conversation body is built into its own buffer while a table of
	// contents is collected alongside it, so the (collapsed) TOC can be emitted
	// between the header and <main> even though its entries are only known once
	// the whole body has been walked.
	var body strings.Builder
	var toc []tocEntry
	turnN := 0

	// The table of contents is a pure question/answer index, so we first mark
	// which assistant text blocks are actual answers (the last text of a Claude
	// turn) as opposed to intermediate narration ("Now I'll look at …") that only
	// introduces the next tool calls; the latter are left out of the TOC.
	isAnswer := answerTextBlocks(entries)
	asstN := 0

	// The technical blocks (thinking / tool calls / results) surrounding a
	// Claude turn are buffered and flushed as one group, so a single "expand
	// all" control can open every small block of that turn at once.
	var pending []string
	flushTech := func() {
		switch len(pending) {
		case 0:
		case 1:
			body.WriteString(pending[0])
		default:
			body.WriteString("<div class=\"tech-group\">\n")
			for _, d := range pending {
				body.WriteString(d)
			}
			body.WriteString("</div>\n")
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
				mark := "●"
				if e.Type == "user" {
					role, cls, mark = "You", "user", "▶"
				}
				// Classify assistant texts: an "answer" is a final reply (also
				// shown in the TOC); everything else is intermediate narration,
				// which the "answer" class distinguishes for the detail levels.
				answer := false
				if cls == "assistant" {
					answer = isAnswer[asstN]
					asstN++
				}
				// A TOC entry is created for every user question and for answers
				// only; intermediate narration is skipped. Only TOC'd sections
				// need an anchor id to jump to.
				inTOC := cls == "user" || answer
				sectionCls := "turn " + cls
				if cls == "assistant" {
					if answer {
						sectionCls += " answer"
					} else {
						sectionCls += " step"
					}
				}
				attrID := ""
				if inTOC {
					turnN++
					id := fmt.Sprintf("t%d", turnN)
					attrID = " id=\"" + id + "\""
					toc = append(toc, tocEntry{ID: id, Kind: cls, Mark: mark, Label: tocLabel(blk.Text)})
				}
				body.WriteString("<section class=\"" + sectionCls + "\"" + attrID + "><div class=\"role\">" + role)
				// The assistant role line carries a toggle icon on its right
				// edge; it opens/closes every collapsible block of the group that
				// follows this turn. Orphan buttons (no following group) are
				// hidden by the script.
				if cls == "assistant" {
					body.WriteString("<button class=\"toggle-all\" type=\"button\" title=\"expand/collapse all blocks\" aria-label=\"expand/collapse all blocks\">▸</button>")
				}
				body.WriteString("</div>\n")
				body.WriteString(mdToHTML(blk.Text))
				body.WriteString("</section>\n")
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

	b.WriteString(renderTOC(toc))
	b.WriteString("<main>\n")
	b.WriteString(body.String())
	b.WriteString("</main>\n<footer>rendered by inspector_claude</footer>\n" + htmlScript + "</body></html>")
	return b.String()
}

// tocEntry is one line of the table of contents, pointing at an anchor in the
// body. Kind is "user" or "assistant" (drives the marker color).
type tocEntry struct {
	ID    string
	Kind  string
	Mark  string
	Label string
}

// renderTOC emits the collapsed table of contents placed between the header and
// the conversation. Each entry is a one-line link that jumps to its anchor.
func renderTOC(toc []tocEntry) string {
	if len(toc) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<details class=\"toc\"><summary>Table of Contents</summary>\n<nav>\n")
	for _, t := range toc {
		label := t.Label
		if label == "" {
			label = "…"
		}
		b.WriteString("<a class=\"toc-item toc-" + t.Kind + "\" href=\"#" + t.ID + "\">")
		b.WriteString("<span class=\"toc-mark\">" + escapeHTML(t.Mark) + "</span>")
		b.WriteString("<span class=\"toc-text\">" + escapeHTML(label) + "</span></a>\n")
	}
	b.WriteString("</nav></details>\n")
	return b.String()
}

// answerTextBlocks marks which assistant text blocks are answers rather than
// intermediate narration. A Claude turn (the run of activity between two user
// messages) usually contains several text blocks: the ones that merely announce
// the next tool calls, and a final one that is the actual answer. The last
// assistant text block before the next user message — or before the transcript
// ends — is that answer. The returned set is keyed by the block's ordinal among
// all non-empty assistant text blocks, in document order.
func answerTextBlocks(entries []session.Entry) map[int]bool {
	answers := map[int]bool{}
	asstN := 0 // ordinal of the next assistant text block
	last := -1 // ordinal of the last assistant text seen in the current turn
	closeTurn := func() {
		if last >= 0 {
			answers[last] = true
			last = -1
		}
	}
	for _, e := range entries {
		if e.Message == nil {
			continue
		}
		for _, blk := range e.Message.Blocks() {
			if blk.Type != "text" || strings.TrimSpace(blk.Text) == "" {
				continue
			}
			if e.Type == "user" {
				closeTurn() // a user message ends the current Claude turn
				continue
			}
			last = asstN
			asstN++
		}
	}
	closeTurn()
	return answers
}

// tocLabel derives a one-line TOC label from a turn's markdown text: the first
// non-empty line, with heading/quote/emphasis markers stripped and clipped.
func tocLabel(text string) string {
	for _, ln := range strings.Split(text, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		ln = oneLine(stripMarkers(strings.TrimLeft(ln, "#> ")))
		if ln != "" {
			return clip(ln, 100)
		}
	}
	return ""
}

// clip shortens s to at most n runes, appending an ellipsis when it truncates.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimRight(string(r[:n]), " ") + "…"
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
:root{--bar:rgba(255,255,255,.82)}
@media(prefers-color-scheme:dark){:root{--bar:rgba(15,17,21,.82)}}
*{box-sizing:border-box}
html{scroll-behavior:smooth}
body{margin:0;padding-top:38px;background:var(--bg);color:var(--fg);font:16px/1.6 -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,Helvetica,Arial,sans-serif}
header,main,footer{max-width:820px;margin:0 auto;padding:0 24px}
header{padding-top:32px}
header h1{margin:0;font-size:22px;color:var(--accent)}
.meta{color:var(--dim);font-size:13px;margin-top:4px}
details.toc{max-width:820px;margin:14px auto 0;padding:0 24px}
details.toc>summary{cursor:pointer;font-size:13px;font-weight:600;letter-spacing:.04em;text-transform:uppercase;color:var(--dim)}
details.toc>summary:hover{color:var(--accent)}
.toc nav{margin-top:8px;display:flex;flex-direction:column;gap:1px}
a.toc-item{display:flex;align-items:baseline;gap:8px;min-width:0;padding:2px 6px;border-radius:5px;color:var(--fg);text-decoration:none;font-size:13.5px;line-height:1.5}
a.toc-item:hover{background:var(--code-bg)}
.toc-mark{flex:0 0 1.1em;text-align:center;font-size:11px}
.toc-text{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
a.toc-user .toc-mark{color:var(--user)}
a.toc-assistant .toc-mark{color:var(--claude)}
main{padding-top:16px;padding-bottom:64px}
.turn{scroll-margin-top:48px}
.turn{padding:12px 0;border-top:1px solid var(--border)}
.turn .role{position:relative;font-weight:700;font-size:13px;text-transform:uppercase;letter-spacing:.04em;margin-bottom:6px}
.turn .role.has-toggle{cursor:pointer}
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
#toolbar{position:fixed;top:0;left:0;right:0;z-index:30;display:flex;align-items:center;justify-content:flex-end;gap:8px;height:38px;padding:0 16px;background:var(--bar);backdrop-filter:blur(8px);-webkit-backdrop-filter:blur(8px);border-bottom:1px solid var(--border)}
.tb-label{color:var(--dim);font-size:12px;text-transform:uppercase;letter-spacing:.04em}
.tb-states{display:flex;gap:4px}
.tb-state{width:24px;height:22px;border:1px solid var(--border);border-radius:5px;background:var(--bg);color:var(--dim);font:12px/1 ui-monospace,Menlo,monospace;cursor:pointer;padding:0}
.tb-state:hover{color:var(--fg)}
.tb-state.active{background:var(--accent);border-color:var(--accent);color:#fff}
/* Three detail levels for the Claude content:
   0 = questions + answers, 1 = + intermediate narration, 2 = + tool blocks. */
body.tech-0 .turn.assistant.step{display:none}
body.tech-0 .tech-group,body.tech-0 details.tech,body.tech-0 .toggle-all,
body.tech-1 .tech-group,body.tech-1 details.tech,body.tech-1 .toggle-all{display:none}
body.tech-0 .turn .role.has-toggle,body.tech-1 .turn .role.has-toggle{cursor:default}
@media print{#toolbar{display:none}}
`

// htmlScript toggles every collapsible block inside one tech-group at once. It
// is self-contained (no external assets) so the rendered file works offline.
const htmlScript = `<script>
function techGroupFor(btn){
  var g=btn.closest('.turn').nextElementSibling;
  return (g&&g.classList.contains('tech-group'))?g:null;
}
function toggleFor(btn){
  var g=techGroupFor(btn);
  if(!g)return;
  var items=g.querySelectorAll('details.tech');
  var open=Array.prototype.some.call(items,function(d){return !d.open;});
  items.forEach(function(d){d.open=open;});
  btn.textContent=open?'▾':'▸';
}
document.querySelectorAll('.toggle-all').forEach(function(btn){
  if(techGroupFor(btn)){
    btn.closest('.role').classList.add('has-toggle');
  }else{
    btn.style.display='none';
  }
});
document.addEventListener('click',function(ev){
  var btn=ev.target.closest('.toggle-all');
  if(!btn){
    // A click anywhere on a Claude role line toggles its group too.
    var role=ev.target.closest('.role.has-toggle');
    if(role)btn=role.querySelector('.toggle-all');
  }
  if(btn)toggleFor(btn);
});
(function(){
  var KEY='ic_tech_state',body=document.body;
  var btns=Array.prototype.slice.call(document.querySelectorAll('.tb-state'));
  var state=0;
  function apply(s){
    state=(s+3)%3;
    body.classList.remove('tech-0','tech-1','tech-2');
    body.classList.add('tech-'+state);
    btns.forEach(function(b){b.classList.toggle('active',+b.dataset.state===state);});
    try{localStorage.setItem(KEY,state);}catch(e){}
  }
  var start=0;try{var v=parseInt(localStorage.getItem(KEY),10);if(v>=0&&v<=2)start=v;}catch(e){}
  apply(start);
  btns.forEach(function(b){b.addEventListener('click',function(){apply(+b.dataset.state);});});
  document.addEventListener('keydown',function(ev){
    if(ev.key!=='t'||ev.metaKey||ev.ctrlKey||ev.altKey)return;
    var el=ev.target;
    if(el&&(el.tagName==='INPUT'||el.tagName==='TEXTAREA'||el.isContentEditable))return;
    apply(state+1);
  });
})();
</script>
`
