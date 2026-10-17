// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package access

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
)

// SessionCookieName is the site session cookie. The __Host- prefix makes
// browsers accept it only host-only, Secure and with Path=/, so a sibling
// site cannot toss or overwrite it; the edge reads no other cookie name.
// It differs from the dashboard's cookie, which never authenticates a site
// (and the reverse: the values are signed and hashed under other purposes).
const SessionCookieName = "__Host-wardlock_site"

// SessionLifetime is a site session's default lifetime (SPEC "Site
// sessions"); a method may cap it sooner, such as at a link's expiry.
const SessionLifetime = 7 * 24 * time.Hour

// Cookie is a site session cookie for the edge to set. The edge adds the
// fixed attributes: no Domain, Path=/, Secure, HttpOnly, SameSite=Lax.
type Cookie struct {
	Name    string
	Value   string
	Expires time.Time
}

// sessionClaims is what a site session cookie carries. The signature binds
// the token to the site, the policy and its version, so a cookie copied to
// another site, or kept across a policy change, fails before any lookup.
type sessionClaims struct {
	SiteID        string
	PolicyID      string
	PolicyVersion int
	ExpiresAt     time.Time // whole milliseconds
	Token         string    // 256 random bits, base64url; stored only as an HMAC
}

const (
	cookieVersion = "v1"
	// maxCookieBytes bounds the work done on a cookie before its MAC is
	// checked; real values are under 250 bytes.
	maxCookieBytes = 512
	maxIDBytes     = 64
	tokenBytes     = 32
	// Purposes keep the cookie MAC and the stored token hash apart from
	// each other and from every other use of the server key.
	purposeCookieMAC   = "site_session_cookie"
	purposeSessionHash = "site_session"
)

// keyed holds the server key and derives the cookie MAC and token hashes.
type keyed struct{ secret []byte }

func (k keyed) mac(purpose string, parts ...[]byte) []byte {
	m := hmac.New(sha256.New, k.secret)
	m.Write([]byte(purpose))
	for _, p := range parts {
		m.Write([]byte{0})
		m.Write(p)
	}
	return m.Sum(nil)
}

// tokenHash is the stored form of a session token.
func (k keyed) tokenHash(token string) string {
	return hex.EncodeToString(k.mac(purposeSessionHash, []byte(token)))
}

// encode signs c into a cookie value: base64url(payload) "." base64url(MAC).
func (k keyed) encode(c sessionClaims) string {
	payload := strings.Join([]string{
		cookieVersion, c.SiteID, c.PolicyID,
		strconv.Itoa(c.PolicyVersion), strconv.FormatInt(c.ExpiresAt.UnixMilli(), 10), c.Token,
	}, "|")
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." +
		base64.RawURLEncoding.EncodeToString(k.mac(purposeCookieMAC, []byte(payload)))
}

// decode verifies a cookie value's MAC in constant time and parses it. Any
// malformed, oversized or tampered value is false; it says nothing about
// expiry or revocation.
func (k keyed) decode(value string) (sessionClaims, bool) {
	// The decoder skips CR and LF; accept only the alphabet encode emits,
	// so one session has exactly one cookie value.
	if len(value) > maxCookieBytes || strings.ContainsFunc(value, func(r rune) bool { return !cookieChar(r) }) {
		return sessionClaims{}, false
	}
	enc, sig, ok := strings.Cut(value, ".")
	if !ok {
		return sessionClaims{}, false
	}
	payload, err := base64.RawURLEncoding.Strict().DecodeString(enc)
	if err != nil {
		return sessionClaims{}, false
	}
	mac, err := base64.RawURLEncoding.Strict().DecodeString(sig)
	if err != nil || !hmac.Equal(mac, k.mac(purposeCookieMAC, payload)) {
		return sessionClaims{}, false
	}
	f := strings.Split(string(payload), "|")
	if len(f) != 6 || f[0] != cookieVersion || !validID(f[1]) || !validID(f[2]) || !validToken(f[5]) {
		return sessionClaims{}, false
	}
	version, err := strconv.Atoi(f[3])
	if err != nil || version < 1 {
		return sessionClaims{}, false
	}
	exp, err := strconv.ParseInt(f[4], 10, 64)
	if err != nil || exp <= 0 {
		return sessionClaims{}, false
	}
	return sessionClaims{SiteID: f[1], PolicyID: f[2], PolicyVersion: version, ExpiresAt: time.UnixMilli(exp).UTC(), Token: f[5]}, true
}

func cookieChar(r rune) bool {
	return 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || '0' <= r && r <= '9' || r == '-' || r == '_' || r == '.'
}

func validID(s string) bool { return s != "" && len(s) <= maxIDBytes }

func validToken(s string) bool {
	b, err := base64.RawURLEncoding.Strict().DecodeString(s)
	return err == nil && len(b) == tokenBytes
}

func newToken() string {
	b := make([]byte, tokenBytes)
	_, _ = rand.Read(b) // crypto/rand.Read never fails since Go 1.24.
	return base64.RawURLEncoding.EncodeToString(b)
}
