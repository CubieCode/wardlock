// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package notify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"wardlock.nz/server/internal/mail"
	"wardlock.nz/server/internal/store"
)

// The classes of a failed attempt, the only detail kept: an error's text
// can carry the URL, which may hold the receiver's secret.
const (
	failStatus     = "status"
	failTimeout    = "timeout"
	failDNS        = "dns"
	failTLS        = "tls"
	failConnection = "connection"
	failRefused    = "refused_address"
)

// webhookDisabled is the email saying a webhook was turned off: it cannot
// be turned off and passes the hourly cap.
const webhookDisabled = "webhook_disabled"

func signature(secret string, t int64, body string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(strconv.FormatInt(t, 10) + "." + body))
	return fmt.Sprintf("t=%d,v1=%s", t, hex.EncodeToString(m.Sum(nil)))
}

func (s *Service) wakeWebhooks() {
	select {
	case s.webhookWake <- struct{}{}:
	default:
	}
}

// runWebhooks delivers until ctx ends. It has its own loop so that receivers
// taking their full timeout never delay email.
func (s *Service) runWebhooks(ctx context.Context) {
	t := time.NewTicker(DeliverInterval)
	defer t.Stop()
	for {
		s.DeliverWebhooks(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.webhookWake:
		}
	}
}

// DeliverWebhooks attempts every webhook delivery that is due.
func (s *Service) DeliverWebhooks(ctx context.Context) {
	rows, err := s.c.Store.Due(ctx, store.NotificationWebhook, s.now(), batch)
	if err != nil {
		s.logErr(ctx, "list due webhook deliveries", err)
		return
	}
	var wg sync.WaitGroup
	slots := make(chan struct{}, webhookWorkers)
	for _, e := range rows {
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		case slots <- struct{}{}:
		}
		wg.Go(func() {
			defer func() { <-slots }()
			s.attempt(ctx, e)
		})
	}
	wg.Wait()
}

// attempt tries one due delivery and records how it went.
func (s *Service) attempt(ctx context.Context, e store.OutboxEntry) {
	// Bookkeeping after an attempt survives a shutdown, as for email.
	done := context.WithoutCancel(ctx)
	settle := func(status store.NotificationStatus) {
		s.logErr(ctx, "settle webhook delivery", s.c.Store.Settle(done, e.ID, status, s.now()))
	}
	drop := func() {
		s.logErr(ctx, "drop webhook delivery", s.c.Webhooks.Drop(done, e.ID, s.now()))
		settle(store.NotificationDropped)
	}
	send, err := s.c.Webhooks.ToSend(ctx, e.ID)
	if errors.Is(err, store.ErrNotFound) {
		settle(store.NotificationDropped) // the webhook has gone
		return
	}
	if err != nil {
		s.logErr(ctx, "read webhook delivery", err)
		return
	}
	to, err := s.c.Store.Recipient(ctx, e.OwnerID)
	if err != nil {
		s.logErr(ctx, "read webhook owner", err)
		return
	}
	if send.Disabled || to.Suspended {
		drop()
		return
	}
	if err := s.c.Store.Claim(ctx, e.ID); err != nil {
		s.logErr(ctx, "claim webhook delivery", err)
		return
	}
	began := time.Now()
	code, class := s.post(ctx, e.Event, send)
	if class != "" && ctx.Err() != nil {
		// Shutdown cut it short, which says nothing about the receiver.
		s.logErr(ctx, "reschedule webhook delivery", s.c.Store.Reschedule(done, e.ID, s.now()))
		return
	}
	now := s.now()
	a := store.WebhookAttempt{
		DeliveryID: e.ID, WebhookID: send.WebhookID, Status: store.WebhookDeliveryPending, OK: class == "",
		ResponseCode: code, Duration: time.Since(began), Error: class, At: now,
	}
	since := send.FailingSince
	if since.IsZero() {
		since = now
	}
	disable := !a.OK && now.Sub(since) >= webhookDisableAfter
	switch {
	case a.OK:
		a.Status = store.WebhookDeliverySucceeded
	case disable, now.Sub(e.CreatedAt) >= webhookRetryFor:
		a.Status = store.WebhookDeliveryFailed
	}
	s.logErr(ctx, "record webhook attempt", s.c.Webhooks.RecordAttempt(done, a, keepDeliveries))
	switch a.Status {
	case store.WebhookDeliverySucceeded:
		settle(store.NotificationSent)
		return
	case store.WebhookDeliveryFailed:
		settle(store.NotificationFailed)
	default:
		next := now.Add(backoff(e.Attempts + 1))
		s.logErr(ctx, "reschedule webhook delivery", s.c.Store.Reschedule(done, e.ID, next))
	}
	s.c.Log.LogAttrs(ctx, slog.LevelWarn, "webhook delivery failed",
		slog.String("id", e.ID), slog.String("webhook_id", send.WebhookID), slog.String("event", e.Event),
		slog.Int64("attempts", e.Attempts+1), slog.Int("response_code", code), slog.String("error", class))
	if disable {
		s.disable(done, e.OwnerID, send, now)
	}
}

// post makes one attempt and returns the response code, if any, and the
// class of failure, "" for a 2xx answer.
func (s *Service) post(ctx context.Context, event string, send store.WebhookSend) (int, string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, send.URL, strings.NewReader(send.Body))
	if err != nil {
		return 0, failConnection
	}
	secret := s.signingSecret(send.WebhookID, send.SecretSalt)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Wardlock-Webhook")
	req.Header.Set("Wardlock-Event", event)
	req.Header.Set("Wardlock-Signature", signature(secret, s.c.Now().Unix(), send.Body))
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, failureClass(err)
	}
	defer func() { _ = resp.Body.Close() }()
	// Nothing from the body is kept. Reading a little lets a small answer
	// finish cleanly; the cap ends a large one, and closing the connection
	// drops the rest unread.
	_, _ = io.CopyN(io.Discard, resp.Body, maxWebhookResponse)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return resp.StatusCode, failStatus
	}
	return resp.StatusCode, ""
}

func failureClass(err error) string {
	var (
		dns  *net.DNSError
		cert *tls.CertificateVerificationError
		rec  tls.RecordHeaderError
		ue   *url.Error
	)
	switch {
	case errors.Is(err, errRefusedAddress):
		return failRefused
	case errors.As(err, &dns):
		return failDNS
	case errors.Is(err, os.ErrDeadlineExceeded), errors.Is(err, context.DeadlineExceeded),
		errors.As(err, &ue) && ue.Timeout():
		return failTimeout
	case errors.As(err, &cert), errors.As(err, &rec):
		return failTLS
	}
	return failConnection
}

// disable turns off a webhook that keeps failing and queues the mandatory
// email to its owner with it, keyed by the moment so it goes once for each
// time it is disabled. The email names only the host: the URL's path may be
// the receiver's secret.
func (s *Service) disable(ctx context.Context, ownerID string, send store.WebhookSend, at time.Time) {
	host := "your endpoint"
	if u, err := url.Parse(send.URL); err == nil && u.Hostname() != "" {
		host = u.Hostname()
	}
	notice := mail.Notice{
		Subject: "A webhook has been turned off",
		Lead:    "Your webhook to " + host + " has failed for 3 days, so Wardlock has turned it off and dropped what was queued for it.",
		Note:    "Once the endpoint works again, turn the webhook back on to receive new events.",
		Action:  "Review your webhooks",
		URL:     webhooksPath,
	}
	e, err := s.entry(key(webhookDisabled, send.WebhookID, at), ownerID, webhookDisabled, emailPayload{Notice: &notice})
	if err != nil {
		s.logErr(ctx, "build webhook notice", err)
		return
	}
	if err := s.c.Webhooks.Disable(ctx, send.WebhookID, at, e); err != nil {
		s.logErr(ctx, "disable webhook", err)
		return
	}
	s.c.Log.LogAttrs(ctx, slog.LevelWarn, "webhook disabled after failing", slog.String("webhook_id", send.WebhookID))
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
