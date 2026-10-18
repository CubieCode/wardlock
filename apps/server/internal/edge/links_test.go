// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package edge_test

import (
	"net/http"
	"strings"
	"testing"

	"wardlock.nz/server/internal/access"
)

// TestIssueRefusedWhenTheLinkChanged: when the link is rotated, revoked or
// expires between the decision and the session's issue, the request is
// refused, no session cookie is set and the access is recorded as refused.
func TestIssueRefusedWhenTheLinkChanged(t *testing.T) {
	const token = "wl_abcdefgh23_dGVzdC1vbmx5LW5vdC1yZWFsLXRva2VuLXNlY3JldHg"
	a := &fakeAccess{issueErr: access.ErrLinkChanged, fn: func(access.Request) (access.Decision, error) {
		d := allow(true)
		d.LinkPublicID, d.TokenAccepted = "abcdefgh23", true
		d.Issue = &access.NewSession{SiteID: "site-basic", Method: "link", ShareLinkID: "link-1", LinkTokenHash: "hash"}
		return d, nil
	}}
	h, rec := newRecordingHarness(t, a)
	for _, tc := range []struct {
		name   string
		header http.Header
		status int
		body   string
	}{
		{"browser", browser, http.StatusUnauthorized, "This page is private"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := h.do(http.MethodGet, "basic."+base, "/guide?t="+token, tc.header)
			if res.Code != tc.status || !strings.Contains(res.Body.String(), tc.body) || strings.Contains(res.Body.String(), "Guide") {
				t.Fatalf("%d %q", res.Code, res.Body.String())
			}
			for _, c := range res.Result().Cookies() {
				if c.Name == access.SessionCookieName && c.MaxAge > 0 {
					t.Fatalf("set a session cookie %v", c)
				}
			}
			got := rec.take()
			if len(got) != 1 || got[0].Status != tc.status || got[0].Via != access.ViaNone || got[0].LinkPublicID != "" {
				t.Fatalf("access records %+v", got)
			}
		})
	}
}
