// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"wardlock.nz/server/internal/access"
	"wardlock.nz/server/internal/store"
)

// FuzzCredentialsThroughTheEdge sends arbitrary share-link tokens (in the
// query, a rendition's query and Authorization), site session and OAuth
// binding cookies, site callback codes and passwords to a protected site
// through the real edge, access service and store. Whatever arrives, the
// edge never fails, never serves protected content or issues a session
// without one of the site's own credentials (another site's do not
// count), and never writes a token-shaped credential it was sent into its
// logs, access records or response body.
//
// Normal test runs execute only the seeds; run it with
// go test -run='^$' -fuzz='^FuzzCredentialsThroughTheEdge$' ./cmd/wardlockd/
func FuzzCredentialsThroughTheEdge(f *testing.F) {
	p := newPolicyEdge(f, func(c *access.Config) {
		// Every execution is a fresh guess: limits that tripped would
		// only answer 429 from then on and hide the paths behind them.
		c.Limits = access.DefaultLimits()
		c.Limits.TokenFailuresPerIP.N, c.Limits.TokenFailuresPerSite.N = math.MaxInt, math.MaxInt
		c.Limits.PasswordPerIP.N, c.Limits.PasswordPerSite.N = math.MaxInt, math.MaxInt
	})
	p.publish("docs")
	p.publish("other")
	p.setPassword("docs", "/", sitePassword, true)
	p.set("other", "/", store.Protected, true)
	_, token := p.link("docs", access.NewLink{})
	_, otherToken := p.link("other", access.NewLink{})
	docsCookie := p.visit(visit{target: withT("/", token), header: html}).sessionCookie()
	otherCookie := p.visit(visit{host: otherHost, target: withT("/", otherToken), header: html}).sessionCookie()
	if docsCookie == "" || otherCookie == "" {
		f.Fatal("no sessions to seed with")
	}
	p.logs.Reset()
	p.accessRecords()

	for _, s := range []struct {
		shape                 uint8
		cred, cookie, binding string
	}{
		{0, token, "", ""},
		{0, otherToken, otherCookie, ""},
		{0, strings.Replace(token, token[len(token)-4:], "AAAA", 1), "", ""},
		{1, "Bearer " + token, "", ""},
		{1, "bearer  " + otherToken + " ", "", ""},
		{2, "format=md&t=" + url.QueryEscape(token), "", ""},
		{2, "t=" + token + "&t=junk&format=txt", "", ""},
		{3, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"},
		{4, sitePassword, "", ""},
		{4, otherPassword, "", ""},
		{5, "", docsCookie, ""},
		{5, "", otherCookie, ""},
		{5, "", docsCookie[:len(docsCookie)-2] + "AA", ""},
		{5, "", "djE.AAAA", ""},
	} {
		f.Add(s.shape, s.cred, s.cookie, s.binding)
	}

	f.Fuzz(func(t *testing.T, shape uint8, cred, cookie, binding string) {
		var req *http.Request
		switch shape % 6 {
		case 0: // ?t=
			req = httptest.NewRequest(http.MethodGet, "/", nil)
			req.URL.RawQuery = "t=" + cred
		case 1: // Authorization
			req = httptest.NewRequest(http.MethodGet, "/internal/", nil)
			req.Header.Set("Authorization", cred)
		case 2: // a rendition's query, as sent
			req = httptest.NewRequest(http.MethodGet, "/", nil)
			req.URL.RawQuery = cred
		case 3: // the site callback
			req = httptest.NewRequest(http.MethodGet, "/.wardlock/callback", nil)
			req.URL.RawQuery = url.Values{"code": {cred}}.Encode()
		case 4: // the password form, same-origin
			form := url.Values{"method": {"password"}, "password": {cred}, "next": {"/"}}
			req = httptest.NewRequest(http.MethodPost, "/.wardlock/login", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Origin", "null")
			req.Header.Set("Sec-Fetch-Site", "same-origin")
		default: // cookies alone
			req = httptest.NewRequest(http.MethodGet, "/internal/", nil)
		}
		req.Host = host
		req.RemoteAddr = "192.0.2.10:4711"
		req.Header.Set("Accept", "text/html")
		var cookies []string
		if cookie != "" {
			cookies = append(cookies, access.SessionCookieName+"="+cookie)
		}
		if binding != "" {
			cookies = append(cookies, access.OAuthBindingCookieName+"="+binding)
		}
		if len(cookies) > 0 {
			req.Header.Set("Cookie", strings.Join(cookies, "; "))
		}
		rec := httptest.NewRecorder()
		p.h.ServeHTTP(rec, req)
		r := result{rec.Code, rec.Body.String(), rec.Result().Header}
		logs := p.logs.String()
		records := p.accessRecords()
		p.logs.Reset()

		if r.code >= 500 {
			t.Fatalf("%d for shape %d: %q", r.code, shape%6, r.body)
		}
		// What could legitimately grant access: the site's own token
		// anywhere it was sent (decoded or not), its own cookie, its
		// password.
		sent := cred + "\x00" + cookie
		if q, err := url.ParseQuery(cred); err == nil || len(q) > 0 {
			for _, vs := range q {
				sent += "\x00" + strings.Join(vs, "\x00")
			}
		}
		for part := range strings.SplitSeq(cred, "&") {
			if _, v, ok := strings.Cut(part, "="); ok {
				if dv, err := url.QueryUnescape(v); err == nil {
					sent += "\x00" + dv
				}
			}
		}
		granted := strings.Contains(sent, token) || strings.Contains(cookie, docsCookie) || cred == sitePassword
		content := strings.Contains(r.body, "Home page") || strings.Contains(r.body, "Internal plans")
		if (content || r.sessionFrom() != "") && !granted {
			t.Fatalf("shape %d granted access without a credential: %d %v %q", shape%6, r.code, r.h, r.body)
		}
		if shape%6 == 3 && r.sessionFrom() != "" {
			t.Fatalf("a site callback with no flow issued a session: %v", r.h)
		}
		// A token-shaped credential, sent as the token itself, is never
		// repeated back or recorded.
		if strings.HasPrefix(cred, "wl_") && len(cred) >= 14 && !strings.ContainsAny(cred, "&;%+= \t") {
			if strings.Contains(logs, cred) || strings.Contains(r.body, cred) {
				t.Fatalf("credential %q reached the logs or body:\n%s\n%s", cred, logs, r.body)
			}
			for _, rec := range records {
				if strings.Contains(fmt.Sprintf("%+v", rec), cred) {
					t.Fatalf("credential %q reached the access log: %+v", cred, rec)
				}
			}
		}
		for _, secret := range []string{token, otherToken, docsCookie, otherCookie, sitePassword} {
			if strings.Contains(logs, secret) {
				t.Fatalf("a real credential reached the logs:\n%s", logs)
			}
		}
	})
}
