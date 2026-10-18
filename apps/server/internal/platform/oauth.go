// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package platform

import (
	"fmt"
	"net/url"
)

// OAuthConfig is the operator's OAuth and OIDC providers (A2.5;
// docs/DEPLOYMENT.md "OAuth and OIDC"). A provider is configured when its
// client ID is set; its secret is then required. None configured leaves
// the method unavailable.
type OAuthConfig struct {
	GitHubClientID     string
	GitHubClientSecret Secret
	GoogleClientID     string
	GoogleClientSecret Secret
	// OIDCIssuer is a generic OpenID Connect issuer URL; OIDCName labels
	// its button on login pages.
	OIDCIssuer       string
	OIDCClientID     string
	OIDCClientSecret Secret
	OIDCName         string
}

// AuthHost is the auth origin every provider redirects to: auth.<base
// domain>. Site names cannot be "auth".
func (c Config) AuthHost() string { return "auth." + c.BaseDomain }

// loadOAuth reads the WARDLOCK_GITHUB_*, WARDLOCK_GOOGLE_* and
// WARDLOCK_OIDC_* variables.
func (c *Config) loadOAuth(get func(key, fallback string) string) error {
	if c.DashboardHost == c.AuthHost() {
		return fmt.Errorf("%w: WARDLOCK_DASHBOARD_HOST cannot be %s, the OAuth auth origin", ErrInvalidConfig, c.AuthHost())
	}
	o := OAuthConfig{
		GitHubClientID: get("WARDLOCK_GITHUB_CLIENT_ID", ""), GitHubClientSecret: Secret(get("WARDLOCK_GITHUB_CLIENT_SECRET", "")),
		GoogleClientID: get("WARDLOCK_GOOGLE_CLIENT_ID", ""), GoogleClientSecret: Secret(get("WARDLOCK_GOOGLE_CLIENT_SECRET", "")),
		OIDCIssuer: get("WARDLOCK_OIDC_ISSUER", ""), OIDCClientID: get("WARDLOCK_OIDC_CLIENT_ID", ""),
		OIDCClientSecret: Secret(get("WARDLOCK_OIDC_CLIENT_SECRET", "")), OIDCName: get("WARDLOCK_OIDC_NAME", "single sign-on"),
	}
	for _, p := range []struct {
		name   string
		id     string
		secret Secret
	}{
		{"GITHUB", o.GitHubClientID, o.GitHubClientSecret},
		{"GOOGLE", o.GoogleClientID, o.GoogleClientSecret},
		{"OIDC", o.OIDCClientID, o.OIDCClientSecret},
	} {
		if (p.id == "") != (p.secret == "") {
			return fmt.Errorf("%w: set both WARDLOCK_%s_CLIENT_ID and WARDLOCK_%s_CLIENT_SECRET, or neither", ErrInvalidConfig, p.name, p.name)
		}
	}
	if (o.OIDCIssuer == "") != (o.OIDCClientID == "") {
		return fmt.Errorf("%w: WARDLOCK_OIDC_ISSUER and WARDLOCK_OIDC_CLIENT_ID go together", ErrInvalidConfig)
	}
	if o.OIDCIssuer != "" {
		u, err := url.Parse(o.OIDCIssuer)
		local := u != nil && (isLocalHost(u.Hostname()) || u.Hostname() == "127.0.0.1")
		if err != nil || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil ||
			(u.Scheme != "https" && (u.Scheme != "http" || !local)) {
			return fmt.Errorf("%w: WARDLOCK_OIDC_ISSUER must be an https:// issuer URL (http only for localhost)", ErrInvalidConfig)
		}
	}
	if len(o.OIDCName) > 40 {
		return fmt.Errorf("%w: WARDLOCK_OIDC_NAME can be at most 40 characters", ErrInvalidConfig)
	}
	c.OAuth = o
	return nil
}
