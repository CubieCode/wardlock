// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package deploys

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// The conversion (SPEC "AI-readable renditions") reads an HTML page into a
// small document model of blocks and inlines, which renditions_write.go
// prints as Markdown and as plain text, so both formats keep exactly the
// same content.

type blockKind int

const (
	blockPara blockKind = iota
	blockHeading
	blockList
	blockCode
	blockQuote
	blockTable
	blockRule
)

type block struct {
	kind     blockKind
	level    int          // heading: 1 to 6
	inlines  []inline     // para, heading
	ordered  bool         // list
	start    int          // ordered list: first number
	items    [][]block    // list
	lang     string       // code: language from a language-* or lang-* class
	code     string       // code: verbatim text
	children []block      // quote
	rows     [][][]inline // table: rows of cells; the first row is the header
}

type inlineKind int

const (
	inlineText inlineKind = iota
	inlineBreak
	inlineStrong
	inlineEm
	inlineDel
	inlineCode
	inlineLink
	inlineImage
)

type inline struct {
	kind     inlineKind
	text     string   // text, code, image alt
	href     string   // link target, image source; "" when unusable
	children []inline // strong, em, del, link
}

// maxDepth bounds the recursion over the element tree. Content nested more
// deeply is kept as plain text.
const maxDepth = 128

type converter struct {
	base   *url.URL // resolves the page's references
	origin *url.URL // the site's address; nil keeps links root-relative
}

// convert parses page, an HTML document served at pageURL, into blocks
// with every token redacted. pageURL is absolute when the site's address
// is known and otherwise root-relative. Invalid UTF-8 becomes U+FFFD.
func convert(page []byte, pageURL *url.URL) []block {
	blocks := parse(bytes.ToValidUTF8(page, []byte("\uFFFD")), pageURL)
	scrubBlocks(blocks)
	return blocks
}

func parse(page []byte, pageURL *url.URL) []block {
	doc, err := html.Parse(bytes.NewReader(page))
	if err != nil {
		// The parser refuses pathological nesting (over 512 open elements).
		return fallback(page)
	}
	c := &converter{base: pageURL}
	if pageURL.Host != "" {
		c.origin = &url.URL{Scheme: pageURL.Scheme, Host: pageURL.Host}
	}
	var head, body *html.Node
	for n := range doc.Descendants() {
		switch {
		case n.Type != html.ElementNode:
		case n.DataAtom == atom.Head && head == nil:
			head = n
		case n.DataAtom == atom.Body && body == nil:
			body = n
		}
	}
	var title string
	if head != nil {
		for n := range head.Descendants() {
			if n.Type != html.ElementNode {
				continue
			}
			switch n.DataAtom {
			case atom.Title:
				if title == "" {
					title = collapse(strings.TrimSpace(textContent(n)))
				}
			case atom.Base:
				// Only the first <base href> counts, and only one that
				// stays on the page's scheme and host.
				if href, ok := attr(n, "href"); ok && c.base == pageURL {
					if u, err := pageURL.Parse(strings.TrimSpace(href)); err == nil && u.Scheme == pageURL.Scheme && u.Host == pageURL.Host {
						c.base = u
					}
				}
			}
		}
	}
	var blocks []block
	if body != nil {
		blocks = c.blocks(body, 0)
	}
	if title != "" && !hasH1(blocks) {
		blocks = append([]block{{kind: blockHeading, level: 1, inlines: []inline{{kind: inlineText, text: title}}}}, blocks...)
	}
	return blocks
}

// fallback keeps the text of a page the parser refuses, as one paragraph,
// still leaving out the contents of elements skipped would drop.
func fallback(page []byte) []block {
	z := html.NewTokenizer(bytes.NewReader(page))
	var text []inline
	skip := 0 // depth inside skipped elements
	for {
		switch z.Next() {
		case html.ErrorToken:
			if inl := normalise(text); len(inl) > 0 {
				return []block{{kind: blockPara, inlines: inl}}
			}
			return nil
		case html.StartTagToken:
			name, _ := z.TagName()
			// Void elements (input, meta, link…) have no end tag to wait for.
			if a := atom.Lookup(name); skippedTag(a) && !voidTag(a) {
				skip++
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			if skippedTag(atom.Lookup(name)) && skip > 0 {
				skip--
			}
		case html.TextToken:
			if skip == 0 {
				text = append(text, inline{kind: inlineText, text: string(z.Text()) + " "})
			}
		}
	}
}

func voidTag(a atom.Atom) bool {
	switch a {
	case atom.Area, atom.Base, atom.Br, atom.Col, atom.Embed, atom.Hr, atom.Img, atom.Input, atom.Link, atom.Meta,
		atom.Source, atom.Track, atom.Wbr:
		return true
	}
	return false
}

func hasH1(blocks []block) bool {
	for _, b := range blocks {
		if b.kind == blockHeading && b.level == 1 {
			return true
		}
		if hasH1(b.children) {
			return true
		}
		for _, item := range b.items {
			if hasH1(item) {
				return true
			}
		}
	}
	return false
}

// skipped reports whether n and everything in it are left out: scripts,
// styles, forms, embedded media and anything hidden.
func skipped(n *html.Node) bool {
	if skippedTag(n.DataAtom) {
		return true
	}
	if _, ok := attr(n, "hidden"); ok {
		return true
	}
	if v, _ := attr(n, "aria-hidden"); strings.EqualFold(strings.TrimSpace(v), "true") {
		return true
	}
	return false
}

// skippedTag reports whether elements named a are always left out.
func skippedTag(a atom.Atom) bool {
	switch a {
	case atom.Script, atom.Style, atom.Noscript, atom.Template, atom.Head, atom.Title, atom.Meta, atom.Link, atom.Base,
		atom.Form, atom.Input, atom.Button, atom.Select, atom.Textarea, atom.Option, atom.Optgroup, atom.Datalist, atom.Output,
		atom.Iframe, atom.Object, atom.Embed, atom.Svg, atom.Math, atom.Canvas, atom.Audio, atom.Video, atom.Source, atom.Track,
		atom.Map, atom.Dialog:
		return true
	}
	return false
}

// isBlock reports whether n starts a block of its own.
func isBlock(n *html.Node) bool {
	switch n.DataAtom {
	case atom.Address, atom.Article, atom.Aside, atom.Blockquote, atom.Body, atom.Center, atom.Details, atom.Dd, atom.Div,
		atom.Dl, atom.Dt, atom.Fieldset, atom.Figcaption, atom.Figure, atom.Footer, atom.H1, atom.H2, atom.H3, atom.H4,
		atom.H5, atom.H6, atom.Header, atom.Hgroup, atom.Hr, atom.Html, atom.Legend, atom.Li, atom.Main, atom.Nav, atom.Ol,
		atom.P, atom.Pre, atom.Search, atom.Section, atom.Summary, atom.Table, atom.Caption, atom.Thead, atom.Tbody,
		atom.Tfoot, atom.Tr, atom.Td, atom.Th, atom.Ul:
		return true
	}
	return false
}

// blocks converts n's children. Runs of inline content between blocks
// become paragraphs.
func (c *converter) blocks(n *html.Node, depth int) []block {
	var out []block
	var run []inline
	flush := func() {
		if inl := normalise(run); len(inl) > 0 {
			out = append(out, block{kind: blockPara, inlines: inl})
		}
		run = nil
	}
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		switch {
		case ch.Type == html.TextNode:
			run = append(run, inline{kind: inlineText, text: ch.Data})
		case ch.Type != html.ElementNode || skipped(ch):
		case depth >= maxDepth:
			run = append(run, inline{kind: inlineText, text: " " + textContent(ch) + " "})
		case isBlock(ch):
			flush()
			out = append(out, c.block(ch, depth+1)...)
		default:
			run = append(run, c.inline(ch, depth+1)...)
		}
	}
	flush()
	return out
}

// block converts one block element.
func (c *converter) block(n *html.Node, depth int) []block {
	switch n.DataAtom {
	case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
		inl := normalise(c.inlines(n, depth))
		if len(inl) == 0 {
			return nil
		}
		return []block{{kind: blockHeading, level: int(n.Data[1] - '0'), inlines: inl}}
	case atom.P:
		if inl := normalise(c.inlines(n, depth)); len(inl) > 0 {
			return []block{{kind: blockPara, inlines: inl}}
		}
		return nil
	case atom.Ul, atom.Ol:
		return c.list(n, depth)
	case atom.Pre:
		return []block{c.pre(n)}
	case atom.Blockquote:
		if children := c.blocks(n, depth); len(children) > 0 {
			return []block{{kind: blockQuote, children: children}}
		}
		return nil
	case atom.Table:
		return c.table(n, depth)
	case atom.Hr:
		return []block{{kind: blockRule}}
	}
	return c.blocks(n, depth)
}

func (c *converter) list(n *html.Node, depth int) []block {
	b := block{kind: blockList, ordered: n.DataAtom == atom.Ol, start: 1}
	if v, ok := attr(n, "start"); ok && b.ordered {
		if i, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && i >= 0 && i < 1_000_000_000 {
			b.start = i
		}
	}
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		if ch.Type != html.ElementNode || ch.DataAtom != atom.Li || skipped(ch) {
			continue
		}
		b.items = append(b.items, c.blocks(ch, depth+1))
	}
	if len(b.items) == 0 {
		return nil
	}
	return []block{b}
}

func (c *converter) pre(n *html.Node) block {
	b := block{kind: blockCode, code: strings.TrimRight(textContent(n), "\n\r\t ")}
	b.code = strings.TrimLeft(b.code, "\n\r")
	b.lang = language(n)
	if b.lang == "" {
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			if ch.Type == html.ElementNode && ch.DataAtom == atom.Code {
				b.lang = language(ch)
				break
			}
		}
	}
	return b
}

// language returns the x of a language-x or lang-x class, if it is a plain
// word that cannot break the code fence.
func language(n *html.Node) string {
	class, _ := attr(n, "class")
	for f := range strings.FieldsSeq(class) {
		for _, prefix := range []string{"language-", "lang-"} {
			if l, ok := strings.CutPrefix(f, prefix); ok && l != "" && plainWord(l) {
				return l
			}
		}
	}
	return ""
}

func plainWord(s string) bool {
	for _, r := range s {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_+#.", r)
		if !ok {
			return false
		}
	}
	return true
}

func (c *converter) table(n *html.Node, depth int) []block {
	var out []block
	var rows [][][]inline
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			if ch.Type != html.ElementNode || skipped(ch) {
				continue
			}
			switch ch.DataAtom {
			case atom.Caption:
				if inl := normalise(c.inlines(ch, depth)); len(inl) > 0 {
					out = append(out, block{kind: blockPara, inlines: inl})
				}
			case atom.Thead, atom.Tbody, atom.Tfoot:
				walk(ch)
			case atom.Tr:
				var row [][]inline
				for cell := ch.FirstChild; cell != nil; cell = cell.NextSibling {
					if cell.Type == html.ElementNode && (cell.DataAtom == atom.Td || cell.DataAtom == atom.Th) && !skipped(cell) {
						row = append(row, normalise(c.inlines(cell, depth+1)))
					}
				}
				// Rows with nothing in them would print as blank lines.
				if slices.ContainsFunc(row, func(cell []inline) bool { return len(cell) > 0 }) {
					rows = append(rows, row)
				}
			}
		}
	}
	walk(n)
	if len(rows) > 0 {
		out = append(out, block{kind: blockTable, rows: rows})
	}
	return out
}

// inlines converts n's children as inline content. Blocks nested inside
// inline content are flattened into it.
func (c *converter) inlines(n *html.Node, depth int) []inline {
	var out []inline
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		switch {
		case ch.Type == html.TextNode:
			out = append(out, inline{kind: inlineText, text: ch.Data})
		case ch.Type != html.ElementNode || skipped(ch):
		case depth >= maxDepth:
			out = append(out, inline{kind: inlineText, text: " " + textContent(ch) + " "})
		case isBlock(ch):
			out = append(out, inline{kind: inlineText, text: " "})
			out = append(out, c.inlines(ch, depth+1)...)
			out = append(out, inline{kind: inlineText, text: " "})
		default:
			out = append(out, c.inline(ch, depth+1)...)
		}
	}
	return out
}

// inline converts one inline element.
func (c *converter) inline(n *html.Node, depth int) []inline {
	switch n.DataAtom {
	case atom.Br:
		return []inline{{kind: inlineBreak}}
	case atom.Img:
		alt, _ := attr(n, "alt")
		if alt = collapse(strings.TrimSpace(alt)); alt == "" {
			return nil // decorative
		}
		src, _ := attr(n, "src")
		return []inline{{kind: inlineImage, text: alt, href: c.ref(src)}}
	case atom.A:
		href, ok := attr(n, "href")
		if !ok {
			return c.inlines(n, depth)
		}
		link := inline{kind: inlineLink, href: c.ref(href), children: c.inlines(n, depth)}
		if link.href != "" && c.showsOwnURL(n, href) {
			// The text is the URL itself, which may hold what ref removed:
			// show the cleaned URL instead.
			link.children = nil
		}
		return []inline{link}
	case atom.Strong, atom.B:
		return []inline{{kind: inlineStrong, children: c.inlines(n, depth)}}
	case atom.Em, atom.I:
		return []inline{{kind: inlineEm, children: c.inlines(n, depth)}}
	case atom.Del, atom.S, atom.Strike:
		return []inline{{kind: inlineDel, children: c.inlines(n, depth)}}
	case atom.Code, atom.Kbd, atom.Samp, atom.Tt:
		return []inline{{kind: inlineCode, text: textContent(n)}}
	}
	return c.inlines(n, depth)
}

// ref resolves a link or image reference against the page. It returns ""
// for a reference that is unusable outside the page: unparseable, or a
// scheme other than http, https, mailto and tel (javascript:, data: and the
// like). Credentials in the URL are dropped, and so is every share-link
// token: the t parameter of a link to the site itself, and any parameter
// (name or value) or fragment holding a Wardlock token, even
// percent-encoded, wherever the link points. A token in the path is
// redacted.
func (c *converter) ref(raw string) string {
	u, err := c.resolve(raw)
	if err != nil {
		return ""
	}
	switch strings.ToLower(u.Scheme) {
	case "", "http", "https", "mailto", "tel":
	default:
		return ""
	}
	u.User = nil
	if u.RawQuery != "" {
		sameSite := u.Host == "" || (c.origin != nil && strings.EqualFold(u.Hostname(), c.origin.Hostname()))
		q, err := url.ParseQuery(u.RawQuery)
		if err != nil {
			// Unparseable queries cannot be cleaned reliably; drop them.
			u.RawQuery = ""
		} else {
			changed := false
			for k, vs := range q {
				if (sameSite && k == "t") || hasToken(k) || slices.ContainsFunc(vs, hasToken) {
					delete(q, k)
					changed = true
				}
			}
			if changed {
				u.RawQuery = q.Encode()
			}
		}
	}
	if hasToken(u.Fragment) {
		u.Fragment, u.RawFragment = "", ""
	}
	if hasToken(u.Path) {
		u.Path, u.RawPath = scrubTokens(u.Path), ""
	}
	if hasToken(u.Opaque) {
		u.Opaque = scrubTokens(u.Opaque)
	}
	return scrubTokens(u.String())
}

// resolve parses raw against the page's base.
func (c *converter) resolve(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("empty reference")
	}
	u, err := c.base.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse reference: %w", err)
	}
	return u, nil
}

// tokenPattern matches a Wardlock token (SPEC "Tokens": wl_<public id>_<secret>)
// anywhere, in any letter case, as the access log's scrubber does. The
// secret runs to the end of the base64url alphabet, which includes "_".
var tokenPattern = regexp.MustCompile(`(?i)wl_([a-z2-7]{10})_[A-Za-z0-9_-]+`)

// redacted replaces a token's secret, keeping the public ID that names it.
const redacted = "wl_${1}_redacted"

// scrubTokens redacts every token in s.
func scrubTokens(s string) string {
	if !strings.Contains(strings.ToLower(s), "wl") {
		return s
	}
	return tokenPattern.ReplaceAllString(s, redacted)
}

// hasToken reports whether s holds a token as is or after up to three
// rounds of percent-decoding.
func hasToken(s string) bool {
	for range 4 {
		if tokenPattern.MatchString(s) {
			return true
		}
		d, err := url.QueryUnescape(s)
		if err != nil || d == s {
			return false
		}
		s = d
	}
	return false
}

// scrubBlocks redacts tokens in every text of blocks: body text, alt text,
// code and link targets alike.
func scrubBlocks(blocks []block) {
	for i := range blocks {
		b := &blocks[i]
		b.code = scrubTokens(b.code)
		scrubInlines(b.inlines)
		scrubBlocks(b.children)
		for _, item := range b.items {
			scrubBlocks(item)
		}
		for _, row := range b.rows {
			for _, cell := range row {
				scrubInlines(cell)
			}
		}
	}
}

func scrubInlines(in []inline) {
	for i := range in {
		in[i].text = scrubTokens(in[i].text)
		in[i].href = scrubTokens(in[i].href)
		scrubInlines(in[i].children)
	}
}

// showsOwnURL reports whether link n's text is its href, as written or
// resolved.
func (c *converter) showsOwnURL(n *html.Node, href string) bool {
	text := collapse(strings.TrimSpace(textContent(n)))
	if text == "" {
		return false
	}
	if text == strings.TrimSpace(href) {
		return true
	}
	u, err := c.resolve(href)
	return err == nil && text == u.String()
}

func attr(n *html.Node, key string) (string, bool) {
	for _, a := range n.Attr {
		if a.Namespace == "" && a.Key == key {
			return a.Val, true
		}
	}
	return "", false
}

// textContent returns the text inside n, skipping what skipped leaves
// out. It walks iteratively, so any depth is safe.
func textContent(n *html.Node) string {
	var b strings.Builder
	stack := []*html.Node{n}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		switch {
		case cur.Type == html.TextNode:
			b.WriteString(cur.Data)
			continue
		case cur != n && cur.Type == html.ElementNode && skipped(cur):
			continue
		case cur.Type == html.ElementNode && cur.DataAtom == atom.Br:
			b.WriteByte('\n')
		}
		for ch := cur.LastChild; ch != nil; ch = ch.PrevSibling {
			stack = append(stack, ch)
		}
	}
	return b.String()
}

// collapse turns every run of HTML whitespace into one space.
func collapse(s string) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\f' {
			if !space {
				b.WriteByte(' ')
			}
			space = true
			continue
		}
		space = false
		b.WriteRune(r)
	}
	return b.String()
}

// normalise collapses whitespace the way a browser lays out inline
// content: runs become one space, none at the start or end or around a
// line break, and empty elements disappear. Each step walks the content,
// or one path through it, once, so nesting depth costs linear time.
func normalise(in []inline) []inline {
	n := normaliser{space: true}
	return trimTrailing(trimLeading(dropEmpty(n.inlines(in))))
}

// trimLeading removes line breaks at the start of in, however deeply they
// are nested, and the elements that leaves empty. It descends only into
// the first element.
func trimLeading(in []inline) []inline {
	for len(in) > 0 {
		x := in[0]
		switch x.kind {
		case inlineBreak:
			in = in[1:]
			continue
		case inlineStrong, inlineEm, inlineDel, inlineLink:
			x.children = trimLeading(x.children)
			if len(x.children) == 0 && (x.kind != inlineLink || x.href == "") {
				in = in[1:]
				continue
			}
			in[0] = x
		}
		return in
	}
	return in
}

// trimTrailing removes line breaks and spaces at the end of in, however
// deeply they are nested, and the elements that leaves empty. It descends
// only into the last element.
func trimTrailing(in []inline) []inline {
	for len(in) > 0 {
		last := len(in) - 1
		x := in[last]
		switch x.kind {
		case inlineBreak:
			in = in[:last]
			continue
		case inlineText:
			if x.text = strings.TrimRight(x.text, " "); x.text == "" {
				in = in[:last]
				continue
			}
			in[last] = x
		case inlineStrong, inlineEm, inlineDel, inlineLink:
			x.children = trimTrailing(x.children)
			if len(x.children) == 0 && (x.kind != inlineLink || x.href == "") {
				in = in[:last]
				continue
			}
			in[last] = x
		}
		return in
	}
	return in
}

type normaliser struct{ space bool } // the output so far ends in a space, or is empty

func (n *normaliser) inlines(in []inline) []inline {
	var out []inline
	for _, x := range in {
		switch x.kind {
		case inlineText:
			t := collapse(x.text)
			if n.space {
				t = strings.TrimLeft(t, " ")
			}
			if t == "" {
				continue
			}
			n.space = strings.HasSuffix(t, " ")
			x.text = t
		case inlineBreak:
			trimEnd(out)
			n.space = true
		case inlineCode:
			x.text = strings.TrimSpace(collapse(x.text))
			if x.text == "" {
				continue
			}
			n.space = false
		case inlineImage:
			n.space = false
		default:
			x.children = n.inlines(x.children)
			if len(x.children) == 0 && (x.kind != inlineLink || x.href == "") {
				continue
			}
			if len(x.children) == 0 {
				n.space = false
			}
		}
		out = append(out, x)
	}
	return out
}

// trimEnd removes trailing spaces from the last text in in, however deeply
// it is nested.
func trimEnd(in []inline) {
	if len(in) == 0 {
		return
	}
	last := &in[len(in)-1]
	switch last.kind {
	case inlineText:
		last.text = strings.TrimRight(last.text, " ")
	case inlineStrong, inlineEm, inlineDel, inlineLink:
		trimEnd(last.children)
	}
}

// dropEmpty removes texts and elements trimming emptied.
func dropEmpty(in []inline) []inline {
	out := in[:0]
	for _, x := range in {
		switch x.kind {
		case inlineText:
			if x.text == "" {
				continue
			}
		case inlineStrong, inlineEm, inlineDel, inlineLink:
			x.children = dropEmpty(x.children)
			if len(x.children) == 0 && (x.kind != inlineLink || x.href == "") {
				continue
			}
		}
		out = append(out, x)
	}
	return out
}
