// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package edge

import "testing"

func TestHashedAsset(t *testing.T) {
	for name, want := range map[string]bool{
		// Vite, Rollup and esbuild: name-hash.ext.
		"assets/index-BdK3j2Lp.js":                     true,
		"assets/index-BdK3j2Lp.css":                    true,
		"assets/_plugin-vue_export-helper-DlAUqK2U.js": true,
		"assets/inter-latin-400-normal-Cz_jRjDp.woff2": true,
		// webpack and Parcel: name.hash.ext.
		"assets/main.3f2a9c1b.js":          true,
		"assets/main.3f2a9c1b.chunk.js":    false, // the hash is not right before the extension
		"assets/fonts/inter.5d1c2e8f.ttf":  true,  // nested directories count
		"assets/logo.3f2a9c1b4d5e6f70.svg": true,
		// The documented false positive: a long word looks like a hash.
		"assets/my-component.js": true,
		// Not hashed.
		"assets/app.js":              false,
		"assets/index-BdK3j2L.js":    false, // 7 characters
		"assets/index-BdK3-2Lp.js":   false, // the segment after the last '-' is short
		"assets/index~BdK3j2Lp.js":   false, // other separators
		"assets/index-BdK3j2L$.js":   false,
		"assets/index-BdK3j2Lp":      false, // no extension
		"assets/index-BdK3j2Lp.":     false,
		"assets/-BdK3j2Lp.js":        false, // nothing before the separator
		"assets/.BdK3j2Lp":           false,
		"assets/BdK3j2LpXy.js":       false, // no separator
		"assets/main.3f2a9c1b.js.br": false, // a sibling by its own name
		// Outside assets/.
		"index-BdK3j2Lp.js":            false,
		"static/index-BdK3j2Lp.js":     false,
		"Assets/index-BdK3j2Lp.js":     false, // case-sensitive, like paths
		"docs/assets/main.3f2a9c1b.js": false,
	} {
		if got := hashedAsset(name); got != want {
			t.Errorf("hashedAsset(%q) = %v, want %v", name, got, want)
		}
	}
}
