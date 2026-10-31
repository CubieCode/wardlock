// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package notify_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"wardlock.nz/server/internal/notify"
	"wardlock.nz/server/internal/sites"
	"wardlock.nz/server/internal/store"
)

var signingKey = bytes.Repeat([]byte("k"), 32)

// receiver is a webhook endpoint that answers status and records requests.
type receiver struct {
	*httptest.Server
	mu     sync.Mutex
	status int
	got    []received
}

type received struct {
	header http.Header
	body   string
}

func newReceiver(t *testing.T) *receiver {
	t.Helper()
	r := &receiver{status: http.StatusNoContent}
	r.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		defer r.mu.Unlock()
		r.got = append(r.got, received{req.Header.Clone(), string(body)})
		w.WriteHeader(r.status)
	}))
	t.Cleanup(r.Close)
	return r
}

func (r *receiver) answer(status int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status = status
}

func (r *receiver) received() []received {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.got)
}

// hookService is a Service with webhooks on that may reach httptest servers.
func (w *world) hookService() *notify.Service {
	s := w.hookServiceGuarded(nil, nil)
	s.AllowLoopback(0)
	return s
}

// hookServiceGuarded is one with the production address policy, logging to
// log if given.
func (w *world) hookServiceGuarded(log *slog.Logger, allow []netip.Prefix) *notify.Service {
	return notify.New(notify.Config{
		Store: w.db.Notifications(), Mailer: w.box, DashboardURL: dashboard, Cap: w.cap,
		Now: func() time.Time { return w.clock }, Log: log,
		Webhooks: w.db.Webhooks(), Sites: w.db.Sites(), SigningKey: signingKey, AllowHTTP: true, AllowPrivate: allow,
	})
}

// hook registers a webhook straight in the store, past the URL check, and
// returns it with its secret.
func (w *world) hook(ownerID, url string, change func(*store.Webhook)) (store.Webhook, string) {
	w.t.Helper()
	h := store.Webhook{
		ID: store.NewID(), OwnerID: ownerID, URL: url, AllEvents: true, AllSites: true,
		SecretSalt: "salt-" + store.NewID(), CreatedAt: w.clock,
	}
	if change != nil {
		change(&h)
	}
	w.must(w.db.Webhooks().Create(w.t.Context(), h, notify.MaxWebhooks))
	return h, w.hookService().SigningSecret(h.ID, h.SecretSalt)
}

func (w *world) deliveries(h store.Webhook) []store.WebhookDelivery {
	w.t.Helper()
	list, err := w.db.Webhooks().Deliveries(w.t.Context(), h.OwnerID, h.ID, 1000)
	w.must(err)
	return list
}

func (w *world) reread(h store.Webhook) store.Webhook {
	w.t.Helper()
	got, err := w.db.Webhooks().Get(w.t.Context(), h.OwnerID, h.ID)
	w.must(err)
	return got
}

// hmacSHA256 is HMAC written out from its definition (RFC 2104), so the
// test does not lean on the code it checks.
func hmacSHA256(key, msg []byte) []byte {
	block := make([]byte, 64)
	if len(key) > len(block) {
		sum := sha256.Sum256(key)
		key = sum[:]
	}
	copy(block, key)
	inner, outer := sha256.New(), sha256.New()
	for _, b := range block {
		inner.Write([]byte{b ^ 0x36})
		outer.Write([]byte{b ^ 0x5c})
	}
	inner.Write(msg)
	outer.Write(inner.Sum(nil))
	return outer.Sum(nil)
}

// verify checks a delivery as a receiver would and returns its timestamp.
func verify(t *testing.T, r received, secret string) int64 {
	t.Helper()
	var ts int64
	var v1 string
	for part := range strings.SplitSeq(r.header.Get("Wardlock-Signature"), ",") {
		k, v, _ := strings.Cut(part, "=")
		switch k {
		case "t":
			ts, _ = strconv.ParseInt(v, 10, 64)
		case "v1":
			v1 = v
		}
	}
	want := hex.EncodeToString(hmacSHA256([]byte(secret), []byte(strconv.FormatInt(ts, 10)+"."+r.body)))
	if ts == 0 || v1 != want {
		t.Fatalf("signature %q does not verify", r.header.Get("Wardlock-Signature"))
	}
	return ts
}

type event struct {
	ID      string          `json:"id"`
	Type    string          `json:"type"`
	Created int64           `json:"created"`
	Data    json.RawMessage `json:"data"`
}

func parse(t *testing.T, body string) event {
	t.Helper()
	var e event
	dec := json.NewDecoder(strings.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&e); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	return e
}

func TestWebhookIsSigned(t *testing.T) {
	w := newWorld(t)
	o := w.owner("one")
	docs := w.site(o.ID, "docs")
	r := newReceiver(t)
	s := w.hookService()
	h, secret, err := s.CreateWebhook(t.Context(), o.ID, notify.WebhookInput{URL: r.URL})
	w.must(err)
	if !strings.HasPrefix(secret, "wlwh_") || len(secret) != 48 {
		t.Fatalf("secret = %q", secret)
	}
	if strings.Contains(fmt.Sprintf("%+v", h), secret) {
		t.Fatal("the stored webhook holds its secret")
	}
	s.SiteCreated(t.Context(), docs)
	s.DeliverWebhooks(t.Context())

	got := r.received()
	if len(got) != 1 {
		t.Fatalf("received %d", len(got))
	}
	if ts := verify(t, got[0], secret); ts != w.clock.Unix() {
		t.Fatalf("t = %d, want %d", ts, w.clock.Unix())
	}
	if got[0].header.Get("Wardlock-Event") != notify.EventSiteCreated || got[0].header.Get("Content-Type") != "application/json" {
		t.Fatalf("headers %v", got[0].header)
	}
	e := parse(t, got[0].body)
	want := fmt.Sprintf(`{"site":{"id":%q,"name":"docs"}}`, docs.ID)
	if e.ID == "" || e.Type != notify.EventSiteCreated || e.Created != w.clock.UnixMilli() || string(e.Data) != want {
		t.Fatalf("event %+v", e)
	}

	// A different secret must not verify: the check above means something.
	wrong := hex.EncodeToString(hmacSHA256([]byte(secret+"x"), []byte("1."+got[0].body)))
	if strings.Contains(got[0].header.Get("Wardlock-Signature"), wrong) {
		t.Fatal("signature ignores the secret")
	}
	d := w.deliveries(h)
	if len(d) != 1 || d[0].Status != store.WebhookDeliverySucceeded || d[0].ResponseCode != http.StatusNoContent || d[0].Attempts != 1 {
		t.Fatalf("delivery %+v", d)
	}
}

func TestWebhookRetriesWithBackoff(t *testing.T) {
	w := newWorld(t)
	o := w.owner("one")
	docs := w.site(o.ID, "docs")
	r := newReceiver(t)
	r.answer(http.StatusServiceUnavailable)
	h, secret := w.hook(o.ID, r.URL, nil)
	s := w.hookService()
	s.SiteCreated(t.Context(), docs)
	s.DeliverWebhooks(t.Context())

	// Not due until the backoff has passed, then 1 minute doubling.
	wait := time.Minute
	for attempt := 2; attempt <= 4; attempt++ {
		w.clock = w.clock.Add(wait - time.Second)
		s.DeliverWebhooks(t.Context())
		if n := len(r.received()); n != attempt-1 {
			t.Fatalf("attempt %d came early: %d received", attempt, n)
		}
		w.clock = w.clock.Add(time.Second)
		s.DeliverWebhooks(t.Context())
		if n := len(r.received()); n != attempt {
			t.Fatalf("attempt %d did not come: %d received", attempt, n)
		}
		wait *= 2
	}
	d := w.deliveries(h)
	if len(d) != 1 || d[0].Status != store.WebhookDeliveryPending || d[0].Attempts != 4 || d[0].ResponseCode != 503 || d[0].Error != "status" {
		t.Fatalf("delivery %+v", d)
	}
	if w.reread(h).FailingSince.IsZero() {
		t.Fatal("failure not marked")
	}

	// It then succeeds: same event, same body, fresh signature.
	r.answer(http.StatusOK)
	w.clock = w.clock.Add(wait)
	s.DeliverWebhooks(t.Context())
	got := r.received()
	if len(got) != 5 || got[4].body != got[0].body {
		t.Fatalf("received %d", len(got))
	}
	verify(t, got[4], secret)
	if d := w.deliveries(h); d[0].Status != store.WebhookDeliverySucceeded || d[0].Error != "" || d[0].Attempts != 5 {
		t.Fatalf("delivery %+v", d[0])
	}
	if !w.reread(h).FailingSince.IsZero() {
		t.Fatal("a success left the failure marked")
	}
}

func TestWebhookGivesUpAfterADay(t *testing.T) {
	w := newWorld(t)
	o := w.owner("one")
	docs := w.site(o.ID, "docs")
	r := newReceiver(t)
	r.answer(http.StatusInternalServerError)
	h, _ := w.hook(o.ID, r.URL, nil)
	s := w.hookService()
	s.SiteCreated(t.Context(), docs)
	start := w.clock
	for w.clock.Sub(start) < notify.WebhookRetryFor+2*hour {
		s.DeliverWebhooks(t.Context())
		w.clock = w.clock.Add(10 * time.Minute)
	}
	d := w.deliveries(h)
	if len(d) != 1 || d[0].Status != store.WebhookDeliveryFailed || d[0].FinishedAt.IsZero() {
		t.Fatalf("delivery %+v", d)
	}
	if !w.reread(h).DisabledAt.IsZero() {
		t.Fatal("disabled after one day")
	}
	n := len(r.received())
	// Backoff caps at an hour, so a day holds well over a dozen tries.
	if n < 20 || n > 40 {
		t.Fatalf("%d attempts in a day", n)
	}
	w.clock = w.clock.Add(48 * hour)
	s.DeliverWebhooks(t.Context())
	if len(r.received()) != n {
		t.Fatal("attempted after giving up")
	}
}

func TestWebhookIsDisabledAfterThreeDays(t *testing.T) {
	w := newWorld(t)
	o := w.owner("one")
	docs := w.site(o.ID, "docs")
	r := newReceiver(t)
	r.answer(http.StatusBadGateway)
	h, _ := w.hook(o.ID, r.URL+"/services/secret-path", nil)
	s := w.hookService()
	ctx := t.Context()

	// Turning every email off changes nothing: this one is mandatory.
	w.must(s.SetSettings(ctx, o.ID, map[string]bool{notify.DomainVerified: false, notify.DomainFailing: false, notify.LinkUsedUp: false, notify.LinksExpiring: false, notify.TokenExpiring: false, notify.PreviewExpiring: false}))

	start := w.clock
	s.SiteCreated(ctx, docs)
	n := 0
	for w.clock.Sub(start) < notify.WebhookDisableAfter+2*hour && w.reread(h).DisabledAt.IsZero() {
		n++
		s.PreviewDeleted(ctx, docs, fmt.Sprintf("p%d", n), w.clock)
		s.DeliverWebhooks(ctx)
		w.clock = w.clock.Add(hour)
	}
	off := w.reread(h)
	if off.DisabledAt.IsZero() || off.DisabledReason != store.WebhookDisabledFailing {
		t.Fatalf("webhook %+v", off)
	}
	if off.DisabledAt.Sub(start) < notify.WebhookDisableAfter {
		t.Fatalf("disabled after %v", off.DisabledAt.Sub(start))
	}

	// Its owner hears once, by host, whatever else happens.
	before := len(r.received())
	s.PreviewDeleted(ctx, docs, "after", w.clock)
	s.DeliverWebhooks(ctx)
	s.Deliver(ctx)
	w.clock = w.clock.Add(2 * hour)
	s.DeliverWebhooks(ctx)
	s.Deliver(ctx)
	got := w.box.subjects()
	if !slices.Equal(got, []string{"one@example.test: A webhook has been turned off"}) {
		t.Fatalf("emails %q", got)
	}
	body := string(w.box.sent[0].Text)
	if !strings.Contains(body, "127.0.0.1") || strings.Contains(body, "secret-path") {
		t.Fatalf("email body:\n%s", body)
	}
	if !strings.Contains(body, "/webhooks") {
		t.Fatalf("email does not link to the webhooks page:\n%s", body)
	}
	if len(r.received()) != before {
		t.Fatal("a disabled webhook was sent to")
	}
	for _, d := range w.deliveries(h) {
		if d.Status == store.WebhookDeliveryPending {
			t.Fatalf("still pending: %+v", d)
		}
	}
	_, err := s.TestWebhook(ctx, o.ID, h.ID)
	if !errors.Is(err, notify.ErrWebhookDisabled) {
		t.Fatalf("TestWebhook = %v", err)
	}

	// Turned back on, it starts clean and works; failing again means a new
	// run of three days and a new email.
	on := true
	again, err := s.UpdateWebhook(ctx, o.ID, h.ID, notify.WebhookChange{Enabled: &on})
	w.must(err)
	if !again.DisabledAt.IsZero() || again.DisabledReason != "" || !again.FailingSince.IsZero() {
		t.Fatalf("webhook %+v", again)
	}
	r.answer(http.StatusOK)
	s.PreviewDeleted(ctx, docs, "back", w.clock)
	s.DeliverWebhooks(ctx)
	if len(r.received()) != before+1 {
		t.Fatalf("after turning it on: %d", len(r.received())-before)
	}
}

func TestWebhookAnOccasionalSuccessKeepsItOn(t *testing.T) {
	w := newWorld(t)
	o := w.owner("one")
	docs := w.site(o.ID, "docs")
	r := newReceiver(t)
	h, _ := w.hook(o.ID, r.URL, nil)
	s := w.hookService()
	ctx := t.Context()
	for i := range 10 {
		r.answer(http.StatusBadGateway)
		if i%3 == 2 {
			r.answer(http.StatusOK)
		}
		s.PreviewDeleted(ctx, docs, fmt.Sprintf("p%d", i), w.clock)
		s.DeliverWebhooks(ctx)
		w.clock = w.clock.Add(day)
	}
	if !w.reread(h).DisabledAt.IsZero() {
		t.Fatal("disabled although it never failed for 3 days in a row")
	}
}

func TestWebhookDoesNotCountTowardsTheEmailCap(t *testing.T) {
	w := newWorld(t)
	w.cap = 1
	o := w.owner("one")
	docs := w.site(o.ID, "docs")
	r := newReceiver(t)
	w.hook(o.ID, r.URL, nil)
	s := w.hookService()
	ctx := t.Context()
	for i := range 5 {
		s.PreviewDeleted(ctx, docs, fmt.Sprintf("p%d", i), w.clock)
	}
	s.DeliverWebhooks(ctx)
	if len(r.received()) != 5 {
		t.Fatalf("received %d", len(r.received()))
	}
	w.usedUpLink(o.ID, docs.ID, "Link")
	s.Detect(ctx)
	s.Deliver(ctx)
	if got := w.box.subjects(); len(got) != 1 {
		t.Fatalf("emails %q", got)
	}
}

func TestWebhookRedelivery(t *testing.T) {
	w := newWorld(t)
	o, other := w.owner("one"), w.owner("two")
	docs := w.site(o.ID, "docs")
	r := newReceiver(t)
	h, secret := w.hook(o.ID, r.URL, nil)
	s := w.hookService()
	ctx := t.Context()
	s.SiteCreated(ctx, docs)
	s.DeliverWebhooks(ctx)
	first := w.deliveries(h)[0]

	if _, err := s.RedeliverWebhook(ctx, other.ID, h.ID, first.ID); !errors.Is(err, notify.ErrNotFound) {
		t.Fatalf("another owner: %v", err)
	}
	if _, err := s.RedeliverWebhook(ctx, o.ID, h.ID, "nothing"); !errors.Is(err, notify.ErrNotFound) {
		t.Fatalf("unknown delivery: %v", err)
	}

	w.clock = w.clock.Add(hour)
	d, err := s.RedeliverWebhook(ctx, o.ID, h.ID, first.ID)
	w.must(err)
	if d.ID == first.ID || d.EventID != first.EventID || d.RedeliveryOf != first.ID || d.Status != store.WebhookDeliveryPending {
		t.Fatalf("redelivery %+v of %+v", d, first)
	}
	s.DeliverWebhooks(ctx)
	got := r.received()
	if len(got) != 2 || got[1].body != got[0].body {
		t.Fatalf("received %+v", got)
	}
	if t0, t1 := verify(t, got[0], secret), verify(t, got[1], secret); t1 != t0+3600 {
		t.Fatalf("t = %d then %d", t0, t1)
	}
	if list := w.deliveries(h); len(list) != 2 || list[0].Status != store.WebhookDeliverySucceeded {
		t.Fatalf("deliveries %+v", list)
	}
}

func TestWebhookKeepsTheLastDeliveries(t *testing.T) {
	w := newWorld(t)
	o := w.owner("one")
	docs := w.site(o.ID, "docs")
	r := newReceiver(t)
	h, _ := w.hook(o.ID, r.URL, nil)
	s := w.hookService()
	ctx := t.Context()
	for i := range notify.KeepDeliveries + 7 {
		s.PreviewDeleted(ctx, docs, fmt.Sprintf("p%d", i), w.clock)
		w.clock = w.clock.Add(time.Second)
		s.DeliverWebhooks(ctx)
	}
	list, err := s.WebhookDeliveries(ctx, o.ID, h.ID)
	w.must(err)
	if len(list) != notify.KeepDeliveries {
		t.Fatalf("kept %d", len(list))
	}
	if !strings.Contains(list[0].Body, fmt.Sprintf(`"p%d"`, notify.KeepDeliveries+6)) {
		t.Fatalf("newest is %s", list[0].Body)
	}
}

func TestWebhookResponseIsCapped(t *testing.T) {
	w := newWorld(t)
	o := w.owner("one")
	docs := w.site(o.ID, "docs")
	var written atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.WriteHeader(http.StatusOK)
		chunk := make([]byte, 64<<10)
		for {
			n, err := rw.Write(chunk)
			written.Add(int64(n))
			if err != nil {
				return
			}
			rw.(http.Flusher).Flush()
		}
	}))
	t.Cleanup(srv.Close)
	h, _ := w.hook(o.ID, srv.URL, nil)
	s := w.hookService()
	s.SiteCreated(t.Context(), docs)
	// An endless answer: this returns only because reading stops.
	s.DeliverWebhooks(t.Context())
	if d := w.deliveries(h); d[0].Status != store.WebhookDeliverySucceeded || d[0].ResponseCode != 200 {
		t.Fatalf("delivery %+v", d[0])
	}
}

func TestWebhookResponseHeadersAreCapped(t *testing.T) {
	w := newWorld(t)
	o := w.owner("one")
	docs := w.site(o.ID, "docs")
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.Header().Set("X-Filler", strings.Repeat("x", 3*notify.MaxWebhookResponse))
		rw.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	h, _ := w.hook(o.ID, srv.URL, nil)
	s := w.hookService()
	s.SiteCreated(t.Context(), docs)
	s.DeliverWebhooks(t.Context())
	if d := w.deliveries(h); d[0].Status != store.WebhookDeliveryPending || d[0].Error != "connection" {
		t.Fatalf("delivery %+v", d[0])
	}
}

func TestWebhookTimesOut(t *testing.T) {
	w := newWorld(t)
	o := w.owner("one")
	docs := w.site(o.ID, "docs")
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	t.Cleanup(func() { close(release); srv.Close() })
	h, _ := w.hook(o.ID, srv.URL, nil)
	s := w.hookService()
	s.AllowLoopback(50 * time.Millisecond)
	s.SiteCreated(t.Context(), docs)
	s.DeliverWebhooks(t.Context())
	if d := w.deliveries(h); d[0].Status != store.WebhookDeliveryPending || d[0].Error != "timeout" || d[0].ResponseCode != 0 {
		t.Fatalf("delivery %+v", d[0])
	}
}

func TestWebhookFollowsNoRedirect(t *testing.T) {
	w := newWorld(t)
	o := w.owner("one")
	docs := w.site(o.ID, "docs")
	target := newReceiver(t)
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		http.Redirect(rw, r, target.URL, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(srv.Close)
	h, _ := w.hook(o.ID, srv.URL, nil)
	s := w.hookService()
	s.SiteCreated(t.Context(), docs)
	s.DeliverWebhooks(t.Context())
	if len(target.received()) != 0 {
		t.Fatal("followed a redirect")
	}
	d := w.deliveries(h)[0]
	if d.Status != store.WebhookDeliveryPending || d.ResponseCode != http.StatusTemporaryRedirect || d.Error != "status" {
		t.Fatalf("a redirect is a failure: %+v", d)
	}
}

func TestCrashRequeuesWebhooksButNotEmail(t *testing.T) {
	w := newWorld(t)
	o := w.owner("one")
	docs := w.site(o.ID, "docs")
	r := newReceiver(t)
	h, _ := w.hook(o.ID, r.URL, nil)
	w.usedUpLink(o.ID, docs.ID, "Link")
	s := w.hookService()
	ctx := t.Context()
	s.Detect(ctx)
	n := w.db.Notifications()
	for _, channel := range []store.NotificationChannel{store.NotificationEmail, store.NotificationWebhook} {
		rows, err := n.Due(ctx, channel, w.clock, 10)
		w.must(err)
		if len(rows) != 1 {
			t.Fatalf("%s: %d rows", channel, len(rows))
		}
		w.must(n.Claim(ctx, rows[0].ID))
	}

	restarted := w.hookService()
	restarted.Recover(ctx)
	restarted.Deliver(ctx)
	restarted.DeliverWebhooks(ctx)
	if got := w.box.subjects(); len(got) != 0 {
		t.Fatalf("an interrupted email was resent: %q", got)
	}
	if len(r.received()) != 1 {
		t.Fatalf("webhook received %d", len(r.received()))
	}
	if d := w.deliveries(h)[0]; d.Status != store.WebhookDeliverySucceeded {
		t.Fatalf("delivery %+v", d)
	}
}

func TestWebhookQueuedThenGone(t *testing.T) {
	cases := map[string]func(*world, *notify.Service, store.Webhook){
		"deleted": func(w *world, s *notify.Service, h store.Webhook) {
			w.must(s.DeleteWebhook(w.t.Context(), h.OwnerID, h.ID))
		},
		"turned off": func(w *world, s *notify.Service, h store.Webhook) {
			off := false
			_, err := s.UpdateWebhook(w.t.Context(), h.OwnerID, h.ID, notify.WebhookChange{Enabled: &off})
			w.must(err)
		},
		"owner suspended": func(w *world, _ *notify.Service, h store.Webhook) {
			w.must(w.db.Owners().Suspend(w.t.Context(), h.OwnerID, w.clock))
		},
	}
	for name, gone := range cases {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			o := w.owner("one")
			docs := w.site(o.ID, "docs")
			r := newReceiver(t)
			h, _ := w.hook(o.ID, r.URL, nil)
			s := w.hookService()
			ctx := t.Context()
			s.SiteCreated(ctx, docs)
			gone(w, s, h)
			s.DeliverWebhooks(ctx)
			s.DeliverWebhooks(ctx)
			if len(r.received()) != 0 {
				t.Fatal("sent")
			}
			if rows, err := w.db.Notifications().Due(ctx, store.NotificationWebhook, w.clock.Add(day), 10); err != nil || len(rows) != 0 {
				t.Fatalf("still due: %+v, %v", rows, err)
			}
		})
	}
}

func TestWebhookScope(t *testing.T) {
	w := newWorld(t)
	o, other := w.owner("one"), w.owner("two")
	docs, blog := w.site(o.ID, "docs"), w.site(o.ID, "blog")
	theirs := w.site(other.ID, "theirs")
	everything, onlyDocs, onlyLinks, others := newReceiver(t), newReceiver(t), newReceiver(t), newReceiver(t)
	w.hook(o.ID, everything.URL, nil)
	w.hook(o.ID, onlyDocs.URL, func(h *store.Webhook) {
		h.AllSites, h.Sites = false, []store.WebhookSite{{ID: docs.ID}}
	})
	w.hook(o.ID, onlyLinks.URL, func(h *store.Webhook) {
		h.AllEvents, h.Events = false, []string{notify.EventLinkCreated}
	})
	// Another owner's webhook, even one listing this owner's site by ID.
	w.hook(other.ID, others.URL, func(h *store.Webhook) {
		h.AllSites, h.Sites = false, []store.WebhookSite{{ID: docs.ID}, {ID: theirs.ID}}
	})
	s := w.hookService()
	ctx := t.Context()
	s.SiteCreated(ctx, docs)
	s.SiteCreated(ctx, blog)
	s.LinkCreated(ctx, docs, store.ShareLink{ID: "l1", PublicID: "p1"})
	s.DeliverWebhooks(ctx)
	if n := len(everything.received()); n != 3 {
		t.Errorf("everything got %d", n)
	}
	if n := len(onlyDocs.received()); n != 2 {
		t.Errorf("docs webhook got %d", n)
	}
	if n := len(onlyLinks.received()); n != 1 {
		t.Errorf("links webhook got %d", n)
	}
	if n := len(others.received()); n != 0 {
		t.Errorf("another owner's webhook got %d", n)
	}
	// Every webhook gets the same event ID, for one event.
	linkEvent := func(r *receiver) string {
		for _, g := range r.received() {
			if e := parse(t, g.body); e.Type == notify.EventLinkCreated {
				return e.ID
			}
		}
		return ""
	}
	if a, b := linkEvent(everything), linkEvent(onlyLinks); a == "" || a != b {
		t.Errorf("deliveries of one event have IDs %q and %q", a, b)
	}
}

func TestWebhookListingADeletedSite(t *testing.T) {
	w := newWorld(t)
	o := w.owner("one")
	docs, blog := w.site(o.ID, "docs"), w.site(o.ID, "blog")
	r := newReceiver(t)
	h, _ := w.hook(o.ID, r.URL, func(h *store.Webhook) {
		h.AllSites, h.Sites = false, []store.WebhookSite{{ID: docs.ID}}
	})
	s := w.hookService()
	ctx := t.Context()
	w.must(w.db.Sites().Delete(ctx, o.ID, docs.ID))
	s.SiteDeleted(ctx, docs)
	s.DeliverWebhooks(ctx)
	if got := r.received(); len(got) != 1 || parse(t, got[0].body).Type != notify.EventSiteDeleted {
		t.Fatalf("received %+v", got)
	}
	// Its list is now empty, which does not mean every site.
	got := w.reread(h)
	if got.AllSites || len(got.Sites) != 0 {
		t.Fatalf("webhook %+v", got)
	}
	s.SiteCreated(ctx, blog)
	s.DeliverWebhooks(ctx)
	if len(r.received()) != 1 {
		t.Fatal("a webhook left listing nothing got another site's event")
	}
}

func TestWebhookEventsFromTheScans(t *testing.T) {
	w := newWorld(t)
	o := w.owner("one")
	docs := w.site(o.ID, "docs")
	// Webhooks are told whatever the owner chose for email.
	s := w.hookService()
	ctx := t.Context()
	w.must(s.SetSettings(ctx, o.ID, map[string]bool{notify.LinkUsedUp: false, notify.DomainVerified: false, notify.DomainFailing: false}))
	r := newReceiver(t)
	h, _ := w.hook(o.ID, r.URL, nil)
	w.clock = w.clock.Add(time.Minute)

	l := w.usedUpLink(o.ID, docs.ID, "Client")
	w.domain(o.ID, docs.ID, "new.example.test", func(d *store.Domain) { d.VerifiedAt = w.clock })
	w.domain(o.ID, docs.ID, "gone.example.test", func(d *store.Domain) {
		d.Status, d.FailingSince = store.DomainLapsed, w.clock.Add(-sites.LapseAfter)
	})
	s.Detect(ctx)
	s.Detect(ctx) // reading it again queues nothing
	s.DeliverWebhooks(ctx)
	seen := map[string]string{}
	for _, g := range r.received() {
		e := parse(t, g.body)
		var k string
		if e.Type == notify.EventDomainStatusChanged {
			var d struct {
				Domain struct{ Hostname, Status string }
			}
			w.must(json.Unmarshal(e.Data, &d))
			k = d.Domain.Hostname + "=" + d.Domain.Status
		} else {
			k = e.Type
		}
		seen[k] = string(e.Data)
	}
	if len(seen) != 3 || seen["new.example.test=active"] == "" || seen["gone.example.test=lapsed"] == "" {
		t.Fatalf("seen %v", seen)
	}
	want := fmt.Sprintf(`{"site":{"id":%q,"name":"docs"},"link":{"id":%q,"publicId":%q,"label":"Client"}}`, docs.ID, l.ID, l.PublicID)
	if seen[notify.EventLinkUsedUp] != want {
		t.Fatalf("link.used_up = %s, want %s", seen[notify.EventLinkUsedUp], want)
	}
	if got := w.box.subjects(); len(got) != 0 {
		t.Fatalf("emails %q", got)
	}

	// A webhook made after an event does not hear of it, nor does a site
	// that has been taken down speak.
	late := newReceiver(t)
	w.clock = w.clock.Add(time.Minute)
	w.hook(o.ID, late.URL, nil)
	s.Detect(ctx)
	s.DeliverWebhooks(ctx)
	if n := len(late.received()); n != 0 {
		t.Fatalf("a newer webhook got %d old events", n)
	}
	if n := len(w.deliveries(h)); n != 3 {
		t.Fatalf("%d deliveries", n)
	}
}

// TestWebhookPayloadsHoldNoSecrets sends every event through a capturing
// handler and checks that nothing in the bodies or the log is a token, the
// signing secret, the URL's secret path or an email address.
func TestWebhookPayloadsHoldNoSecrets(t *testing.T) {
	w := newWorld(t)
	o := w.owner("one")
	docs := w.site(o.ID, "docs")
	r := newReceiver(t)
	var logs bytes.Buffer
	s := w.hookServiceGuarded(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})), nil)
	s.AllowLoopback(0)
	ctx := t.Context()
	h, signing, err := s.CreateWebhook(ctx, o.ID, notify.WebhookInput{URL: r.URL + "/services/T000/B000/urlsecret"})
	w.must(err)

	link := store.ShareLink{ID: "link", SiteID: docs.ID, PublicID: "p000000001", TokenHash: "stored-token-hash", Label: "Client", PathPrefix: "/"}
	s.SiteCreated(ctx, docs)
	s.SiteDeleted(ctx, docs)
	s.DeployActivated(ctx, docs, store.Deploy{ID: "d1", ActivatedAt: w.clock}, "deploy")
	s.PreviewDeployed(ctx, docs, store.Deploy{ID: "d2", Preview: "pr-1"})
	s.PreviewDeleted(ctx, docs, "pr-1", w.clock)
	s.LinkCreated(ctx, docs, link)
	s.LinkRevoked(ctx, docs, link)
	s.FileWithheld(ctx, "file_withheld:k", o.ID, docs.ID, docs.Name, []string{"/a.png"})
	_, err = s.TestWebhook(ctx, o.ID, h.ID)
	w.must(err)
	r.answer(http.StatusInternalServerError)
	s.DeliverWebhooks(ctx)

	got := r.received()
	if len(got) != 9 {
		t.Fatalf("received %d", len(got))
	}
	types := map[string]bool{}
	for _, g := range got {
		types[parse(t, g.body).Type] = true
		for _, leak := range []string{link.TokenHash, signing, "@example.test", "urlsecret", secret} {
			if strings.Contains(g.body, leak) {
				t.Errorf("body holds %q: %s", leak, g.body)
			}
		}
	}
	if len(types) != 9 {
		t.Fatalf("types %v", types)
	}
	for _, leak := range []string{signing, "urlsecret", "/services/"} {
		if strings.Contains(logs.String(), leak) {
			t.Errorf("log holds %q:\n%s", leak, logs.String())
		}
	}
	if !strings.Contains(logs.String(), "webhook delivery failed") {
		t.Fatalf("nothing logged:\n%s", logs.String())
	}
	for _, d := range w.deliveries(h) {
		if strings.Contains(d.Error, "urlsecret") {
			t.Errorf("delivery error holds the URL: %q", d.Error)
		}
	}
}

func TestWebhookURLs(t *testing.T) {
	w := newWorld(t)
	o := w.owner("one")
	ctx := t.Context()
	https := notify.New(notify.Config{Store: w.db.Notifications(), Webhooks: w.db.Webhooks(), Sites: w.db.Sites(), SigningKey: signingKey})
	local := w.hookServiceGuarded(nil, nil)
	cases := []struct {
		name  string
		s     *notify.Service
		url   string
		field string
	}{
		{"https", https, "https://hooks.example.test/x?a=b", ""},
		{"a port", https, "https://hooks.example.test:8443/", ""},
		{"http outside local mode", https, "http://hooks.example.test/", "url"},
		{"http in local mode", local, "http://hooks.example.test/", ""},
		{"another scheme", local, "ftp://hooks.example.test/", "url"},
		{"no host", https, "https:///path", "url"},
		{"not a URL", https, "::nope", "url"},
		{"empty", https, "", "url"},
		{"credentials", https, "https://user:pass@hooks.example.test/", "url"},
		{"a fragment", https, "https://hooks.example.test/#x", "url"},
		{"too long", https, "https://hooks.example.test/" + strings.Repeat("x", 2048), "url"},
		{"loopback literal", local, "http://127.0.0.1:8080/", "url"},
		{"metadata literal", https, "https://169.254.169.254/latest", "url"},
		{"private literal", https, "https://10.0.0.1/", "url"},
		{"IPv6 loopback literal", https, "https://[::1]/", "url"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, _, err := c.s.CreateWebhook(ctx, o.ID, notify.WebhookInput{URL: c.url})
			var we *notify.WebhookError
			switch {
			case c.field == "" && err != nil:
				t.Fatalf("refused: %v", err)
			case c.field != "" && (!errors.As(err, &we) || we.Field != c.field):
				t.Fatalf("err = %v", err)
			}
			if err == nil {
				w.must(c.s.DeleteWebhook(ctx, o.ID, h.ID))
			}
		})
	}
}

func TestWebhookLimitsAndValidation(t *testing.T) {
	w := newWorld(t)
	o, other := w.owner("one"), w.owner("two")
	w.site(o.ID, "docs")
	theirs := w.site(other.ID, "theirs")
	s := w.hookService()
	ctx := t.Context()
	add := func(in notify.WebhookInput) (store.Webhook, error) {
		in.URL = "https://hooks.example.test/"
		h, _, err := s.CreateWebhook(ctx, o.ID, in)
		return h, err
	}
	if _, err := add(notify.WebhookInput{Sites: []string{theirs.Name}}); !errors.Is(err, notify.ErrNotFound) {
		t.Fatalf("another owner's site: %v", err)
	}
	if _, err := add(notify.WebhookInput{Sites: []string{"missing"}}); !errors.Is(err, notify.ErrNotFound) {
		t.Fatalf("a missing site: %v", err)
	}
	var we *notify.WebhookError
	if _, err := add(notify.WebhookInput{Events: []string{"ping"}}); !errors.As(err, &we) || we.Field != "events" {
		t.Fatalf("ping cannot be asked for: %v", err)
	}
	if _, err := add(notify.WebhookInput{Description: strings.Repeat("é", 201)}); !errors.As(err, &we) || we.Field != "description" {
		t.Fatalf("a long description: %v", err)
	}
	h, err := add(notify.WebhookInput{Events: []string{notify.EventSiteCreated, notify.EventSiteCreated}, Sites: []string{"docs", "docs"}})
	w.must(err)
	if len(h.Events) != 1 || len(h.Sites) != 1 {
		t.Fatalf("repeats kept: %+v", h)
	}
	for range notify.MaxWebhooks - 1 {
		_, err := add(notify.WebhookInput{})
		w.must(err)
	}
	if _, err := add(notify.WebhookInput{}); !errors.Is(err, notify.ErrWebhookLimit) {
		t.Fatalf("the eleventh: %v", err)
	}
	if _, _, err := s.CreateWebhook(ctx, other.ID, notify.WebhookInput{URL: "https://hooks.example.test/"}); err != nil {
		t.Fatalf("another owner has their own limit: %v", err)
	}
}

func TestWebhookUpdate(t *testing.T) {
	w := newWorld(t)
	o, other := w.owner("one"), w.owner("two")
	docs := w.site(o.ID, "docs")
	theirs := w.site(other.ID, "theirs")
	h, _ := w.hook(o.ID, "https://old.example.test/", func(h *store.Webhook) { h.FailingSince = w.clock })
	s := w.hookService()
	ctx := t.Context()
	u, d := "https://new.example.test/", "Now described"
	got, err := s.UpdateWebhook(ctx, o.ID, h.ID, notify.WebhookChange{
		URL: &u, Description: &d, Events: []string{notify.EventSiteDeleted}, Sites: []string{docs.Name},
	})
	w.must(err)
	if got.URL != u || got.Description != d || got.AllEvents || got.AllSites || len(got.Sites) != 1 || got.Sites[0].ID != docs.ID {
		t.Fatalf("webhook %+v", got)
	}
	got, err = s.UpdateWebhook(ctx, o.ID, h.ID, notify.WebhookChange{AllEvents: true})
	w.must(err)
	if !got.AllEvents || got.AllSites || got.URL != u {
		t.Fatalf("changed more than asked: %+v", got)
	}
	got, err = s.UpdateWebhook(ctx, o.ID, h.ID, notify.WebhookChange{AllSites: true})
	w.must(err)
	if !got.AllSites {
		t.Fatalf("webhook %+v", got)
	}
	off := false
	got, err = s.UpdateWebhook(ctx, o.ID, h.ID, notify.WebhookChange{Enabled: &off})
	w.must(err)
	if got.DisabledAt.IsZero() || got.DisabledReason != store.WebhookDisabledOwner {
		t.Fatalf("webhook %+v", got)
	}
	bad := "http://new.example.test/"
	var we *notify.WebhookError
	guarded := notify.New(notify.Config{Store: w.db.Notifications(), Webhooks: w.db.Webhooks(), Sites: w.db.Sites(), SigningKey: signingKey})
	if _, err := guarded.UpdateWebhook(ctx, o.ID, h.ID, notify.WebhookChange{URL: &bad}); !errors.As(err, &we) {
		t.Fatalf("plain http: %v", err)
	}
	if _, err := s.UpdateWebhook(ctx, o.ID, h.ID, notify.WebhookChange{Sites: []string{theirs.Name}}); !errors.Is(err, notify.ErrNotFound) {
		t.Fatalf("another owner's site: %v", err)
	}
	if _, err := s.UpdateWebhook(ctx, other.ID, h.ID, notify.WebhookChange{Description: &d}); !errors.Is(err, notify.ErrNotFound) {
		t.Fatalf("another owner's webhook: %v", err)
	}
}

func TestWebhookSecretNeedsTheServerKey(t *testing.T) {
	w := newWorld(t)
	o := w.owner("one")
	s := w.hookService()
	h, a, err := s.CreateWebhook(t.Context(), o.ID, notify.WebhookInput{URL: "https://a.example.test/"})
	w.must(err)
	otherKey := notify.New(notify.Config{Store: w.db.Notifications(), SigningKey: bytes.Repeat([]byte("z"), 32)})
	if otherKey.SigningSecret(h.ID, h.SecretSalt) == a {
		t.Fatal("the secret does not depend on the server key")
	}
	if s.SigningSecret(h.ID, h.SecretSalt) != a {
		t.Fatal("the secret is not reproducible")
	}
	if s.SigningSecret(h.ID, "another salt") == a || s.SigningSecret("another id", h.SecretSalt) == a {
		t.Fatal("the secret does not depend on the webhook")
	}
}

func TestWebhookAddressPolicy(t *testing.T) {
	ten := netip.MustParsePrefix("10.20.0.0/16")
	ula := netip.MustParsePrefix("fd00::/8")
	cases := []struct {
		addr  string
		want  bool
		allow []netip.Prefix
	}{
		{addr: "93.184.216.34", want: true},
		{addr: "2606:2800:220:1::1", want: true},
		{addr: "::ffff:93.184.216.34", want: true},
		{addr: "64:ff9b::5db8:d822", want: true},

		{addr: "127.0.0.1"}, {addr: "127.255.255.254"}, {addr: "::1"},
		{addr: "10.0.0.1"}, {addr: "172.16.0.1"}, {addr: "172.31.255.255"}, {addr: "192.168.1.1"},
		{addr: "169.254.169.254"}, {addr: "fe80::1"}, {addr: "fe80::1%eth0"},
		{addr: "fc00::1"}, {addr: "fd12:3456::1"},
		{addr: "224.0.0.1"}, {addr: "239.255.255.250"}, {addr: "ff02::1"}, {addr: "ff0e::1"},
		{addr: "0.0.0.0"}, {addr: "::"},
		{addr: "0.1.2.3"}, {addr: "255.255.255.255"}, {addr: "100.64.0.1"},
		// An IPv4 address in IPv6 clothes is judged as IPv4.
		{addr: "::ffff:127.0.0.1"}, {addr: "::ffff:10.0.0.1"}, {addr: "::ffff:169.254.169.254"},
		{addr: "64:ff9b::7f00:1"}, {addr: "64:ff9b::a9fe:a9fe"},

		{addr: "10.20.1.1", want: true, allow: []netip.Prefix{ten}},
		{addr: "::ffff:10.20.1.1", want: true, allow: []netip.Prefix{ten}},
		{addr: "fd12::1", want: true, allow: []netip.Prefix{ula}},
		{addr: "10.21.1.1", allow: []netip.Prefix{ten}},
		// Opening a range opens nothing outside the private ones.
		{addr: "127.0.0.1", allow: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}},
		{addr: "169.254.169.254", allow: []netip.Prefix{netip.MustParsePrefix("169.254.0.0/16")}},
	}
	for _, c := range cases {
		if got := notify.Permitted(netip.MustParseAddr(c.addr), c.allow...); got != c.want {
			t.Errorf("Permitted(%s, %v) = %v", c.addr, c.allow, got)
		}
	}
}

// TestWebhookRefusesEachAddressClass goes through the real client: a stored
// URL, past the check made at creation, ends at the dialler and is refused
// before any connection, whatever is listening.
func TestWebhookRefusesEachAddressClass(t *testing.T) {
	w := newWorld(t)
	s := w.hookServiceGuarded(nil, nil)
	ctx := t.Context()
	r := newReceiver(t)
	_, port, err := net.SplitHostPort(r.Listener.Addr().String())
	w.must(err)
	hosts := []string{
		"127.0.0.1", "[::1]", "10.0.0.1", "172.16.0.1", "192.168.0.1", "169.254.169.254", "[fe80::1]",
		"[fd00::1]", "224.0.0.1", "[ff02::1]", "0.0.0.0", "[::]", "[::ffff:127.0.0.1]",
	}
	var hooks []store.Webhook
	for _, host := range hosts {
		// One owner each: an owner may have only ten webhooks.
		owner := w.owner(fmt.Sprintf("o%d", len(hooks)))
		h, _ := w.hook(owner.ID, "http://"+host+":"+port+"/", nil)
		hooks = append(hooks, h)
		if _, err := s.TestWebhook(ctx, owner.ID, h.ID); err != nil {
			t.Fatal(err)
		}
	}
	s.DeliverWebhooks(ctx)
	for _, h := range hooks {
		d := w.deliveries(h)
		if len(d) != 1 || d[0].Error != notify.FailRefused || d[0].ResponseCode != 0 || d[0].Attempts != 1 {
			t.Errorf("%s: %+v", h.URL, d)
		}
	}
	if n := len(r.received()); n != 0 {
		t.Fatalf("%d requests reached a refused address", n)
	}
}

// dnsServer answers A questions from next, one lookup at a time, and AAAA
// with nothing, and returns a resolver that asks it.
func dnsServer(t *testing.T, next func(lookup int) netip.Addr) (*net.Resolver, *atomic.Int64) {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	var lookups atomic.Int64
	go func() {
		buf := make([]byte, 512)
		for {
			_, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			end := 12
			for buf[end] != 0 {
				end += int(buf[end]) + 1
			}
			question := buf[12 : end+5]
			isA := buf[end+1] == 0 && buf[end+2] == 1
			answers := byte(0)
			if isA {
				answers = 1
			}
			out := append([]byte{buf[0], buf[1], 0x81, 0x80, 0, 1, 0, answers, 0, 0, 0, 0}, question...)
			if isA {
				ip := next(int(lookups.Add(1))).As4()
				out = append(out, 0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 0, 0, 4)
				out = append(out, ip[:]...)
			}
			_, _ = pc.WriteTo(out, from)
		}
	}()
	return &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return new(net.Dialer).DialContext(ctx, "udp", pc.LocalAddr().String())
	}}, &lookups
}

// TestWebhookDNSAnswerChanges is DNS rebinding: a name that passed on one
// lookup answers with a refused address on the next. Each attempt dials
// afresh and the dialler judges the address it is about to connect to, so
// there is no earlier check to pass. 127.0.0.1 stands in for a public
// address here; 127.0.0.2 is what the real policy refuses.
func TestWebhookDNSAnswerChanges(t *testing.T) {
	r := newReceiver(t)
	_, port, err := net.SplitHostPort(r.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	public := netip.MustParseAddr("127.0.0.1")
	resolver, lookups := dnsServer(t, func(n int) netip.Addr {
		if n == 1 {
			return public
		}
		return netip.MustParseAddr("127.0.0.2")
	})
	var judged sync.Map
	client := notify.GuardedClient(func(a netip.Addr) bool {
		judged.Store(a, true)
		return a == public || notify.Permitted(a)
	}, resolver)
	post := func() error {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://rebind.example.test:"+port+"/", strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		return resp.Body.Close()
	}
	if err := post(); err != nil {
		t.Fatalf("first attempt: %v", err)
	}
	err = post()
	if !errors.Is(err, notify.ErrRefusedAddress) {
		t.Fatalf("second attempt = %v, want the dialler's refusal", err)
	}
	if n := len(r.received()); n != 1 {
		t.Fatalf("%d requests arrived", n)
	}
	if lookups.Load() != 2 {
		t.Fatalf("%d lookups: a connection was reused, or an answer cached", lookups.Load())
	}
	if _, ok := judged.Load(netip.MustParseAddr("127.0.0.2")); !ok {
		t.Fatal("the dialler never saw the second answer")
	}
}

func TestWebhookWorkerRuns(t *testing.T) {
	w := newWorld(t)
	o := w.owner("one")
	docs := w.site(o.ID, "docs")
	r := newReceiver(t)
	w.hook(o.ID, r.URL, nil)
	s := notify.New(notify.Config{
		Store: w.db.Notifications(), Mailer: w.box, DashboardURL: dashboard,
		Webhooks: w.db.Webhooks(), Sites: w.db.Sites(), SigningKey: signingKey, AllowHTTP: true,
	})
	s.AllowLoopback(0)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	s.SiteCreated(t.Context(), docs)
	deadline := time.Now().Add(5 * time.Second)
	for len(r.received()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	if len(r.received()) != 1 {
		t.Fatalf("received %d: an event should wake the worker", len(r.received()))
	}
}
