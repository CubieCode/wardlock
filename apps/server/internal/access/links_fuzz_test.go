// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package access

import (
	"testing"
	"time"
)

func FuzzParseLinkToken(f *testing.F) {
	token, _ := newLinkToken()
	f.Add(token)
	f.Add("")
	f.Add("wl_")
	f.Add("wl_aaaaaaaaaa_" + "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAB")
	f.Add("WL_AAAAAAAAAA_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	f.Fuzz(func(t *testing.T, raw string) {
		publicID, ok := parseLinkToken(raw)
		if !ok {
			return
		}
		// Anything accepted has exactly the documented shape.
		if len(raw) != linkTokenLen || raw[:3] != "wl_" || raw[3:13] != publicID || !validPublicID(publicID) || raw[13] != '_' {
			t.Fatalf("accepted %q as %q", raw, publicID)
		}
	})
}

func FuzzDecodeTicket(f *testing.F) {
	k := keyed{secret: []byte("fuzz-server-key-0123456789abcdef-012345")}
	f.Add(k.encodeTicket(viewTicket{
		SiteID: "01928c5e-7a1b-7c3d-9e4f-5a6b7c8d9e01", LinkID: "01928c5e-7a1b-7c3d-9e4f-5a6b7c8d9e02",
		Path: "/docs/", Return: "/docs/?x=1", ExpiresAt: time.UnixMilli(1767225600000).UTC(),
	}, "hash"))
	f.Add("")
	f.Add(".")
	f.Add("e30.AAAA")
	f.Fuzz(func(t *testing.T, value string) {
		tk, payload, mac, ok := decodeTicket(value)
		if !ok {
			return
		}
		if tk.SiteID == "" || tk.LinkID == "" || len(payload) == 0 || len(value) > maxTicketBytes {
			t.Fatalf("decoded %q as %+v", value, tk)
		}
		_ = mac
	})
}
