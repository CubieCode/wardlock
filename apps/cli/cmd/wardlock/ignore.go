// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const ignoreFile = ".wardlockignore"

// alwaysIgnored are never deployed, at any depth, whatever .wardlockignore
// says (docs/SPEC.md "wardlock.json and .wardlockignore").
var alwaysIgnored = []string{".git/", "node_modules/", ".DS_Store", configFile, ignoreFile, ".env*"}

// defaultIgnored come before the patterns in .wardlockignore, which can
// override them: dotfiles are left out unless re-included with "!", except
// .well-known/.
var defaultIgnored = []string{".*", "!.well-known/"}

// ignoreRule is one .gitignore-syntax pattern.
type ignoreRule struct {
	re      *regexp.Regexp
	negate  bool
	dirOnly bool
	// anchored rules match the whole path; others match the last segment
	// at any depth.
	anchored bool
}

// ignorer decides which paths of a deploy are left out. Paths are relative
// to the deployed directory, with forward slashes.
type ignorer struct {
	always, rules []ignoreRule
}

// newIgnorer builds an ignorer from the contents of .wardlockignore, which
// may be empty.
func newIgnorer(wardlockignore string) (*ignorer, error) {
	ig := &ignorer{}
	for _, p := range alwaysIgnored {
		r, _, err := parseIgnoreLine(p)
		if err != nil {
			return nil, err
		}
		ig.always = append(ig.always, r)
	}
	// Paths are matched in NFC, so patterns are too.
	lines := append(append([]string{}, defaultIgnored...), strings.Split(norm.NFC.String(wardlockignore), "\n")...)
	for i, line := range lines {
		r, ok, err := parseIgnoreLine(line)
		if err != nil {
			return nil, fmt.Errorf("%s line %d: %w", ignoreFile, i+1-len(defaultIgnored), err)
		}
		if ok {
			ig.rules = append(ig.rules, r)
		}
	}
	return ig, nil
}

// ignored reports whether p is left out. Callers walk top down and skip an
// ignored directory's contents, so, as in Git, a file cannot be re-included
// when a parent directory is excluded.
func (ig *ignorer) ignored(p string, isDir bool) bool {
	for _, r := range ig.always {
		if r.matches(p, isDir) {
			return true
		}
	}
	out := false
	for _, r := range ig.rules {
		if r.matches(p, isDir) {
			out = !r.negate
		}
	}
	return out
}

func (r ignoreRule) matches(p string, isDir bool) bool {
	if r.dirOnly && !isDir {
		return false
	}
	if r.anchored {
		return r.re.MatchString(p)
	}
	return r.re.MatchString(path.Base(p))
}

// parseIgnoreLine parses one line of .gitignore syntax. ok is false for
// blank lines and comments.
func parseIgnoreLine(line string) (r ignoreRule, ok bool, err error) {
	line = strings.TrimSuffix(line, "\r")
	line = trimUnescapedSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return ignoreRule{}, false, nil
	}
	if strings.HasPrefix(line, "!") {
		r.negate = true
		line = line[1:]
	} else if strings.HasPrefix(line, `\!`) || strings.HasPrefix(line, `\#`) {
		line = line[1:]
	}
	if strings.HasSuffix(line, "/") && !strings.HasSuffix(line, `\/`) {
		r.dirOnly = true
		line = strings.TrimRight(line, "/")
	}
	if line == "" {
		return ignoreRule{}, false, nil
	}
	if strings.Contains(line, "/") {
		r.anchored = true
		line = strings.TrimPrefix(line, "/")
	}
	expr, err := globToRegexp(line)
	if err != nil {
		return ignoreRule{}, false, err
	}
	r.re, err = regexp.Compile(expr)
	if err != nil {
		return ignoreRule{}, false, fmt.Errorf("pattern %q: %w", line, err)
	}
	return r, true, nil
}

// trimUnescapedSpace removes trailing spaces unless escaped with "\".
func trimUnescapedSpace(s string) string {
	for strings.HasSuffix(s, " ") && !strings.HasSuffix(s, `\ `) {
		s = s[:len(s)-1]
	}
	return s
}

// globToRegexp translates a .gitignore glob into an anchored regular
// expression over slash-separated paths: "*" and "?" stay within a segment,
// "**" crosses segments, "[...]" is a character class.
func globToRegexp(glob string) (string, error) {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(glob); i++ {
		c := glob[i]
		switch {
		case strings.HasPrefix(glob[i:], "**/") && (i == 0 || glob[i-1] == '/'):
			b.WriteString("(?:.*/)?")
			i += 2
		case strings.HasPrefix(glob[i:], "**") && i+2 == len(glob) && (i == 0 || glob[i-1] == '/'):
			b.WriteString(".*")
			i++
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		case c == '\\' && i+1 < len(glob):
			i++
			b.WriteString(regexp.QuoteMeta(glob[i : i+1]))
		case c == '[':
			expr, end, err := globClass(glob, i)
			if err != nil {
				return "", fmt.Errorf("pattern %q: %w", glob, err)
			}
			if end < 0 {
				// As in Git, an unclosed "[" is a literal.
				b.WriteString(`\[`)
				continue
			}
			b.WriteString(expr)
			i = end
		default:
			b.WriteString(regexp.QuoteMeta(glob[i : i+1]))
		}
	}
	b.WriteString("$")
	return b.String(), nil
}

// posixClasses are the "[:name:]" classes a bracket expression may hold,
// as regexp class contents. None includes "/", which a bracket expression
// never matches, as in Git.
var posixClasses = map[string]string{
	"alnum":  "0-9A-Za-z",
	"alpha":  "A-Za-z",
	"ascii":  `\x00-.0-\x7f`,
	"blank":  `\t `,
	"cntrl":  `\x00-\x1f\x7f`,
	"digit":  "0-9",
	"graph":  "!-.0-~",
	"lower":  "a-z",
	"print":  " -.0-~",
	"punct":  "!-.:-@\\[-`{-~",
	"space":  `\t-\r `,
	"upper":  "A-Z",
	"xdigit": "0-9A-Fa-f",
}

// globClass translates the bracket expression starting at glob[i] ("[")
// into a regexp class that never matches "/", and returns the index of
// its closing "]". end is -1 when it is not closed.
func globClass(glob string, i int) (expr string, end int, err error) {
	j := i + 1
	negate := j < len(glob) && (glob[j] == '!' || glob[j] == '^')
	if negate {
		j++
	}
	var b strings.Builder
	for first := true; j < len(glob); first = false {
		if glob[j] == ']' && !first {
			if negate {
				return "[^/" + b.String() + "]", j, nil
			}
			if b.Len() == 0 {
				return `[^\x00-\x{10FFFF}]`, j, nil // only "/": matches nothing
			}
			return "[" + b.String() + "]", j, nil
		}
		if strings.HasPrefix(glob[j:], "[:") {
			if k := strings.Index(glob[j+2:], ":]"); k >= 0 {
				name := glob[j+2 : j+2+k]
				class, ok := posixClasses[name]
				if !ok {
					return "", 0, fmt.Errorf("unknown character class [:%s:]", name)
				}
				b.WriteString(class)
				j += k + 4
				continue
			}
		}
		lo, next := classRune(glob, j)
		hi := lo
		if next+1 < len(glob) && glob[next] == '-' && glob[next+1] != ']' {
			hi, next = classRune(glob, next+1)
		}
		j = next
		if lo > hi {
			continue // an empty range, as in Git
		}
		// Leave "/" out of the range.
		if lo < '/' {
			writeRange(&b, lo, min(hi, '.'))
		}
		if hi > '/' {
			writeRange(&b, max(lo, '0'), hi)
		}
	}
	return "", -1, nil
}

// classRune decodes one possibly escaped character of a bracket
// expression at glob[j] and returns it with the index after it.
func classRune(glob string, j int) (rune, int) {
	if glob[j] == '\\' && j+1 < len(glob) {
		j++
	}
	r, n := utf8.DecodeRuneInString(glob[j:])
	return r, j + n
}

func writeRange(b *strings.Builder, lo, hi rune) {
	b.WriteString(classLiteral(lo))
	if hi != lo {
		b.WriteString("-" + classLiteral(hi))
	}
}

// classLiteral writes r so a regexp class reads it as itself.
func classLiteral(r rune) string {
	if strings.ContainsRune(`\]-[^`, r) {
		return `\` + string(r)
	}
	return string(r)
}
