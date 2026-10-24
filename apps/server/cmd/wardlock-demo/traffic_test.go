// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"net/netip"
	"testing"
	"time"

	"wardlock.nz/server/internal/store"
)

var testNow = time.Date(2026, 10, 25, 3, 30, 0, 0, time.UTC)

// planTraffic is the plan's sites as seedHistory hands them to generate,
// with made-up IDs and links.
func planTraffic(now time.Time) []siteTraffic {
	var out []siteTraffic
	for i, p := range sites {
		t := siteTraffic{
			id: p.name, name: p.name, host: p.name + ".demo.test",
			created: createdAt(now, p.ageDays, i), plan: p.traffic, paths: fixturePaths[p.fixture],
		}
		t.protectedPrefix, t.ways = ways(p.policies)
		for j, l := range p.links {
			t.links = append(t.links, linkVisit{
				publicID: string(rune('a'+i)) + string(rune('a'+j)) + "23456789",
				created:  now.Add(-days(l.ageDays)),
			})
		}
		out = append(out, t)
	}
	return out
}

func generatePlan(seed uint64) []store.AccessEntry {
	return generate(planTraffic(testNow), testNow.Add(-days(90)), testNow, seed)
}

func TestGenerateIsDeterministic(t *testing.T) {
	a, b := generatePlan(7), generatePlan(7)
	if len(a) != len(b) {
		t.Fatalf("same seed: %d and %d entries", len(a), len(b))
	}
	for i := range a {
		x, y := a[i], b[i]
		x.ID, y.ID = "", ""
		if x != y {
			t.Fatalf("entry %d differs:\n%+v\n%+v", i, x, y)
		}
	}
	if c := generatePlan(8); len(c) == len(a) && c[0].OccurredAt.Equal(a[0].OccurredAt) {
		t.Error("a different seed gave the same traffic")
	}
}

func TestGenerateShape(t *testing.T) {
	entries := generatePlan(1)
	if n := len(entries); n < 65_000 || n > 95_000 {
		t.Errorf("%d entries, want about 80k", n)
	}
	created := map[string]time.Time{}
	for _, s := range planTraffic(testNow) {
		created[s.id] = s.created
	}
	perSite := map[string][]int{} // requests per day ago
	weekday, weekend := map[string]int{}, map[string]int{}
	var nzDay, nzNight int
	countries, statuses := map[string]int{}, map[int]int{}
	for i, e := range entries {
		if i > 0 && e.OccurredAt.Before(entries[i-1].OccurredAt) {
			t.Fatal("entries are not oldest first")
		}
		if e.OccurredAt.Before(created[e.SiteID]) || !e.OccurredAt.Before(testNow) {
			t.Fatalf("entry at %v outside %s's life", e.OccurredAt, e.SiteID)
		}
		if (e.Via == store.ViaLink) != (e.LinkPublicID != "") {
			t.Fatalf("via %s with link %q", e.Via, e.LinkPublicID)
		}
		if e.Browser == "" || e.Country == "" {
			t.Fatalf("entry without browser or country: %+v", e)
		}
		ago := int(testNow.Sub(e.OccurredAt).Hours() / 24)
		if perSite[e.SiteID] == nil {
			perSite[e.SiteID] = make([]int, 91)
		}
		perSite[e.SiteID][ago]++
		local := e.OccurredAt.In(nz)
		if wd := local.Weekday(); wd == time.Saturday || wd == time.Sunday {
			weekend[e.SiteID]++
		} else {
			weekday[e.SiteID]++
		}
		switch h := local.Hour(); {
		case h >= 9 && h < 17:
			nzDay++
		case h < 6:
			nzNight++
		}
		countries[e.Country]++
		statuses[e.Status]++
	}
	sum := func(site string, from, to int) (n int) {
		for _, c := range perSite[site][from:to] {
			n += c
		}
		return n
	}
	// The launch spike: launch week busier than the week before it and
	// than the last week.
	if launch, before, last := sum("spring-launch", 18, 25), sum("spring-launch", 25, 30), sum("spring-launch", 0, 7); launch < 3*before || launch < 2*last {
		t.Errorf("spring-launch: launch week %d, before %d, last week %d", launch, before, last)
	}
	if early, late := sum("newsletter-archive", 60, 75), sum("newsletter-archive", 0, 15); late < 2*early {
		t.Errorf("newsletter-archive is not rising: %d then %d", early, late)
	}
	if early, late := sum("winter-sale", 70, 85), sum("winter-sale", 0, 15); early < 4*late {
		t.Errorf("winter-sale is not fading: %d then %d", early, late)
	}
	if n := sum("harbour-wayfinding", 0, 40); n != 0 {
		t.Errorf("harbour-wayfinding has %d requests after it died", n)
	}
	// Work sites are quiet at weekends (5 weekdays to 2 weekend days).
	if wd, we := weekday["team-handbook"], weekend["team-handbook"]; we*5 > wd {
		t.Errorf("team-handbook: %d weekday and %d weekend requests", wd, we)
	}
	if nzDay < 3*nzNight {
		t.Errorf("%d requests in the NZ working day, %d at night", nzDay, nzNight)
	}
	for _, c := range []string{"NZ", "AU", "US", "GB", "IS", "BR", store.CountryUnknown} {
		if countries[c] == 0 {
			t.Errorf("no requests from %s", c)
		}
	}
	if countries["NZ"] < countries["AU"] {
		t.Errorf("NZ %d, AU %d: NZ should lead", countries["NZ"], countries["AU"])
	}
	for _, s := range []int{200, 401, 403, 404, 429} {
		if statuses[s] == 0 {
			t.Errorf("no %d responses", s)
		}
	}
}

func TestGenerateViaMatchesPolicy(t *testing.T) {
	plans := map[string]siteTraffic{}
	for _, s := range planTraffic(testNow) {
		plans[s.id] = s
	}
	links := map[string]bool{}
	for _, e := range generatePlan(1) {
		s := plans[e.SiteID]
		protected := s.protectedPrefix != "" && len(e.Path) >= len(s.protectedPrefix) && e.Path[:len(s.protectedPrefix)] == s.protectedPrefix
		switch {
		case !protected && e.Via != store.ViaPublic:
			t.Fatalf("%s %s: via %s on a public path", e.SiteID, e.Path, e.Via)
		case protected && e.Via == store.ViaPublic:
			t.Fatalf("%s %s: public on a protected path", e.SiteID, e.Path)
		case e.Via == store.ViaNone && e.Status < 400:
			t.Fatalf("%s: refused with %d", e.SiteID, e.Status)
		}
		if e.LinkPublicID != "" {
			found := false
			for _, l := range s.links {
				if l.publicID == e.LinkPublicID {
					found = !e.OccurredAt.Before(l.created)
				}
			}
			if !found {
				t.Fatalf("%s: visit with link %s before it existed or of another site", e.SiteID, e.LinkPublicID)
			}
			links[e.LinkPublicID] = true
		}
	}
	if len(links) < 5 {
		t.Errorf("visits name only %d links", len(links))
	}
}

func TestDocumentationIPs(t *testing.T) {
	ranges := []netip.Prefix{netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("2001:db8::/32")}
	var v4, v6 int
	for _, e := range generatePlan(3) {
		addr, err := netip.ParseAddr(e.IP)
		if err != nil {
			t.Fatalf("IP %q: %v", e.IP, err)
		}
		if !ranges[0].Contains(addr) && !ranges[1].Contains(addr) {
			t.Fatalf("IP %s is outside the documentation ranges", addr)
		}
		// Stored truncated, as the access log keeps them.
		if addr.Is4() {
			v4++
			if addr.As4()[3] != 0 {
				t.Fatalf("IPv4 %s is not truncated to /24", addr)
			}
		} else {
			v6++
		}
	}
	if v4 == 0 || v6 == 0 {
		t.Errorf("%d IPv4 and %d IPv6 addresses, want both", v4, v6)
	}
}

func TestBackdatedLink(t *testing.T) {
	created := testNow.Add(-time.Minute)
	tests := []struct {
		name    string
		expires time.Time
		want    time.Time
	}{
		{"never expires", time.Time{}, testNow.Add(-days(20))},
		{"expires in 30 days", testNow.Add(30 * 24 * time.Hour), testNow.Add(-days(20))},
		{"expires in 30 minutes", testNow.Add(30 * time.Minute), created},
		{"expires in 36 hours", testNow.Add(36 * time.Hour), created},
		{"expires in exactly a week", testNow.Add(7 * 24 * time.Hour), created},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := backdatedLink(created, tt.expires, testNow, 20); !got.Equal(tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// TestPlan checks the plan itself: what the dashboard is meant to show.
func TestPlan(t *testing.T) {
	if len(sites) != 18 || len(groups) != 4 || len(tags) != 8 {
		t.Errorf("%d sites, %d groups, %d tags; want 18, 4, 8", len(sites), len(groups), len(tags))
	}
	var pinned, links, expiringSoon, ungrouped int
	knownTag := map[string]bool{}
	for _, tg := range tags {
		knownTag[tg.name] = true
	}
	for _, s := range sites {
		if s.pinned {
			pinned++
		}
		if s.group == "" {
			ungrouped++
		}
		if _, ok := fixturePaths[s.fixture]; !ok {
			t.Errorf("%s: unknown fixture %q", s.name, s.fixture)
		}
		for _, tg := range s.tags {
			if !knownTag[tg] {
				t.Errorf("%s: unknown tag %q", s.name, tg)
			}
		}
		prefix, w := ways(s.policies)
		if len(s.links) > 0 && (prefix == "" || w[0] != store.ViaLink) {
			t.Errorf("%s has links but no policy that takes them", s.name)
		}
		for _, l := range s.links {
			links++
			if at, err := parseExpires(l.expires); err != nil {
				t.Errorf("%s %q: %v", s.name, l.label, err)
			} else if at > 0 && at <= 48*time.Hour {
				expiringSoon++
			}
			if l.ageDays >= s.ageDays {
				t.Errorf("%s %q is older than its site", s.name, l.label)
			}
		}
		if s.redeployAgeDays >= s.ageDays {
			t.Errorf("%s is redeployed before it was created", s.name)
		}
	}
	if pinned != 4 || links != 11 || expiringSoon != 4 || ungrouped == 0 {
		t.Errorf("%d pinned, %d links (%d expiring within 48h), %d ungrouped; want 4, 11 (4), some", pinned, links, expiringSoon, ungrouped)
	}
}
