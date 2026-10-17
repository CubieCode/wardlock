// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package access

import (
	"testing"
	"time"
)

func FuzzDecodeCookie(f *testing.F) {
	k := keyed{secret: []byte("fuzz-server-key-0123456789abcdef-012345")}
	f.Add(k.encode(sessionClaims{
		SiteID: "01928c5e-7a1b-7c3d-9e4f-5a6b7c8d9e01", PolicyID: "01928c5e-7a1b-7c3d-9e4f-5a6b7c8d9e02",
		PolicyVersion: 3, ExpiresAt: time.UnixMilli(1767225600000).UTC(), Token: newToken(),
	}))
	f.Add("")
	f.Add(".")
	f.Add("djE.AAAA")
	f.Fuzz(func(t *testing.T, value string) {
		c, ok := k.decode(value)
		if !ok {
			return
		}
		// Anything accepted is exactly what this key signed.
		if k.encode(c) != value {
			t.Fatalf("decode accepted %q, which re-encodes as %q", value, k.encode(c))
		}
	})
}
