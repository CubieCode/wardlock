// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package tls

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// forwardedHeaders are the request headers a proxy uses to describe the
// original request. Only a trusted proxy may set them.
var forwardedHeaders = []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Proto", "X-Forwarded-Host", "X-Real-Ip"}

// TrustedProxies is the set of peer addresses whose X-Forwarded-* headers
// are believed (WARDLOCK_TRUSTED_PROXIES).
type TrustedProxies []netip.Prefix

// Contains reports whether addr belongs to a trusted proxy.
func (t TrustedProxies) Contains(addr netip.Addr) bool {
	addr = addr.Unmap()
	for _, p := range t {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

type peerKey struct{}

// Peer is the connection's immediate peer, before any X-Forwarded-For
// rewrite, and whether it is a trusted proxy.
type Peer struct {
	Addr    netip.Addr
	Trusted bool
}

// PeerFrom returns the Peer that Handler recorded for a request; ok is
// false when Handler did not run or the peer address was unparseable. Use
// it where the proxy itself must be identified, such as
// /.wardlock/tls-ask, since RemoteAddr is the client behind it.
func PeerFrom(ctx context.Context) (p Peer, ok bool) {
	p, ok = ctx.Value(peerKey{}).(Peer)
	return p, ok
}

// Handler makes r.RemoteAddr the client's address for everything after it.
// A request from a trusted proxy takes its client from X-Forwarded-For,
// read right to left past every trusted hop, so a client cannot choose its
// own address by sending the header; X-Forwarded-Proto is kept only when it
// is http or https. A request from any other peer has every forwarding
// header removed, so nothing downstream can mistake a client's claim for
// the proxy's. Either way the original peer stays available through
// PeerFrom. RemoteAddr keeps port 0 when it is rewritten, since the
// client's port is unknown.
func (t TrustedProxies) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peer, ok := peerAddr(r.RemoteAddr)
		trusted := ok && t.Contains(peer)
		if ok {
			r = r.WithContext(context.WithValue(r.Context(), peerKey{}, Peer{Addr: peer, Trusted: trusted}))
		}
		if !trusted {
			for _, h := range forwardedHeaders {
				r.Header.Del(h)
			}
			next.ServeHTTP(w, r)
			return
		}
		if client := t.clientAddr(peer, r.Header.Values("X-Forwarded-For")); client != peer {
			r.RemoteAddr = net.JoinHostPort(client.String(), "0")
		}
		if proto := r.Header.Values("X-Forwarded-Proto"); len(proto) != 1 || (proto[0] != "http" && proto[0] != "https") {
			r.Header.Del("X-Forwarded-Proto")
		}
		next.ServeHTTP(w, r)
	})
}

// clientAddr walks the X-Forwarded-For chain from the nearest hop. It
// stops at the first address that is not a trusted proxy (the client) or
// at the first entry it cannot parse, keeping the last trusted hop then,
// since nothing beyond it can be believed.
func (t TrustedProxies) clientAddr(peer netip.Addr, values []string) netip.Addr {
	var hops []string
	for _, v := range values {
		hops = append(hops, strings.Split(v, ",")...)
	}
	client := peer.Unmap()
	for i := len(hops) - 1; i >= 0; i-- {
		addr, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			break
		}
		client = addr.Unmap()
		if !t.Contains(client) {
			break
		}
	}
	return client
}

func peerAddr(remote string) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}
