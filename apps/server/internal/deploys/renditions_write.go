// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package deploys

import (
	"regexp"
	"strconv"
	"strings"
)

// markdown prints blocks as CommonMark with GitHub tables and
// strikethrough, ending in one newline (or empty).
func markdown(blocks []block) string {
	return finish(mdBlocks(blocks))
}

// plainText prints blocks as plain text, ending in one newline (or empty).
func plainText(blocks []block) string {
	return finish(textBlocks(blocks))
}

func finish(s string) string {
	if s == "" {
		return ""
	}
	return s + "\n"
}

// joinBlocks separates rendered blocks with a blank line, except that a
// list follows the block before it directly inside a list item, keeping
// nested lists tight.
func joinBlocks(blocks []block, render func(block) string, inItem bool) string {
	var b strings.Builder
	for i, blk := range blocks {
		s := render(blk)
		if s == "" {
			continue
		}
		if b.Len() > 0 {
			if inItem && blk.kind == blockList && i > 0 {
				b.WriteString("\n")
			} else {
				b.WriteString("\n\n")
			}
		}
		b.WriteString(s)
	}
	return b.String()
}

// indent prefixes the first line of s with first and every later non-empty
// line with rest; empty lines take rest without its trailing spaces.
func indent(s, first, rest string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		switch {
		case i == 0:
			lines[i] = first + l
		case l == "":
			lines[i] = strings.TrimRight(rest, " ")
		default:
			lines[i] = rest + l
		}
	}
	return strings.Join(lines, "\n")
}

func listMarker(b block, i int) string {
	if b.ordered {
		return strconv.Itoa(b.start+i) + ". "
	}
	return "- "
}

// renderList prints each item with its marker, hanging later lines under
// the item's first.
func renderList(b block, items func([]block) string) string {
	out := make([]string, 0, len(b.items))
	for i, item := range b.items {
		marker := listMarker(b, i)
		body := items(item)
		if body == "" {
			out = append(out, strings.TrimRight(marker, " "))
			continue
		}
		out = append(out, indent(body, marker, strings.Repeat(" ", len(marker))))
	}
	return strings.Join(out, "\n")
}

// Markdown.

func mdBlocks(blocks []block) string { return joinBlocks(blocks, mdBlock, false) }

func mdItem(blocks []block) string { return joinBlocks(blocks, mdBlock, true) }

func mdBlock(b block) string {
	switch b.kind {
	case blockHeading:
		return strings.Repeat("#", b.level) + " " + oneLine(mdInlines(b.inlines, true))
	case blockPara:
		return mdInlines(b.inlines, true)
	case blockList:
		return renderList(b, mdItem)
	case blockCode:
		fence := strings.Repeat("`", max(3, longestRun(b.code, '`')+1))
		return fence + b.lang + "\n" + b.code + "\n" + fence
	case blockQuote:
		return indent(mdBlocks(b.children), "> ", "> ")
	case blockTable:
		return mdTable(b.rows)
	case blockRule:
		return "---"
	}
	return ""
}

func mdTable(rows [][][]inline) string {
	cols := 0
	for _, r := range rows {
		cols = max(cols, len(r))
	}
	line := func(r [][]inline) string {
		var b strings.Builder
		b.WriteString("|")
		for i := range cols {
			cell := ""
			if i < len(r) {
				cell = strings.ReplaceAll(oneLine(mdInlines(r[i], false)), "|", `\|`)
			}
			b.WriteString(" " + cell + " |")
		}
		return strings.ReplaceAll(b.String(), "  |", " |")
	}
	out := []string{line(rows[0]), "|" + strings.Repeat(" --- |", cols)}
	for _, r := range rows[1:] {
		out = append(out, line(r))
	}
	return strings.Join(out, "\n")
}

// oneLine turns line breaks into spaces, for headings and table cells.
func oneLine(s string) string { return strings.ReplaceAll(s, "\\\n", " ") }

// mdInlines prints inline content. atStart is whether it begins a line,
// where block syntax (#, -, 1.) must be escaped.
func mdInlines(in []inline, atStart bool) string {
	w := mdWriter{start: atStart}
	w.inlines(in)
	return w.b.String()
}

type mdWriter struct {
	b     strings.Builder
	start bool // at the start of a line
}

func (w *mdWriter) write(s string) {
	if s != "" {
		w.b.WriteString(s)
		w.start = false
	}
}

func (w *mdWriter) inlines(in []inline) {
	for _, x := range in {
		switch x.kind {
		case inlineText:
			w.write(escapeMD(x.text, w.start))
		case inlineBreak:
			w.b.WriteString("\\\n")
			w.start = true
		case inlineStrong:
			w.wrap("**", x.children)
		case inlineEm:
			w.wrap("*", x.children)
		case inlineDel:
			w.wrap("~~", x.children)
		case inlineCode:
			if x.text != "" { // redaction can empty a span
				w.write(codeSpan(x.text))
			}
		case inlineLink:
			w.link(x)
		case inlineImage:
			if x.href == "" {
				w.write(escapeMD(x.text, w.start))
			} else {
				w.write("![" + escapeMD(x.text, false) + "](" + mdURL(x.href) + ")")
			}
		}
	}
}

// wrap prints children between delimiters, moving edge spaces outside them
// since CommonMark ignores delimiters next to a space on the inside.
func (w *mdWriter) wrap(delim string, children []inline) {
	inner := mdInlines(children, w.start)
	core := strings.TrimSpace(inner)
	if core == "" {
		w.write(inner)
		return
	}
	lead := inner[:strings.Index(inner, core)]
	trail := inner[len(lead)+len(core):]
	w.write(lead + delim + core + delim + trail)
}

func (w *mdWriter) link(x inline) {
	if x.href == "" {
		w.write(oneLine(mdInlines(x.children, w.start)))
		return
	}
	text := oneLine(mdInlines(x.children, false))
	switch {
	case text == "" || text == escapeMD(x.href, false):
		if strings.Contains(x.href, ":") {
			w.write("<" + strings.ReplaceAll(x.href, ">", "%3E") + ">")
		} else {
			w.write("[" + escapeMD(x.href, false) + "](" + mdURL(x.href) + ")")
		}
	default:
		w.write("[" + text + "](" + mdURL(x.href) + ")")
	}
}

// mdURL makes a URL safe as a Markdown link destination.
func mdURL(u string) string {
	return strings.NewReplacer(" ", "%20", "(", "%28", ")", "%29", "<", "%3C", ">", "%3E").Replace(u)
}

// codeSpan wraps s in enough backticks, padded with spaces where s starts
// or ends with one.
func codeSpan(s string) string {
	fence := strings.Repeat("`", longestRun(s, '`')+1)
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") {
		s = " " + s + " "
	}
	return fence + s + fence
}

func longestRun(s string, c byte) int {
	best, run := 0, 0
	for i := range len(s) {
		if s[i] == c {
			run++
			best = max(best, run)
		} else {
			run = 0
		}
	}
	return best
}

// escapeMD backslash-escapes the characters that would otherwise read as
// Markdown inline syntax and, at the start of a line, as block syntax.
func escapeMD(s string, atStart bool) string {
	var b strings.Builder
	for i := range len(s) {
		switch c := s[i]; {
		case strings.IndexByte("\\`*_[]<>~", c) >= 0, c == '&' && entityAt(s[i:]):
			b.WriteByte('\\')
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	out := b.String()
	if !atStart {
		return out
	}
	switch {
	case strings.HasPrefix(out, "#"), strings.HasPrefix(out, "|"), strings.HasPrefix(out, "+ "), strings.HasPrefix(out, "- "),
		strings.HasPrefix(out, "="), out == "-", out == "+":
		return `\` + out
	}
	// An ordered list marker: digits then . or ).
	digits := 0
	for digits < len(out) && digits < 10 && out[digits] >= '0' && out[digits] <= '9' {
		digits++
	}
	if digits > 0 && digits < len(out) && (out[digits] == '.' || out[digits] == ')') {
		return out[:digits] + `\` + out[digits:]
	}
	return out
}

// entityAt reports whether s starts with what CommonMark reads as an
// entity or numeric character reference (&copy;, &#169;, &#xA9;), which
// decoded text such as "&copy;" must not turn back into.
func entityAt(s string) bool {
	return entityPattern.MatchString(s)
}

var entityPattern = regexp.MustCompile(`^&(?:[A-Za-z][A-Za-z0-9]{0,31}|#[0-9]{1,7}|#[xX][0-9A-Fa-f]{1,6});`)

// Plain text.

func textBlocks(blocks []block) string { return joinBlocks(blocks, textBlock, false) }

func textItem(blocks []block) string { return joinBlocks(blocks, textBlock, true) }

func textBlock(b block) string {
	switch b.kind {
	case blockHeading:
		return strings.ReplaceAll(textInlines(b.inlines), "\n", " ")
	case blockPara:
		return textInlines(b.inlines)
	case blockList:
		return renderList(b, textItem)
	case blockCode:
		return b.code
	case blockQuote:
		return indent(textBlocks(b.children), "> ", "> ")
	case blockTable:
		lines := make([]string, 0, len(b.rows))
		for _, r := range b.rows {
			cells := make([]string, len(r))
			for i, c := range r {
				cells[i] = strings.NewReplacer("\n", " ", "\t", " ").Replace(textInlines(c))
			}
			lines = append(lines, strings.Join(cells, "\t"))
		}
		return strings.Join(lines, "\n")
	case blockRule:
		return "---"
	}
	return ""
}

func textInlines(in []inline) string {
	var b strings.Builder
	for _, x := range in {
		switch x.kind {
		case inlineText, inlineCode:
			b.WriteString(x.text)
		case inlineBreak:
			b.WriteString("\n")
		case inlineStrong, inlineEm, inlineDel:
			b.WriteString(textInlines(x.children))
		case inlineLink:
			text := strings.ReplaceAll(textInlines(x.children), "\n", " ")
			switch {
			case x.href == "" || text == x.href:
				b.WriteString(text)
			case text == "":
				b.WriteString(x.href)
			default:
				b.WriteString(text + " (" + x.href + ")")
			}
		case inlineImage:
			b.WriteString("[" + x.text + "]")
		}
	}
	return b.String()
}
