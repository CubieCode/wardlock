// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package oauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

// Errors returned by Identify, wrapped with what failed.
var (
	// ErrExchange means the provider refused the code: wrong PKCE
	// verifier, a replayed or expired code, bad client credentials.
	ErrExchange = errors.New("code exchange failed")
	// ErrIDToken means the ID token is missing or fails verification
	// (signature, issuer, audience, expiry).
	ErrIDToken = errors.New("invalid ID token")
	// ErrNonce means the ID token's nonce is not the flow's.
	ErrNonce = errors.New("ID token nonce mismatch")
	// ErrProvider means a provider API call failed.
	ErrProvider = errors.New("provider request failed")
)

// Timeout bounds every request to a provider.
const Timeout = 10 * time.Second

// NewHTTPClient returns the client providers use: bounded by Timeout,
// never following redirects off the API.
func NewHTTPClient() *http.Client {
	return &http.Client{
		Timeout: Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// pkceOptions are the authorisation URL parameters for an S256 challenge.
func pkceOptions(challenge string) []oauth2.AuthCodeOption {
	return []oauth2.AuthCodeOption{
		oauth2.SetAuthURLParam("code_challenge", challenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	}
}

// exchange redeems code with verifier. A provider's refusal is reported by
// its error code alone: oauth2's error text includes the response body.
func exchange(ctx context.Context, client *http.Client, cfg *oauth2.Config, code, verifier string) (*oauth2.Token, error) {
	tok, err := cfg.Exchange(context.WithValue(ctx, oauth2.HTTPClient, client), code, oauth2.VerifierOption(verifier))
	if err != nil {
		if re, ok := errors.AsType[*oauth2.RetrieveError](err); ok {
			code := re.ErrorCode
			if code == "" && re.Response != nil {
				code = re.Response.Status
			}
			return nil, fmt.Errorf("%w: %s", ErrExchange, safeText(code))
		}
		return nil, fmt.Errorf("%w: %w", ErrExchange, err)
	}
	return tok, nil
}

// safeText keeps a provider's error code printable and short for logs.
func safeText(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e {
			return -1
		}
		return r
	}, s)
	if len(s) > 64 {
		s = s[:64]
	}
	if s == "" {
		return "unknown error"
	}
	return s
}

// origin returns raw's scheme and host, or "" when it has none.
func origin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// normaliseEmail lowercases and trims an address; "" if it has no @.
func normaliseEmail(e string) string {
	e = strings.ToLower(strings.TrimSpace(e))
	if !strings.Contains(e, "@") {
		return ""
	}
	return e
}
