// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package notify

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"time"

	"wardlock.nz/server/internal/mail"
	"wardlock.nz/server/internal/sites"
	"wardlock.nz/server/internal/store"
)

// When each event fires (SPEC "Notifications").
const (
	// failingFor is how long an active domain fails before its owner hears.
	failingFor = 24 * time.Hour
	// linksWithin matches what the Overview calls expiring (V5.11).
	linksWithin = sites.ExpiringWithin
	// tokensWithin and previewsWithin are how far ahead expiries are read.
	tokensWithin   = 7 * 24 * time.Hour
	previewsWithin = 2 * 24 * time.Hour

	// recent is how far back the scans read events that happen once. The
	// worker runs in the process that causes them, so it needs only to
	// cover a slow pass; it stays well inside retention.
	recent = 6 * time.Hour
	// lapsedWithin is how far back a lapse is read: failures start at
	// least 72 hours before one.
	lapsedWithin = 7 * 24 * time.Hour

	timeFormat = "2 Jan 2006 15:04 UTC"
	dateFormat = "2006-01-02"
)

const (
	settingsPath = "/notifications"
	tokensPath   = "/tokens"
	overviewPath = "/"
	// maxSummaryItems bounds a summary; the rest are counted.
	maxSummaryItems = 50
)

// Detect queues every event the store shows. Reading the store rather than
// being told means a restart loses nothing, and keying each event means
// reading it again queues nothing.
func (s *Service) Detect(ctx context.Context) {
	if s.c.Webhooks != nil {
		s.detectWebhooks(ctx)
	}
	now := s.now()
	var entries []store.OutboxEntry
	add := func(what string, list []store.NoticeSubject, err error, build func(store.NoticeSubject) (string, string, mail.Notice)) {
		if err != nil {
			s.logErr(ctx, "read "+what, err)
			return
		}
		for _, n := range list {
			key, event, notice := build(n)
			e, err := s.entry(key, n.OwnerID, event, emailPayload{Notice: &notice})
			if err != nil {
				s.logErr(ctx, "build "+what, err)
				continue
			}
			entries = append(entries, e)
		}
	}
	st := s.c.Store

	l, err := st.VerifiedDomains(ctx, DomainVerified, now.Add(-recent))
	add("verified domains", l, err, domainVerified)
	l, err = st.FailingDomains(ctx, DomainFailing, failingFor)
	add("failing domains", l, err, domainFailing)
	l, err = st.LapsedDomains(ctx, DomainFailing, now.Add(-lapsedWithin))
	add("lapsed domains", l, err, domainLapsed)
	l, err = st.UsedUpLinks(ctx, LinkUsedUp, now.Add(-recent))
	add("used-up links", l, err, linkUsedUp)
	l, err = st.ExpiringTokens(ctx, TokenExpiring, now, now.Add(tokensWithin), tokensWithin)
	add("expiring tokens", l, err, tokenExpiring)
	l, err = st.ExpiringPreviews(ctx, PreviewExpiring, now, now.Add(previewsWithin), previewsWithin)
	add("expiring previews", l, err, previewExpiring)

	links, err := st.ExpiringLinks(ctx, LinksExpiring, now, now.Add(linksWithin), sites.ShortLinkLifetime)
	if err != nil {
		s.logErr(ctx, "read expiring links", err)
	}
	for _, d := range linkDigests(links, now) {
		e, err := s.entry(d.key, d.ownerID, LinksExpiring, emailPayload{Notice: &d.notice})
		if err != nil {
			s.logErr(ctx, "build link digest", err)
			continue
		}
		entries = append(entries, e)
	}

	if len(entries) == 0 {
		return
	}
	added, err := st.Enqueue(ctx, entries...)
	if err != nil {
		s.logErr(ctx, "queue notifications", err)
		return
	}
	if added > 0 {
		s.c.Log.LogAttrs(ctx, slog.LevelInfo, "notifications queued", slog.Int("count", added))
	}
}

func key(parts ...any) string {
	k := ""
	for i, p := range parts {
		if i > 0 {
			k += ":"
		}
		if t, ok := p.(time.Time); ok {
			p = t.UnixMilli()
		}
		k += fmt.Sprint(p)
	}
	return k
}

func sitePath(site, tab string) string {
	return "/sites/" + url.PathEscape(site) + "/" + tab
}

func domainVerified(n store.NoticeSubject) (string, string, mail.Notice) {
	return key(DomainVerified, n.ID, n.At), DomainVerified, mail.Notice{
		Subject: n.Name + " is verified",
		Lead:    "Your custom domain " + n.Name + " passed its DNS check and now serves your site " + n.SiteName + ".",
		Action:  "View the domain",
		URL:     sitePath(n.SiteName, "domains"),
	}
}

func domainFailing(n store.NoticeSubject) (string, string, mail.Notice) {
	return key(DomainFailing, n.ID, n.At), DomainFailing, mail.Notice{
		Subject: n.Name + " is failing its DNS checks",
		Lead:    "Your custom domain " + n.Name + " on your site " + n.SiteName + " has failed its DNS checks for more than 24 hours.",
		Note:    "It is still served, but it stops being served if the checks keep failing for 72 hours.",
		Action:  "Check the domain",
		URL:     sitePath(n.SiteName, "domains"),
	}
}

func domainLapsed(n store.NoticeSubject) (string, string, mail.Notice) {
	return key("domain_lapsed", n.ID, n.At), DomainFailing, mail.Notice{
		Subject: n.Name + " is no longer served",
		Lead:    "Your custom domain " + n.Name + " on your site " + n.SiteName + " failed its DNS checks for 72 hours, so it has stopped being served.",
		Note:    "Wardlock keeps checking it every day and serves it again once a check passes.",
		Action:  "Check the domain",
		URL:     sitePath(n.SiteName, "domains"),
	}
}

// linkName names a share link by its label, or its public ID without one.
// Neither is enough to open the site.
func linkName(n store.NoticeSubject) string {
	if n.Name != "" {
		return `"` + n.Name + `"`
	}
	return n.PublicID
}

func linkUsedUp(n store.NoticeSubject) (string, string, mail.Notice) {
	return key(LinkUsedUp, n.ID), LinkUsedUp, mail.Notice{
		Subject: "A share link on " + n.SiteName + " is used up",
		Lead:    "The share link " + linkName(n) + " on your site " + n.SiteName + " has been used as many times as it allows, so it no longer opens the site.",
		Action:  "View the site's links",
		URL:     sitePath(n.SiteName, "links"),
	}
}

func tokenExpiring(n store.NoticeSubject) (string, string, mail.Notice) {
	return key(TokenExpiring, n.ID, n.At), TokenExpiring, mail.Notice{
		Subject: "An API token expires soon",
		Lead:    `Your API token "` + n.Name + `" expires on ` + n.At.Format(timeFormat) + ". Anything using it stops working then.",
		Action:  "View your API tokens",
		URL:     tokensPath,
	}
}

func previewExpiring(n store.NoticeSubject) (string, string, mail.Notice) {
	return key(PreviewExpiring, n.SiteID, n.Name, n.At), PreviewExpiring, mail.Notice{
		Subject: "The preview " + n.Name + " of " + n.SiteName + " expires soon",
		Lead:    "The preview " + n.Name + " of your site " + n.SiteName + " expires on " + n.At.Format(timeFormat) + ". Deploy to it again to keep it.",
		Action:  "View the site's previews",
		URL:     sitePath(n.SiteName, "previews"),
	}
}

type linkDigest struct {
	key, ownerID string
	notice       mail.Notice
}

// linkDigests is one email for each owner in links, which come grouped by
// owner, keyed by day so an owner gets at most one a day.
func linkDigests(links []store.NoticeSubject, now time.Time) []linkDigest {
	var out []linkDigest
	for start := 0; start < len(links); {
		ownerID := links[start].OwnerID
		var items []string
		path := sitePath(links[start].SiteName, "links")
		end := start
		for ; end < len(links) && links[end].OwnerID == ownerID; end++ {
			n := links[end]
			items = append(items, n.SiteName+": "+linkName(n)+", "+n.At.Format(timeFormat))
			if n.SiteName != links[start].SiteName {
				path = overviewPath
			}
		}
		start = end
		subject := fmt.Sprintf("%d share links expire soon", len(items))
		if len(items) == 1 {
			subject = "A share link expires soon"
		}
		out = append(out, linkDigest{key(LinksExpiring, ownerID, now.Format(dateFormat)), ownerID, mail.Notice{
			Subject: subject,
			Lead:    "These share links expire within 48 hours:",
			Items:   items,
			Action:  "View your links",
			URL:     path,
		}})
	}
	return out
}

func summaryNotice(subjects []string) *mail.Notice {
	n := &mail.Notice{
		Subject: fmt.Sprintf("%d more Wardlock notifications", len(subjects)),
		Lead:    "You reached the hourly limit on notification emails, so Wardlock held these back:",
		Items:   subjects,
		Action:  "Open the dashboard",
		URL:     overviewPath,
	}
	if len(subjects) > maxSummaryItems {
		n.Items = subjects[:maxSummaryItems]
		n.Note = fmt.Sprintf("And %d more.", len(subjects)-maxSummaryItems)
	}
	return n
}
