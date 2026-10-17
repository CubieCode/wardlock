// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package tls

import (
	"net"
	"net/http"
	"strconv"
	"strings"
)

// Redirect answers every request with a redirect to the same host, path
// and query over HTTPS on httpsPort (omitted when it is 443), with no body:
// 301 for GET and HEAD, 308 for other methods so the method and body are
// kept. A Host
// that is not a plain hostname or IP address gets 400 rather than a
// redirect somewhere unexpected.
func Redirect(httpsPort int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		host = strings.ToLower(strings.Trim(host, "[]"))
		if !redirectableHost(host) {
			http.Error(w, "Bad Request", http.StatusBadRequest)
			return
		}
		switch {
		case httpsPort != 443:
			host = net.JoinHostPort(host, strconv.Itoa(httpsPort))
		case strings.Contains(host, ":"):
			host = "[" + host + "]"
		}
		status := http.StatusMovedPermanently
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			status = http.StatusPermanentRedirect
		}
		// The Location is built from a host checked above and the request's
		// own path and query, on the same host: it cannot point elsewhere.
		w.Header().Set("Location", "https://"+host+r.URL.RequestURI())
		w.WriteHeader(status)
	})
}

// redirectableHost accepts a hostname of letters, digits, dots and
// hyphens, or an IP address (whose IPv6 form also has colons).
func redirectableHost(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	if isIP(host) {
		return true
	}
	for _, c := range host {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' && c != '.' {
			return false
		}
	}
	return true
}
