// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package tls

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestTrustedProxies(t *testing.T) {
	trusted := TrustedProxies{
		netip.MustParsePrefix("127.0.0.1/32"),
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("fd00::/8"),
	}
	tests := []struct {
		name       string
		proxies    TrustedProxies
		remote     string
		headers    map[string][]string
		wantRemote string
		wantProto  string
		wantXFF    bool // X-Forwarded-For still present downstream
	}{
		{
			name:       "direct client, no headers",
			proxies:    trusted,
			remote:     "203.0.113.5:4711",
			wantRemote: "203.0.113.5:4711",
		},
		{
			name:       "untrusted peer cannot claim an address",
			proxies:    trusted,
			remote:     "203.0.113.5:4711",
			headers:    map[string][]string{"X-Forwarded-For": {"198.51.100.1"}, "X-Forwarded-Proto": {"https"}, "X-Real-Ip": {"198.51.100.1"}, "Forwarded": {"for=198.51.100.1"}, "X-Forwarded-Host": {"other.example"}},
			wantRemote: "203.0.113.5:4711",
		},
		{
			name:       "no trusted proxies configured",
			remote:     "127.0.0.1:4711",
			headers:    map[string][]string{"X-Forwarded-For": {"198.51.100.1"}, "X-Forwarded-Proto": {"https"}},
			wantRemote: "127.0.0.1:4711",
		},
		{
			name:       "trusted proxy names the client",
			proxies:    trusted,
			remote:     "127.0.0.1:4711",
			headers:    map[string][]string{"X-Forwarded-For": {"198.51.100.1"}, "X-Forwarded-Proto": {"https"}},
			wantRemote: "198.51.100.1:0",
			wantProto:  "https",
			wantXFF:    true,
		},
		{
			name:       "client-supplied entries left of the client are ignored",
			proxies:    trusted,
			remote:     "127.0.0.1:4711",
			headers:    map[string][]string{"X-Forwarded-For": {"6.6.6.6, 198.51.100.1"}},
			wantRemote: "198.51.100.1:0",
			wantXFF:    true,
		},
		{
			name:       "chain of trusted proxies across header lines",
			proxies:    trusted,
			remote:     "127.0.0.1:4711",
			headers:    map[string][]string{"X-Forwarded-For": {"6.6.6.6, 198.51.100.1", "10.1.2.3,10.9.9.9"}},
			wantRemote: "198.51.100.1:0",
			wantXFF:    true,
		},
		{
			name:       "unparseable hop stops at the last trusted address",
			proxies:    trusted,
			remote:     "127.0.0.1:4711",
			headers:    map[string][]string{"X-Forwarded-For": {"198.51.100.1, garbage, 10.1.2.3"}},
			wantRemote: "10.1.2.3:0",
			wantXFF:    true,
		},
		{
			name:       "every hop trusted keeps the farthest",
			proxies:    trusted,
			remote:     "127.0.0.1:4711",
			headers:    map[string][]string{"X-Forwarded-For": {"10.0.0.1, 10.0.0.2"}},
			wantRemote: "10.0.0.1:0",
			wantXFF:    true,
		},
		{
			name:       "trusted proxy without X-Forwarded-For",
			proxies:    trusted,
			remote:     "127.0.0.1:4711",
			wantRemote: "127.0.0.1:4711",
		},
		{
			name:       "IPv6 proxy and client",
			proxies:    trusted,
			remote:     "[fd00::1]:4711",
			headers:    map[string][]string{"X-Forwarded-For": {"2001:db8::7"}},
			wantRemote: "[2001:db8::7]:0",
			wantXFF:    true,
		},
		{
			name:       "IPv4-mapped peer matches an IPv4 range",
			proxies:    trusted,
			remote:     "[::ffff:10.1.1.1]:4711",
			headers:    map[string][]string{"X-Forwarded-For": {"198.51.100.1"}},
			wantRemote: "198.51.100.1:0",
			wantXFF:    true,
		},
		{
			name:       "bogus proto is dropped",
			proxies:    trusted,
			remote:     "127.0.0.1:4711",
			headers:    map[string][]string{"X-Forwarded-For": {"198.51.100.1"}, "X-Forwarded-Proto": {"javascript"}},
			wantRemote: "198.51.100.1:0",
			wantXFF:    true,
		},
		{
			name:       "repeated proto is dropped",
			proxies:    trusted,
			remote:     "127.0.0.1:4711",
			headers:    map[string][]string{"X-Forwarded-Proto": {"http", "https"}},
			wantRemote: "127.0.0.1:4711",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got *http.Request
			h := tt.proxies.Handler(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = r }))
			req := httptest.NewRequest(http.MethodGet, "http://docs.sites.example/", nil)
			req.RemoteAddr = tt.remote
			for k, vs := range tt.headers {
				for _, v := range vs {
					req.Header.Add(k, v)
				}
			}
			h.ServeHTTP(httptest.NewRecorder(), req)
			if got.RemoteAddr != tt.wantRemote {
				t.Errorf("RemoteAddr = %q, want %q", got.RemoteAddr, tt.wantRemote)
			}
			if p := got.Header.Get("X-Forwarded-Proto"); p != tt.wantProto {
				t.Errorf("X-Forwarded-Proto = %q, want %q", p, tt.wantProto)
			}
			if has := got.Header.Get("X-Forwarded-For") != ""; has != tt.wantXFF {
				t.Errorf("X-Forwarded-For present = %v, want %v", has, tt.wantXFF)
			}
			if got.Host != "docs.sites.example" {
				t.Errorf("Host = %q; forwarding headers must never change it", got.Host)
			}
			peer := netip.MustParseAddrPort(tt.remote).Addr().Unmap()
			if p, ok := PeerFrom(got.Context()); !ok || p.Addr != peer || p.Trusted != tt.proxies.Contains(peer) {
				t.Errorf("PeerFrom = %+v, %v; want the connecting peer %s, trusted %v", p, ok, peer, tt.proxies.Contains(peer))
			}
			if !tt.proxies.Contains(peer) {
				for _, h := range forwardedHeaders {
					if got.Header.Get(h) != "" {
						t.Errorf("%s from an untrusted peer reached the handler", h)
					}
				}
			}
		})
	}
}

func TestPeerFromWithoutAddress(t *testing.T) {
	var got *http.Request
	h := TrustedProxies{netip.MustParsePrefix("127.0.0.1/32")}.Handler(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = r }))
	req := httptest.NewRequest(http.MethodGet, "http://docs.sites.example/", nil)
	req.RemoteAddr = "@unix"
	req.Header.Set("X-Forwarded-For", "198.51.100.1")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if _, ok := PeerFrom(got.Context()); ok {
		t.Error("PeerFrom reported a peer for an unparseable address")
	}
	if got.Header.Get("X-Forwarded-For") != "" || got.RemoteAddr != "@unix" {
		t.Errorf("unparseable peer was trusted: RemoteAddr %q, X-Forwarded-For %q", got.RemoteAddr, got.Header.Get("X-Forwarded-For"))
	}
	if _, ok := PeerFrom(req.Context()); ok {
		t.Error("PeerFrom found a peer on a request Handler never saw")
	}
}
