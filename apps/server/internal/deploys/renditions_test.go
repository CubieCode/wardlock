// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package deploys_test

import (
	"bytes"
	"flag"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"wardlock.nz/server/internal/deploys"
)

var update = flag.Bool("update", false, "rewrite the rendition golden files in testdata/renditions")

// fixtureSites is fixtures/sites, relative to this package.
var fixtureSites = filepath.Join("..", "..", "..", "..", "fixtures", "sites")

// TestRenditionGoldens renders every HTML file under fixtures/sites as if
// published at https://<fixture>.wardlock.test and compares the result with
// testdata/renditions/<fixture>/<path>.md and .txt. Run with -update to
// rewrite them, then review the diff.
func TestRenditionGoldens(t *testing.T) {
	seen := map[string]bool{}
	err := filepath.WalkDir(fixtureSites, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !deploys.HasRenditions(p) {
			return err
		}
		rel, _ := filepath.Rel(fixtureSites, p)
		rel = filepath.ToSlash(rel)
		site, file, _ := strings.Cut(rel, "/")
		t.Run(rel, func(t *testing.T) {
			page, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			u, err := deploys.PageURL("https://"+site+".wardlock.test", file)
			if err != nil {
				t.Fatal(err)
			}
			md, txt := deploys.Render(page, u)
			for ext, got := range map[string][]byte{".md": md, ".txt": txt} {
				golden := filepath.Join("testdata", "renditions", filepath.FromSlash(rel)+ext)
				seen[golden] = true
				if *update {
					if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(golden, got, 0o644); err != nil {
						t.Fatal(err)
					}
					continue
				}
				want, err := os.ReadFile(golden)
				if err != nil {
					t.Fatalf("%v (run go test -update to create it)", err)
				}
				if !bytes.Equal(got, want) {
					t.Errorf("%s differs from the golden file:\n--- got ---\n%s\n--- want ---\n%s", golden, got, want)
				}
			}
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Every golden file belongs to a fixture page.
	err = filepath.WalkDir(filepath.Join("testdata", "renditions"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && !seen[p] {
			t.Errorf("%s has no fixture page", p)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

const tok = "wl_abcdefghij_fakeSecret-fake_secretfakesecretfakesecret00"

func TestRender(t *testing.T) {
	for _, tc := range []struct {
		name, origin, path, page, md, txt string
	}{
		{
			name: "empty page", path: "index.html", page: "<!doctype html><title></title>",
			md: "", txt: "",
		},
		{
			name: "root-relative without an origin", path: "docs/a.html",
			page: `<p><a href="b.html?t=wl_x_y&amp;x=1">B</a> <img src="../i.png" alt="I"></p>`,
			md:   "[B](/docs/b.html?x=1) ![I](/i.png)\n", txt: "B (/docs/b.html?x=1) [I]\n",
		},
		{
			name: "same-site t dropped, foreign t kept", origin: "https://s.wardlock.test", path: "index.html",
			page: `<a href="https://s.wardlock.test/x?t=abc">S</a> <a href="https://S.wardlock.test/x?t=abc">U</a> <a href="https://o.test/x?t=abc">O</a>`,
			md:   "[S](https://s.wardlock.test/x) [U](https://S.wardlock.test/x) [O](https://o.test/x?t=abc)\n",
			txt:  "S (https://s.wardlock.test/x) U (https://S.wardlock.test/x) O (https://o.test/x?t=abc)\n",
		},
		{
			name: "tokens in fragments and credentials dropped", origin: "https://s.wardlock.test", path: "index.html",
			page: `<a href="https://user:pw@o.test/#t=wl_abcdefghij_s3cr3t">F</a>`,
			md:   "[F](https://o.test/)\n", txt: "F (https://o.test/)\n",
		},
		{
			name: "unsafe schemes", origin: "https://s.wardlock.test", path: "index.html",
			page: `<a href="data:text/html,x">D</a> <a href=" JavaScript:x">J</a> <img src="javascript:x" alt="Alt">`,
			md:   "D J Alt\n", txt: "D J [Alt]\n",
		},
		{
			name: "base href on the same origin", origin: "https://s.wardlock.test", path: "index.html",
			page: `<base href="/v2/"><base href="/v3/"><a href="a">A</a>`,
			md:   "[A](https://s.wardlock.test/v2/a)\n", txt: "A (https://s.wardlock.test/v2/a)\n",
		},
		{
			name: "base href elsewhere ignored", origin: "https://s.wardlock.test", path: "index.html",
			page: `<base href="https://evil.test/"><a href="a">A</a>`,
			md:   "[A](https://s.wardlock.test/a)\n", txt: "A (https://s.wardlock.test/a)\n",
		},
		{
			name: "link without text", origin: "https://s.wardlock.test", path: "index.html",
			page: `<a href="/x"><img src="/i.png" alt=""></a>`,
			md:   "<https://s.wardlock.test/x>\n", txt: "https://s.wardlock.test/x\n",
		},
		{
			name: "emphasis spacing", path: "index.html",
			page: `<p>a<strong> b </strong>c<em></em>d</p>`,
			md:   "a **b** cd\n", txt: "a b cd\n",
		},
		{
			name: "aria-hidden and hidden", path: "index.html",
			page: `<p aria-hidden="TRUE">x</p><span hidden>y</span><p aria-hidden="false">z</p>`,
			md:   "z\n", txt: "z\n",
		},
		{
			name: "token nested in another parameter", origin: "https://s.wardlock.test", path: "index.html",
			page: `<a href="https://o.test/login?next=/a?t=` + tok + `&amp;x=1">L</a>`,
			md:   "[L](https://o.test/login?x=1)\n", txt: "L (https://o.test/login?x=1)\n",
		},
		{
			name: "percent-encoded token", origin: "https://s.wardlock.test", path: "index.html",
			page: `<a href="https://o.test/l?next=%2Fa%3Ft%3D` + strings.ReplaceAll(tok, "_", "%5F") + `">L</a> <a href="https://o.test/l?n=%252Fa%253Ft%253D` + tok + `">D</a>`,
			md:   "[L](https://o.test/l) [D](https://o.test/l)\n", txt: "L (https://o.test/l) D (https://o.test/l)\n",
		},
		{
			name: "token as a parameter name, in the path and in mailto", origin: "https://s.wardlock.test", path: "index.html",
			page: `<a href="https://o.test/x?` + tok + `=1&amp;k=v">N</a> <a href="/s/` + tok + `/x">P</a> <a href="mailto:a@example.test?subject=` + tok + `">M</a>`,
			md:   "[N](https://o.test/x?k=v) [P](https://s.wardlock.test/s/wl_abcdefghij_redacted/x) [M](mailto:a@example.test)\n",
			txt:  "N (https://o.test/x?k=v) P (https://s.wardlock.test/s/wl_abcdefghij_redacted/x) M (mailto:a@example.test)\n",
		},
		{
			name: "link text that is its tokenised URL", origin: "https://s.wardlock.test", path: "index.html",
			page: `<a href="https://s.wardlock.test/a?t=` + tok + `">https://s.wardlock.test/a?t=` + tok + `</a> <a href="/b?t=` + tok + `">/b?t=` + tok + `</a>`,
			md:   "<https://s.wardlock.test/a> <https://s.wardlock.test/b>\n", txt: "https://s.wardlock.test/a https://s.wardlock.test/b\n",
		},
		{
			name: "tokens in text, alt text, code and the title", origin: "https://s.wardlock.test", path: "index.html",
			page: `<title>T ` + tok + `</title><p>Use ` + tok + ` <img src="/i.png" alt="` + tok + `"> <code>` + tok + `</code></p><pre>curl -H 'Authorization: Bearer ` + strings.ToUpper(tok[:3]) + tok[3:] + `'</pre>`,
			md:   "# T wl\\_abcdefghij\\_redacted\n\nUse wl\\_abcdefghij\\_redacted ![wl\\_abcdefghij\\_redacted](https://s.wardlock.test/i.png) `wl_abcdefghij_redacted`\n\n```\ncurl -H 'Authorization: Bearer wl_abcdefghij_redacted'\n```\n",
			txt:  "T wl_abcdefghij_redacted\n\nUse wl_abcdefghij_redacted [wl_abcdefghij_redacted] wl_abcdefghij_redacted\n\ncurl -H 'Authorization: Bearer wl_abcdefghij_redacted'\n",
		},
		{
			name: "token split by a comment", path: "index.html",
			page: `<p>` + tok[:20] + `<!-- x -->` + tok[20:] + `</p>`,
			md:   "wl\\_abcdefghij\\_redacted\n", txt: "wl_abcdefghij_redacted\n",
		},
		{
			name: "more Markdown escapes", path: "index.html",
			page: `<p>~5 &amp;copy; &amp;#169; &amp;x</p><p>| a | b</p>`,
			md:   "\\~5 \\&copy; \\&#169; &x\n\n\\| a | b\n", txt: "~5 &copy; &#169; &x\n\n| a | b\n",
		},
		{
			name: "invalid UTF-8", path: "index.html",
			page: "<p>a\xff\xfeb</p>",
			md:   "a\uFFFDb\n", txt: "a\uFFFDb\n",
		},
		{
			name: "table without header cells", path: "index.html",
			page: `<table><tr><td>a</td></tr><tr><td>b</td><td>c</td></tr></table>`,
			md:   "| a | |\n| --- | --- |\n| b | c |\n", txt: "a\nb\tc\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u, err := deploys.PageURL(tc.origin, tc.path)
			if err != nil {
				t.Fatal(err)
			}
			md, txt := deploys.Render([]byte(tc.page), u)
			if string(md) != tc.md {
				t.Errorf("markdown = %q, want %q", md, tc.md)
			}
			if string(txt) != tc.txt {
				t.Errorf("text = %q, want %q", txt, tc.txt)
			}
		})
	}
}

// TestRenderDeepNesting keeps content nested beyond the recursion bound,
// and beyond what the parser accepts, as text.
func TestRenderDeepNesting(t *testing.T) {
	for _, depth := range []int{200, 5000} {
		page := `<meta charset="utf-8"><p>top <input name="q"></p>` + strings.Repeat("<div><span>", depth) + "deep <b>text</b><script>x()</script>" + strings.Repeat("</span></div>", depth)
		u, _ := deploys.PageURL("", "index.html")
		md, txt := deploys.Render([]byte(page), u)
		for _, out := range []string{string(md), string(txt)} {
			if !strings.Contains(out, "top") || !strings.Contains(out, "deep text") || strings.Contains(out, "x()") {
				t.Errorf("depth %d: %q", depth, out)
			}
		}
	}
}

// deepInline nests each of b, strong, em and a depth times around text.
func deepInline(depth int) string {
	var open, closing strings.Builder
	for i := range depth {
		tag := []string{"b", "strong", "em", "a"}[i%4]
		if tag == "a" {
			open.WriteString(`<a href="/x?t=` + tok + `">`)
		} else {
			open.WriteString("<" + tag + ">")
		}
		closing.WriteString("</" + tag + ">")
	}
	return "<p>" + open.String() + "deep<br>text<br>" + reverse(closing.String()) + "</p>"
}

// reverse closes tags innermost first.
func reverse(closing string) string {
	parts := strings.SplitAfter(closing, ">")
	slices.Reverse(parts)
	return strings.Join(parts, "")
}

// TestRenderDeepInline: nesting inline elements costs linear time, to the
// recursion bound and beyond it.
func TestRenderDeepInline(t *testing.T) {
	u, _ := deploys.PageURL("https://s.wardlock.test", "index.html")
	for _, page := range []string{
		strings.Repeat("<b>", 100) + "x" + strings.Repeat("</b>", 100),
		strings.Repeat("<em>", 500) + "x<br>" + strings.Repeat("</em>", 500),
		deepInline(128), deepInline(129), deepInline(400),
	} {
		done := make(chan [2][]byte, 1)
		go func() {
			md, txt := deploys.Render([]byte(page), u)
			done <- [2][]byte{md, txt}
		}()
		select {
		case out := <-done:
			for _, r := range out {
				if !bytes.Contains(r, []byte("deep")) && !bytes.Contains(r, []byte("x")) {
					t.Errorf("content lost: %q", r)
				}
				if bytes.Contains(r, []byte("fakeSecret")) {
					t.Errorf("token leaked: %q", r)
				}
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("rendering %d bytes of nested inline elements took over 2s", len(page))
		}
	}
}

// tokenShape matches a token as either rendition prints it; group 1 is the
// secret.
var tokenShape = regexp.MustCompile(`(?i)wl\\?_[a-z2-7]{10}\\?_((?:[A-Za-z0-9-]|\\?_)+)`)

// FuzzRender checks that any input converts without panicking, that both
// renditions are empty or end in exactly one newline, and that every
// token in them is redacted.
func FuzzRender(f *testing.F) {
	f.Add([]byte(`<h1>x</h1><ul><li>a<ol><li>b</li></ol></li></ul><pre>` + "```" + `</pre><table><tr><td>|</td></tr></table>`))
	f.Add([]byte(`<a href="?t=wl_a_b"><b> </b></a><br><br><code>` + "`" + `</code>`))
	f.Add([]byte(strings.Repeat("<b>", 40) + "x<br>" + strings.Repeat("</b>", 40)))
	f.Add([]byte(deepInline(130)))
	f.Add([]byte(`<a href="/l?next=%2F%3Ft%3D` + tok + `">/l?next=%2F%3Ft%3D` + tok + `</a>`))
	u, _ := deploys.PageURL("https://s.wardlock.test", "index.html")
	f.Fuzz(func(t *testing.T, page []byte) {
		md, txt := deploys.Render(page, u)
		for _, out := range [][]byte{md, txt} {
			for _, m := range tokenShape.FindAllSubmatch(out, -1) {
				if string(bytes.ReplaceAll(m[1], []byte(`\_`), []byte("_"))) != "redacted" {
					t.Fatalf("token %q leaked: %q", m[0], out)
				}
			}
			if len(out) > 0 && (!bytes.HasSuffix(out, []byte("\n")) || bytes.HasSuffix(out, []byte("\n\n"))) {
				t.Fatalf("rendition %q does not end in one newline", out)
			}
		}
	})
}
