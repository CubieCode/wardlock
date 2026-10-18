// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package deploys_test

import (
	"bytes"
	"flag"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
			page: `<a href="https://user:pw@o.test/#t=wl_a_b">F</a>`,
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

// FuzzRender checks that any input converts without panicking and that
// both renditions are empty or end in exactly one newline.
func FuzzRender(f *testing.F) {
	f.Add([]byte(`<h1>x</h1><ul><li>a<ol><li>b</li></ol></li></ul><pre>` + "```" + `</pre><table><tr><td>|</td></tr></table>`))
	f.Add([]byte(`<a href="?t=wl_a_b"><b> </b></a><br><br><code>` + "`" + `</code>`))
	u, _ := deploys.PageURL("https://s.wardlock.test", "index.html")
	f.Fuzz(func(t *testing.T, page []byte) {
		md, txt := deploys.Render(page, u)
		for _, out := range [][]byte{md, txt} {
			if len(out) > 0 && (!bytes.HasSuffix(out, []byte("\n")) || bytes.HasSuffix(out, []byte("\n\n"))) {
				t.Fatalf("rendition %q does not end in one newline", out)
			}
		}
	})
}
