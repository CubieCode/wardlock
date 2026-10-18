// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"path"
	"regexp"
	"strings"
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
	lines := append(append([]string{}, defaultIgnored...), strings.Split(wardlockignore, "\n")...)
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
			end := strings.IndexByte(glob[i+1:], ']')
			if end < 0 {
				return "", fmt.Errorf("pattern %q: unclosed [", glob)
			}
			// "[]...]" starts a class with a literal "]".
			if end == 0 {
				next := strings.IndexByte(glob[i+2:], ']')
				if next < 0 {
					return "", fmt.Errorf("pattern %q: unclosed [", glob)
				}
				end = next + 1
			}
			class := glob[i+1 : i+1+end]
			if strings.HasPrefix(class, "!") {
				class = "^" + class[1:]
			}
			b.WriteString("[" + class + "]")
			i += end + 1
		default:
			b.WriteString(regexp.QuoteMeta(glob[i : i+1]))
		}
	}
	b.WriteString("$")
	return b.String(), nil
}
