// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/oauth2"

	"wardlock.nz/server/internal/access"
)

// GitHub's public endpoints.
const (
	GitHubAuthURL     = "https://github.com/login/oauth/authorize"
	GitHubExchangeURL = "https://github.com/login/oauth/access_token"
	GitHubAPIURL      = "https://api.github.com"
)

// GitHubConfig configures the GitHub provider. The endpoint URLs default
// to GitHub's; tests point them at a fake.
type GitHubConfig struct {
	ClientID     string
	ClientSecret string
	AuthURL      string
	TokenURL     string
	APIURL       string
	HTTPClient   *http.Client
}

// GitHub signs visitors in with a GitHub OAuth app. Identities carry the
// account's verified emails (user:email) and, when the policy lists
// organisations, which of them the account is an active member of
// (read:org, requested only then).
type GitHub struct {
	cfg    oauth2.Config
	api    string
	client *http.Client
}

var _ access.Provider = (*GitHub)(nil)

// NewGitHub returns the GitHub provider.
func NewGitHub(c GitHubConfig) *GitHub {
	g := &GitHub{
		cfg: oauth2.Config{
			ClientID: c.ClientID, ClientSecret: c.ClientSecret,
			Endpoint: oauth2.Endpoint{
				AuthURL: or(c.AuthURL, GitHubAuthURL), TokenURL: or(c.TokenURL, GitHubExchangeURL),
				AuthStyle: oauth2.AuthStyleInParams,
			},
		},
		api:    strings.TrimSuffix(or(c.APIURL, GitHubAPIURL), "/"),
		client: c.HTTPClient,
	}
	if g.client == nil {
		g.client = NewHTTPClient()
	}
	return g
}

func or(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func (*GitHub) ID() string   { return "github" }
func (*GitHub) Name() string { return "GitHub" }
func (*GitHub) GitHub() bool { return true }

// AuthCodeURL implements access.Provider.
func (g *GitHub) AuthCodeURL(_ context.Context, a access.AuthRequest) (string, error) {
	cfg := g.cfg
	cfg.RedirectURL = a.RedirectURI
	cfg.Scopes = []string{"user:email"}
	if a.Orgs {
		cfg.Scopes = append(cfg.Scopes, "read:org")
	}
	opts := append(pkceOptions(a.Challenge), oauth2.SetAuthURLParam("allow_signup", "false"))
	return cfg.AuthCodeURL(a.State, opts...), nil
}

// Identify implements access.Provider.
func (g *GitHub) Identify(ctx context.Context, x access.Exchange) (access.Identity, error) {
	cfg := g.cfg
	cfg.RedirectURL = x.RedirectURI
	tok, err := exchange(ctx, g.client, &cfg, x.Code, x.Verifier)
	if err != nil {
		return access.Identity{}, err
	}
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if status, err := g.get(ctx, tok, "/user/emails", &emails); err != nil {
		return access.Identity{}, err
	} else if status != http.StatusOK {
		return access.Identity{}, fmt.Errorf("%w: GitHub emails: status %d", ErrProvider, status)
	}
	var id access.Identity
	for _, primary := range []bool{true, false} {
		for _, e := range emails {
			if e.Verified && e.Primary == primary {
				if addr := normaliseEmail(e.Email); addr != "" {
					id.Emails = append(id.Emails, addr)
				}
			}
		}
	}
	for _, org := range x.Orgs {
		var m struct {
			State        string `json:"state"`
			Organization struct {
				Login string `json:"login"`
			} `json:"organization"`
		}
		status, err := g.get(ctx, tok, "/user/memberships/orgs/"+url.PathEscape(org), &m)
		if err != nil {
			return access.Identity{}, err
		}
		// 404 and 403 mean not a member, or the organisation has not
		// approved the app: either way no membership to count.
		if status == http.StatusOK && m.State == "active" && strings.EqualFold(m.Organization.Login, org) {
			id.Orgs = append(id.Orgs, strings.ToLower(org))
		}
	}
	return id, nil
}

// maxAPIBody bounds what one GitHub API answer may hold.
const maxAPIBody = 1 << 20

// get calls the GitHub API and decodes a 200 answer into v.
func (g *GitHub) get(ctx context.Context, tok *oauth2.Token, path string, v any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.api+path, nil)
	if err != nil {
		return 0, fmt.Errorf("%w: %w", ErrProvider, err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	res, err := g.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("%w: GitHub API: %w", ErrProvider, err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, maxAPIBody))
		return res.StatusCode, nil
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, maxAPIBody)).Decode(v); err != nil {
		return 0, fmt.Errorf("%w: GitHub API %s: %w", ErrProvider, path, err)
	}
	return res.StatusCode, nil
}
