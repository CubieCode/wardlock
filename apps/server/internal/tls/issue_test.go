// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package tls

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/caddyserver/certmagic"
	"github.com/letsencrypt/pebble/v2/ca"
	"github.com/letsencrypt/pebble/v2/db"
	"github.com/letsencrypt/pebble/v2/va"
	"github.com/letsencrypt/pebble/v2/wfe"
	"github.com/libdns/libdns"
	"github.com/miekg/dns"
)

// TestIssuance runs automatic TLS end to end against Pebble, Let's
// Encrypt's test CA, in process: no Docker, network or real DNS. A fake
// DNS server is authoritative for the synthetic zone sites.test and
// resolves every name to 127.0.0.1; a fake libdns provider writes the
// DNS-01 TXT records into it. Pebble validates HTTP-01 and TLS-ALPN-01
// against this test's own listeners.
func TestIssuance(t *testing.T) {
	if testing.Short() {
		t.Skip("issues certificates from an in-process ACME CA")
	}
	t.Setenv("PEBBLE_VA_NOSLEEP", "1")
	t.Setenv("PEBBLE_WFE_NONCEREJECT", "0")

	zone := newFakeZone(t, "sites.test.")
	httpLn := listen(t)
	httpsLn := listen(t)
	acme, roots := startPebble(t, zone.addr, port(httpLn), port(httpsLn))

	domains := &fakeDomains{active: map[string]bool{"docs.customer.test": true}}
	var logs syncBuffer
	m, err := New(Config{
		BaseDomain:    "sites.test",
		DashboardHost: "wardlock.example.test",
		CA:            acme.URL + "/dir",
		CARoots:       roots.acme,
		DNS:           zone,
		Storage:       &certmagic.FileStorage{Path: t.TempDir()},
		Domains:       domains,
		HTTPPort:      port(httpLn),
		HTTPSPort:     port(httpsLn),
		Logger:        slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		dnsResolvers:  []string{zone.addr},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)

	site := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "site "+r.Host) })
	httpsSrv := &http.Server{Handler: site, TLSConfig: m.TLSConfig(), ReadHeaderTimeout: 5 * time.Second, ErrorLog: log.New(io.Discard, "", 0)}
	httpSrv := &http.Server{Handler: m.HTTPChallenge(Redirect(port(httpsLn))), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = httpsSrv.ServeTLS(httpsLn, "", "") }()
	go func() { _ = httpSrv.Serve(httpLn) }()
	t.Cleanup(func() { _ = httpsSrv.Close(); _ = httpSrv.Close() })

	if got, want := m.Names(), []string{"*.sites.test", "wardlock.example.test"}; !slices.Equal(got, want) {
		t.Fatalf("Names = %v, want %v", got, want)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if err := m.Manage(ctx); err != nil {
		t.Fatal(err)
	}

	handshake := func(name string) (*x509.Certificate, error) {
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}, "tcp", httpsLn.Addr().String(),
			&tls.Config{ServerName: name, RootCAs: roots.issued, MinVersion: tls.VersionTLS12})
		if err != nil {
			return nil, err
		}
		defer func() { _ = conn.Close() }()
		return conn.ConnectionState().PeerCertificates[0], nil
	}
	// The managed certificates are obtained in the background.
	waitFor := func(t *testing.T, name string) *x509.Certificate {
		t.Helper()
		var lastErr error
		for ctx.Err() == nil {
			cert, err := handshake(name)
			if err == nil {
				return cert
			}
			lastErr = err
			select {
			case <-ctx.Done():
			case <-time.After(250 * time.Millisecond):
			}
		}
		t.Fatalf("no certificate for %s: %v\nlogs:\n%s", name, lastErr, logs.String())
		return nil
	}

	t.Run("wildcard over DNS-01 covers sites and the auth origin", func(t *testing.T) {
		cert := waitFor(t, "docs.sites.test")
		if !slices.Contains(cert.DNSNames, "*.sites.test") {
			t.Fatalf("certificate names = %v, want the wildcard", cert.DNSNames)
		}
		if _, err := handshake("auth.sites.test"); err != nil {
			t.Fatalf("auth origin: %v", err)
		}
		if !zone.sawTXT("_acme-challenge.sites.test.") {
			t.Fatal("no DNS-01 record was presented for the wildcard")
		}
		if zone.liveTXT() != 0 {
			t.Fatal("DNS-01 records were not cleaned up")
		}
	})
	t.Run("dashboard host outside the zone over HTTP-01 or TLS-ALPN-01", func(t *testing.T) {
		cert := waitFor(t, "wardlock.example.test")
		if !slices.Equal(cert.DNSNames, []string{"wardlock.example.test"}) {
			t.Fatalf("certificate names = %v", cert.DNSNames)
		}
	})
	t.Run("on demand for an active custom domain", func(t *testing.T) {
		cert, err := handshake("docs.customer.test")
		if err != nil {
			t.Fatalf("handshake: %v", err)
		}
		if !slices.Equal(cert.DNSNames, []string{"docs.customer.test"}) {
			t.Fatalf("certificate names = %v", cert.DNSNames)
		}
	})
	t.Run("denied names get no certificate", func(t *testing.T) {
		for _, name := range []string{"unknown.customer.test", "nested.docs.sites.test"} {
			if _, err := handshake(name); err == nil {
				t.Errorf("handshake for %s succeeded; it must be denied", name)
			}
		}
		if !slices.Contains(domains.seen(), "unknown.customer.test") {
			t.Error("the decision did not consult Domains for an unknown custom domain")
		}
		if slices.Contains(domains.seen(), "nested.docs.sites.test") {
			t.Error("a name under the base domain reached Domains")
		}
	})
	t.Run("plain HTTP redirects to HTTPS", func(t *testing.T) {
		client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+httpLn.Addr().String()+"/a?b=1", nil)
		req.Host = "docs.sites.test"
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		want := "https://docs.sites.test:" + strconv.Itoa(port(httpsLn)) + "/a?b=1"
		if res.StatusCode != http.StatusMovedPermanently || res.Header.Get("Location") != want {
			t.Fatalf("got %d %q, want 301 %q", res.StatusCode, res.Header.Get("Location"), want)
		}
	})
}

// startPebble serves Pebble's ACME API over HTTPS. It returns the server,
// the root for its API certificate and the root its certificates chain to.
func startPebble(t *testing.T, resolver string, httpPort, tlsPort int) (*httptest.Server, struct{ acme, issued *x509.CertPool }) {
	t.Helper()
	logger := log.New(io.Discard, "", 0)
	store := db.NewMemoryStore()
	authority := ca.New(logger, store, "", "ecdsa", 0, 1, map[string]ca.Profile{"default": {Description: "test"}})
	validator := va.New(logger, httpPort, tlsPort, false, resolver, store)
	front := wfe.New(logger, store, validator, authority, []string{"pebble.letsencrypt.org"}, false, false, 1, 1)
	srv := httptest.NewTLSServer(front.Handler())
	t.Cleanup(srv.Close)

	var roots struct{ acme, issued *x509.CertPool }
	roots.acme = x509.NewCertPool()
	roots.acme.AddCert(srv.Certificate())
	roots.issued = x509.NewCertPool()
	roots.issued.AddCert(authority.GetRootCert(0).Cert)
	return srv, roots
}

// fakeZone is an authoritative DNS server for one zone and a libdns
// provider that edits it. Every A query answers 127.0.0.1.
type fakeZone struct {
	zone string
	addr string
	mu   sync.Mutex
	txt  map[string][]string
	seen map[string]bool
}

func newFakeZone(t *testing.T, zone string) *fakeZone {
	t.Helper()
	z := &fakeZone{zone: zone, txt: map[string][]string{}, seen: map[string]bool{}}
	// UDP and TCP on the same port: certmagic asks over UDP, Pebble over TCP.
	var udp net.PacketConn
	var tcp net.Listener
	for range 20 {
		var err error
		if tcp, err = net.Listen("tcp", "127.0.0.1:0"); err != nil {
			t.Fatal(err)
		}
		if udp, err = net.ListenPacket("udp", tcp.Addr().String()); err == nil {
			break
		}
		_ = tcp.Close()
		udp = nil
	}
	if udp == nil {
		t.Fatal("no free port for UDP and TCP DNS")
	}
	z.addr = tcp.Addr().String()
	for _, srv := range []*dns.Server{{PacketConn: udp, Handler: z}, {Listener: tcp, Handler: z}} {
		go func() { _ = srv.ActivateAndServe() }()
		t.Cleanup(func() { _ = srv.Shutdown() })
	}
	return z
}

func (z *fakeZone) ServeDNS(w dns.ResponseWriter, req *dns.Msg) {
	m := new(dns.Msg)
	m.SetReply(req)
	m.Authoritative = true
	q := req.Question[0]
	name := strings.ToLower(q.Name)
	hdr := dns.RR_Header{Name: q.Name, Class: dns.ClassINET, Ttl: 0}
	switch q.Qtype {
	case dns.TypeSOA:
		if name == z.zone {
			hdr.Rrtype = dns.TypeSOA
			m.Answer = append(m.Answer, &dns.SOA{Hdr: hdr, Ns: "ns." + z.zone, Mbox: "hostmaster." + z.zone, Serial: 1, Refresh: 60, Retry: 60, Expire: 60, Minttl: 0})
		}
	case dns.TypeA:
		hdr.Rrtype = dns.TypeA
		m.Answer = append(m.Answer, &dns.A{Hdr: hdr, A: net.IPv4(127, 0, 0, 1)})
	case dns.TypeTXT:
		z.mu.Lock()
		for _, v := range z.txt[name] {
			hdr.Rrtype = dns.TypeTXT
			m.Answer = append(m.Answer, &dns.TXT{Hdr: hdr, Txt: []string{v}})
		}
		z.mu.Unlock()
	}
	_ = w.WriteMsg(m)
}

func (z *fakeZone) AppendRecords(_ context.Context, zone string, recs []libdns.Record) ([]libdns.Record, error) {
	if zone != z.zone {
		return nil, errors.New("unknown zone " + zone)
	}
	z.mu.Lock()
	defer z.mu.Unlock()
	for _, r := range recs {
		rr := r.RR()
		name := strings.ToLower(libdns.AbsoluteName(rr.Name, zone))
		z.txt[name] = append(z.txt[name], rr.Data)
		z.seen[name] = true
	}
	return recs, nil
}

func (z *fakeZone) DeleteRecords(_ context.Context, zone string, recs []libdns.Record) ([]libdns.Record, error) {
	z.mu.Lock()
	defer z.mu.Unlock()
	for _, r := range recs {
		rr := r.RR()
		name := strings.ToLower(libdns.AbsoluteName(rr.Name, zone))
		z.txt[name] = slices.DeleteFunc(z.txt[name], func(v string) bool { return v == rr.Data })
	}
	return recs, nil
}

func (z *fakeZone) sawTXT(name string) bool {
	z.mu.Lock()
	defer z.mu.Unlock()
	return z.seen[name]
}

func (z *fakeZone) liveTXT() int {
	z.mu.Lock()
	defer z.mu.Unlock()
	n := 0
	for _, vs := range z.txt {
		n += len(vs)
	}
	return n
}

type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func listen(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return ln
}

func port(ln net.Listener) int { return ln.Addr().(*net.TCPAddr).Port }
