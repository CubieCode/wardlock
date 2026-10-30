// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package edge

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
)

func TestStripCSPSource(t *testing.T) {
	// Browsers hash exactly the text between <script> and </script>.
	body, ok := strings.CutPrefix(stripScript, "<script>")
	body, ok2 := strings.CutSuffix(body, "</script>")
	sum := sha256.Sum256([]byte(body))
	if !ok || !ok2 || stripCSPSource != "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'" {
		t.Fatalf("stripCSPSource = %s does not hash the strip script's body", stripCSPSource)
	}
}

func TestAllowStripScript(t *testing.T) {
	h := stripCSPSource
	for _, tc := range []struct{ name, in, want string }{
		{"no script directive", "img-src 'self'; frame-ancestors 'none'", "img-src 'self'; frame-ancestors 'none'"},
		{"default-src", "default-src 'self'", "default-src 'self' " + h},
		{"script-src", "default-src 'self'; script-src 'self' https://cdn.example.test", "default-src 'self'; script-src 'self' https://cdn.example.test " + h},
		{"script-src-elem wins", "default-src 'none'; script-src 'self'; script-src-elem 'self'", "default-src 'none'; script-src 'self'; script-src-elem 'self' " + h},
		{"lone 'none'", "default-src 'none'", "default-src " + h},
		{"lone 'NONE' with trailing space", "script-src 'NONE' ; img-src *", "script-src " + h + " ; img-src *"},
		{"'unsafe-inline' kept", "script-src 'self' 'unsafe-inline'", "script-src 'self' 'unsafe-inline'"},
		{"'unsafe-inline' with a nonce", "script-src 'unsafe-inline' 'nonce-abc'", "script-src 'unsafe-inline' 'nonce-abc' " + h},
		{"'unsafe-inline' with a hash", "script-src 'unsafe-inline' 'sha384-abc'", "script-src 'unsafe-inline' 'sha384-abc' " + h},
		{"'strict-dynamic'", "script-src 'strict-dynamic' 'nonce-abc' 'unsafe-inline' https:", "script-src 'strict-dynamic' 'nonce-abc' 'unsafe-inline' https: " + h},
		{"directive names in any case", "Script-Src 'self'", "Script-Src 'self' " + h},
		{"the first of a repeated directive", "script-src 'self'; script-src 'none'", "script-src 'self' " + h + "; script-src 'none'"},
		{"several directives untouched", "default-src 'self'; style-src 'unsafe-inline'; report-uri /csp", "default-src 'self' " + h + "; style-src 'unsafe-inline'; report-uri /csp"},
		{"comma-joined policies", "default-src 'self', script-src 'none'; img-src *, img-src 'self'",
			"default-src 'self' " + h + ", script-src " + h + "; img-src *, img-src 'self'"},
		{"trailing semicolon", "default-src 'self';", "default-src 'self' " + h + ";"},
	} {
		if got := allowStripScript(tc.in); got != tc.want {
			t.Errorf("%s: allowStripScript(%q)\n = %q\nwant %q", tc.name, tc.in, got, tc.want)
		}
	}
}
