// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"wardlock.nz/server/internal/api"
	"wardlock.nz/server/internal/deploys"
	"wardlock.nz/server/internal/store"
)

// TestAnalyticsCountries checks the by-country breakdown (V5.13): busiest
// first with unknown among them, scoped like the rest, never another
// owner's, and left out with the lookup's credit while the lookup is off.
func TestAnalyticsCountries(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	a, b := hs.ownerA, hs.ownerB
	docs := hs.createSite(a, "docs")
	blog := hs.createSite(a, "blog")
	theirs := hs.createSite(b, "theirs")
	now := time.Now().UTC().Add(-time.Hour)
	var entries []store.AccessEntry
	for i, c := range []struct{ site, country string }{
		{docs.Id, "NZ"}, {docs.Id, "NZ"}, {blog.Id, "NZ"}, {docs.Id, "AU"},
		{blog.Id, store.CountryUnknown}, {docs.Id, ""}, {theirs.Id, "FR"},
	} {
		e := logEntry(now, time.Duration(i)*time.Second, c.site, store.ViaPublic, "", "/", http.StatusOK)
		e.Country = c.country
		entries = append(entries, e)
	}
	entries[3].Status = http.StatusUnauthorized
	entries[3].Via = store.ViaNone
	hs.logTraffic(entries...)

	// Off: no countries and nothing about the lookup.
	res := hs.do(a, http.MethodGet, "/analytics", nil)
	res.want(t, http.StatusOK, "")
	if body := string(res.body); strings.Contains(body, "countr") {
		t.Fatalf("lookup off, body mentions countries: %s", body)
	}

	hs.srv.Countries = &api.CountryLookupConfig{Attribution: "IP Geolocation by DB-IP (https://db-ip.com)"}
	got := hs.analytics(a, "")
	want := []api.AnalyticsCountry{
		{Country: "NZ", Allowed: 3},
		{Country: "AU", Refused: 1},
		{Country: "unknown", Allowed: 1},
	}
	if got.Countries == nil || fmt.Sprint(*got.Countries) != fmt.Sprint(want) {
		t.Fatalf("Countries = %v, want %v", got.Countries, want)
	}
	if got.CountryLookup == nil || got.CountryLookup.Attribution != "IP Geolocation by DB-IP (https://db-ip.com)" || got.CountryLookup.ServerCountry != nil {
		t.Fatalf("CountryLookup = %+v", got.CountryLookup)
	}
	// One site's scope.
	if site := hs.analytics(a, "?site=blog"); site.Countries == nil || fmt.Sprint(*site.Countries) != fmt.Sprint([]api.AnalyticsCountry{{Country: "NZ", Allowed: 1}, {Country: "unknown", Allowed: 1}}) {
		t.Fatalf("blog Countries = %v", site.Countries)
	}
	// The other owner sees only their own country.
	if other := hs.analytics(b, ""); other.Countries == nil || fmt.Sprint(*other.Countries) != fmt.Sprint([]api.AnalyticsCountry{{Country: "FR", Allowed: 1}}) {
		t.Fatalf("owner B's Countries = %v", other.Countries)
	}

	// The server's country, when the operator names one; an empty list,
	// not null, when nothing has a country.
	hs.srv.Countries.ServerCountry = "NZ"
	hs.do(b, http.MethodDelete, "/sites/theirs", nil).want(t, http.StatusNoContent, "")
	res = hs.do(b, http.MethodGet, "/analytics", nil)
	res.want(t, http.StatusOK, "")
	for _, s := range []string{`"countries":[]`, `"countryLookup":{"attribution":"IP Geolocation by DB-IP (https://db-ip.com)","serverCountry":"NZ"}`} {
		if !strings.Contains(string(res.body), s) {
			t.Fatalf("body %s, want %s", res.body, s)
		}
	}
}

// TestAccessLogCountry checks site and link access logs list an entry's
// country, unknown included, while the lookup is on, and leave every
// country out while it is off, even for entries recorded with one.
func TestAccessLogCountry(t *testing.T) {
	hs := newHarness(t, deploys.DefaultLimits())
	docs := hs.createSite(hs.ownerA, "docs")
	link := hs.createLink(hs.ownerA, "docs", map[string]any{}).ShareLink.PublicId
	base := time.Now().Add(-time.Hour).UTC()
	var entries []store.AccessEntry
	for i, c := range []string{"NZ", store.CountryUnknown, ""} {
		e := logEntry(base, time.Duration(i)*time.Second, docs.Id, store.ViaLink, link, "/", http.StatusOK)
		e.Country = c
		entries = append(entries, e)
	}
	if err := hs.db.AccessLog().Append(t.Context(), entries); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		on     bool
		want   string
		fields int
	}{
		// Newest first.
		{"on", true, "[- unknown NZ]", 2},
		{"off", false, "[- - -]", 0},
	} {
		if tc.on {
			hs.srv.Countries = &api.CountryLookupConfig{Attribution: "IP Geolocation by DB-IP (https://db-ip.com)"}
		} else {
			hs.srv.Countries = nil
		}
		for _, path := range []string{"/sites/docs/access-log", "/sites/docs/links/" + link + "/access-log"} {
			res := hs.do(hs.ownerA, http.MethodGet, path, nil)
			res.want(t, http.StatusOK, "")
			var page api.AccessLogPage
			res.decode(t, &page)
			var got []string
			for _, e := range page.Items {
				if e.Country == nil {
					got = append(got, "-")
				} else {
					got = append(got, *e.Country)
				}
			}
			if fmt.Sprint(got) != tc.want {
				t.Fatalf("%s %s: countries = %v, want %s", tc.name, path, got, tc.want)
			}
			if n := strings.Count(string(res.body), `"country"`); n != tc.fields {
				t.Fatalf("%s %s: body has %d country fields, want %d: %s", tc.name, path, n, tc.fields, res.body)
			}
		}
	}
}
