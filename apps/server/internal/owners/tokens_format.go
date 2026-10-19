// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package owners

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
)

// The shared token format (docs/SPEC.md "Tokens"): wl_<public id>_<secret>.
// The public ID is 10 lowercase base32 characters that name the token in
// logs and listings; the secret is 256 random bits, base64url without
// padding. Only HashToken's output is ever stored.
const (
	tokenPrefix    = "wl_"
	publicIDLen    = 10
	publicIDChars  = "abcdefghijklmnopqrstuvwxyz234567"
	secretBytes    = 32
	tokenSecretLen = 43 // base64.RawURLEncoding.EncodedLen(secretBytes)
	tokenLen       = len(tokenPrefix) + publicIDLen + 1 + tokenSecretLen
)

// NewToken returns a new random token and its public ID.
func NewToken() (token, publicID string) {
	b := make([]byte, publicIDLen)
	_, _ = rand.Read(b) // crypto/rand.Read never fails since Go 1.24.
	for i := range b {
		b[i] = publicIDChars[b[i]&31]
	}
	publicID = string(b)
	secret := make([]byte, secretBytes)
	_, _ = rand.Read(secret)
	return tokenPrefix + publicID + "_" + base64.RawURLEncoding.EncodeToString(secret), publicID
}

// ParseToken returns the public ID of a well-formed token. It looks only at
// the shape, so it never says whether a token exists. The secret must be
// canonical base64url, so one secret has exactly one spelling.
func ParseToken(raw string) (publicID string, ok bool) {
	if len(raw) != tokenLen || !strings.HasPrefix(raw, tokenPrefix) || raw[len(tokenPrefix)+publicIDLen] != '_' {
		return "", false
	}
	publicID = raw[len(tokenPrefix) : len(tokenPrefix)+publicIDLen]
	for i := range len(publicID) {
		if !strings.ContainsRune(publicIDChars, rune(publicID[i])) {
			return "", false
		}
	}
	secret, err := base64.RawURLEncoding.Strict().DecodeString(raw[len(raw)-tokenSecretLen:])
	if err != nil || len(secret) != secretBytes {
		return "", false
	}
	return publicID, true
}

// HashToken is a token's stored form: HMAC-SHA-256 of the whole token keyed
// by the server secret, hex-encoded. Compare hashes with hmac.Equal.
func HashToken(secret []byte, token string) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(token))
	return hex.EncodeToString(m.Sum(nil))
}
