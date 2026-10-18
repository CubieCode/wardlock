// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package access

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
)

// Share-link tokens use the shared format (docs/SPEC.md "Tokens"):
// wl_<public id>_<secret>, a 10-character lowercase base32 public ID and a
// 256-bit secret, base64url without padding. Only linkTokenHash's output is
// stored. API tokens (internal/owners) use the same format in another
// table; neither package accepts the other's tokens.
const (
	linkTokenPrefix   = "wl_"
	linkPublicIDLen   = 10
	linkPublicIDChars = "abcdefghijklmnopqrstuvwxyz234567"
	linkSecretBytes   = 32
	linkSecretLen     = 43 // base64.RawURLEncoding.EncodedLen(linkSecretBytes)
	linkTokenLen      = len(linkTokenPrefix) + linkPublicIDLen + 1 + linkSecretLen
)

// newLinkToken returns a new random token and its public ID.
func newLinkToken() (token, publicID string) {
	b := make([]byte, linkPublicIDLen)
	_, _ = rand.Read(b) // crypto/rand.Read never fails since Go 1.24.
	for i := range b {
		b[i] = linkPublicIDChars[b[i]&31]
	}
	return withSecret(string(b)), string(b)
}

// withSecret returns a new token for publicID with a fresh secret, as
// rotation issues.
func withSecret(publicID string) string {
	secret := make([]byte, linkSecretBytes)
	_, _ = rand.Read(secret)
	return linkTokenPrefix + publicID + "_" + base64.RawURLEncoding.EncodeToString(secret)
}

// parseLinkToken returns the public ID of a well-formed token. It looks only
// at the shape, so it never says whether a token exists. The secret must be
// canonical base64url, so one secret has exactly one spelling.
func parseLinkToken(raw string) (publicID string, ok bool) {
	if len(raw) != linkTokenLen || !strings.HasPrefix(raw, linkTokenPrefix) || raw[len(linkTokenPrefix)+linkPublicIDLen] != '_' {
		return "", false
	}
	publicID = raw[len(linkTokenPrefix) : len(linkTokenPrefix)+linkPublicIDLen]
	if !validPublicID(publicID) {
		return "", false
	}
	secret, err := base64.RawURLEncoding.Strict().DecodeString(raw[len(raw)-linkSecretLen:])
	if err != nil || len(secret) != linkSecretBytes {
		return "", false
	}
	return publicID, true
}

// validPublicID reports whether s has the shape of a public ID.
func validPublicID(s string) bool {
	if len(s) != linkPublicIDLen {
		return false
	}
	for i := range len(s) {
		if !strings.ContainsRune(linkPublicIDChars, rune(s[i])) {
			return false
		}
	}
	return true
}

// linkTokenHash is a token's stored form: HMAC-SHA-256 of the whole token
// keyed by the server key, hex-encoded, as SPEC "Tokens" fixes for every
// token. Compare hashes with hmac.Equal.
func (k keyed) linkTokenHash(token string) string {
	m := hmac.New(sha256.New, k.secret)
	m.Write([]byte(token))
	return hex.EncodeToString(m.Sum(nil))
}
