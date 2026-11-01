// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"time"

	"wardlock.nz/server/internal/store/sqlcgen"
)

// Visibility says whether a policy lets anyone in.
type Visibility string

const (
	Public    Visibility = "public"
	Protected Visibility = "protected"
)

// Policy decides who may read a site or a path prefix. PathPrefix "/" is the
// site default. Version increases on every change.
type Policy struct {
	ID              string
	SiteID          string
	PathPrefix      string
	Visibility      Visibility
	LinksEnabled    bool
	PasswordEnabled bool
	EmailEnabled    bool
	OAuthEnabled    bool
	PasswordHash    string // Argon2id
	// Allowlist is the policy's one allowlist, shared by the email-code
	// and OAuth methods (A2.4).
	Allowlist Allowlist
	// KeepAllowlist, only in a Policy passed to Policies.Set, keeps the
	// stored policy's allowlist (read inside Set's transaction) instead of
	// Allowlist.
	KeepAllowlist bool
	// KeepGitHubOrgs, only in a Policy passed to Policies.Set, keeps the
	// stored policy's GitHub organisations while Allowlist replaces its
	// addresses and domains (A2.5).
	KeepGitHubOrgs bool
	Version        int
	// CreatedBy is who last changed it, as Deploy.CreatedBy (T7.1). Read
	// only: the repository records its actor.
	CreatedBy      string
	CreatedByToken string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Allowlist entry kinds, as the policy_allowlist.kind column spells them.
const (
	AllowEmail     = "email"
	AllowDomain    = "domain"
	AllowGitHubOrg = "github_org"
)

// Allowlist holds who an email-code or OAuth sign-in may be: normalised,
// sorted and distinct entries of each kind.
type Allowlist struct {
	// Emails are exact addresses, lower case.
	Emails []string
	// Domains are email domains, example.com (that domain only) or
	// *.example.com (its subdomains), lower-case A-labels.
	Domains []string
	// GitHubOrgs are GitHub organisation logins, lower case (A2.5).
	GitHubOrgs []string
}

// Equal reports whether a and b hold the same entries.
func (a Allowlist) Equal(b Allowlist) bool {
	same := func(x, y []string) bool {
		x, y = slices.Clone(x), slices.Clone(y)
		slices.Sort(x)
		slices.Sort(y)
		return slices.Equal(x, y)
	}
	return same(a.Emails, b.Emails) && same(a.Domains, b.Domains) && same(a.GitHubOrgs, b.GitHubOrgs)
}

// Len is the number of entries of every kind.
func (a Allowlist) Len() int { return len(a.Emails) + len(a.Domains) + len(a.GitHubOrgs) }

func (a Allowlist) entries() [][2]string {
	var out [][2]string
	for _, kv := range []struct {
		kind   string
		values []string
	}{{AllowEmail, a.Emails}, {AllowDomain, a.Domains}, {AllowGitHubOrg, a.GitHubOrgs}} {
		for _, v := range kv.values {
			out = append(out, [2]string{kv.kind, v})
		}
	}
	return out
}

// allowlistsFromRows groups rows by policy ID.
func allowlistsFromRows(rows []sqlcgen.PolicyAllowlist) map[string]Allowlist {
	out := map[string]Allowlist{}
	for _, row := range rows {
		a := out[row.PolicyID]
		switch row.Kind {
		case AllowEmail:
			a.Emails = append(a.Emails, row.Value)
		case AllowDomain:
			a.Domains = append(a.Domains, row.Value)
		case AllowGitHubOrg:
			a.GitHubOrgs = append(a.GitHubOrgs, row.Value)
		}
		out[row.PolicyID] = a
	}
	return out
}

// SameSettings reports whether p and q decide alike: the same visibility,
// methods, password hash and allowlist. Version, IDs and times are
// ignored.
func (p Policy) SameSettings(q Policy) bool {
	return p.Visibility == q.Visibility && p.LinksEnabled == q.LinksEnabled &&
		p.PasswordEnabled == q.PasswordEnabled && p.EmailEnabled == q.EmailEnabled &&
		p.OAuthEnabled == q.OAuthEnabled && p.PasswordHash == q.PasswordHash &&
		p.Allowlist.Equal(q.Allowlist)
}

// KeepPasswordHash, as a Policy's PasswordHash passed to Policies.Set, keeps
// the stored policy's password hash, read inside Set's transaction so a
// concurrent password change is never undone. With no stored password Set
// returns ErrNoPasswordToKeep. It is never a valid hash, so never stored.
const KeepPasswordHash = "keep:"

// ErrNoPasswordToKeep is returned by Policies.Set for KeepPasswordHash when
// the policy has no password enabled.
var ErrNoPasswordToKeep = errors.New("no password to keep")

// ErrEmailNeedsAllowlist is returned by Policies.Set when the email method
// would be enabled with no allowed addresses or domains, as can happen
// when KeepAllowlist finds none stored.
var ErrEmailNeedsAllowlist = errors.New("the email method needs allowed addresses or domains")

// ErrOAuthNeedsAllowlist is returned by Policies.Set when the OAuth method
// would be enabled with an empty allowlist (A2.5).
var ErrOAuthNeedsAllowlist = errors.New("the oauth method needs an allowlist")

// Policies stores site and path policies, one per site and prefix.
type Policies interface {
	// Create adds p to a site the actor can edit.
	Create(ctx context.Context, actor Actor, p Policy) error
	// ForSite returns every policy of siteID ordered by path prefix. It is
	// an instance-wide read for the edge and must never back an owner API
	// handler.
	ForSite(ctx context.Context, siteID string) ([]Policy, error)
	// EnsureDefault inserts p as siteID's default policy (PathPrefix "/",
	// Version 1, UpdatedAt = CreatedAt) unless the site has one already, in
	// which case it changes nothing. Instance-wide, like ForSite.
	EnsureDefault(ctx context.Context, p Policy) error
	// List returns up to limit of the policies of site siteID
	// ordered by path prefix, after afterPrefix ("" for the first page).
	List(ctx context.Context, actor Actor, siteID, afterPrefix string, limit int) ([]Policy, error)
	// Set stores p's settings as the policy for (p.SiteID, p.PathPrefix) on
	// a site the actor can edit, in one transaction. Without a policy for the
	// prefix it inserts p with Version 1. With one whose settings differ it
	// keeps that policy's ID and CreatedAt, takes p's settings and
	// UpdatedAt, and increments its Version, which ends every session bound
	// to the old version; a changed or removed password hash also revokes
	// the policy's password sessions. With identical settings it changes
	// nothing. A PasswordHash of KeepPasswordHash keeps the stored one (see
	// there), and so does KeepAllowlist for the allowlist; a changed
	// allowlist also revokes the policy's email and OAuth sessions. It
	// returns the stored policy.
	Set(ctx context.Context, actor Actor, p Policy) (Policy, error)
	// Delete removes policy id of site siteID together with the
	// site sessions bound to it.
	Delete(ctx context.Context, actor Actor, siteID, id string) error
}

// Policies returns the SQLite Policies repository.
func (db *DB) Policies() Policies { return policiesRepo{db} }

type policiesRepo struct{ db *DB }

func (r policiesRepo) Create(ctx context.Context, actor Actor, p Policy) error {
	ownerID, err := r.db.authorizeSite(ctx, actor, p.SiteID, RoleEditor)
	if err != nil {
		return err
	}
	by, token := actor.MadeBy()
	return r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
		if err := requireSite(ctx, q, ownerID, p.SiteID); err != nil {
			return err
		}
		if err := mapErr("create policy", q.CreatePolicy(ctx, sqlcgen.CreatePolicyParams{
			ID:              p.ID,
			SiteID:          p.SiteID,
			PathPrefix:      p.PathPrefix,
			Visibility:      string(p.Visibility),
			LinksEnabled:    boolInt(p.LinksEnabled),
			PasswordEnabled: boolInt(p.PasswordEnabled),
			EmailEnabled:    boolInt(p.EmailEnabled),
			OauthEnabled:    boolInt(p.OAuthEnabled),
			PasswordHash:    nullString(p.PasswordHash),
			Version:         int64(p.Version),
			CreatedAt:       ms(p.CreatedAt),
			UpdatedAt:       ms(p.UpdatedAt),
			CreatedBy:       nullString(by),
			CreatedByToken:  nullString(token),
		})); err != nil {
			return err
		}
		return addAllowlist(ctx, q, p)
	})
}

// addAllowlist stores p's allowlist entries.
func addAllowlist(ctx context.Context, q *sqlcgen.Queries, p Policy) error {
	for _, e := range p.Allowlist.entries() {
		if err := q.AddAllowlistEntry(ctx, sqlcgen.AddAllowlistEntryParams{
			PolicyID: p.ID, SiteID: p.SiteID, Kind: e[0], Value: e[1],
		}); err != nil {
			return mapErr("add allowlist entry", err)
		}
	}
	return nil
}

// withAllowlists attaches each policy's allowlist from rows.
func withAllowlists(ps []Policy, rows []sqlcgen.PolicyAllowlist) []Policy {
	lists := allowlistsFromRows(rows)
	for i := range ps {
		ps[i].Allowlist = lists[ps[i].ID]
	}
	return ps
}

func policyFromRow(row sqlcgen.Policy) Policy {
	return Policy{
		ID:              row.ID,
		SiteID:          row.SiteID,
		PathPrefix:      row.PathPrefix,
		Visibility:      Visibility(row.Visibility),
		LinksEnabled:    row.LinksEnabled != 0,
		PasswordEnabled: row.PasswordEnabled != 0,
		EmailEnabled:    row.EmailEnabled != 0,
		OAuthEnabled:    row.OauthEnabled != 0,
		PasswordHash:    row.PasswordHash.String,
		Version:         int(row.Version),
		CreatedAt:       fromMs(row.CreatedAt),
		UpdatedAt:       fromMs(row.UpdatedAt),
		CreatedBy:       row.CreatedBy.String,
		CreatedByToken:  row.CreatedByToken.String,
	}
}

func policiesFromRows(rows []sqlcgen.Policy) []Policy {
	out := make([]Policy, len(rows))
	for i, row := range rows {
		out[i] = policyFromRow(row)
	}
	return out
}

func (r policiesRepo) ForSite(ctx context.Context, siteID string) ([]Policy, error) {
	// Two reads, outside a transaction (which would take the write lock):
	// a concurrent Set notifies, so its caller drops what was read.
	rows, err := r.db.q.ListSitePolicies(ctx, siteID)
	if err != nil {
		return nil, mapErr("list site policies", err)
	}
	entries, err := r.db.q.ListSiteAllowlist(ctx, siteID)
	if err != nil {
		return nil, mapErr("list site allowlist", err)
	}
	return withAllowlists(policiesFromRows(rows), entries), nil
}

func (r policiesRepo) EnsureDefault(ctx context.Context, p Policy) error {
	return mapErr("ensure default policy", r.db.q.EnsureDefaultPolicy(ctx, sqlcgen.EnsureDefaultPolicyParams{
		ID:              p.ID,
		SiteID:          p.SiteID,
		Visibility:      string(p.Visibility),
		LinksEnabled:    boolInt(p.LinksEnabled),
		PasswordEnabled: boolInt(p.PasswordEnabled),
		EmailEnabled:    boolInt(p.EmailEnabled),
		OauthEnabled:    boolInt(p.OAuthEnabled),
		PasswordHash:    nullString(p.PasswordHash),
		CreatedAt:       ms(p.CreatedAt),
	}))
}

func (r policiesRepo) List(ctx context.Context, actor Actor, siteID, afterPrefix string, limit int) ([]Policy, error) {
	ownerID, err := r.db.authorizeSite(ctx, actor, siteID, RoleViewer)
	if errors.Is(err, ErrNotFound) {
		return []Policy{}, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := r.db.q.ListOwnerPolicies(ctx, sqlcgen.ListOwnerPoliciesParams{
		OwnerID:     ownerID,
		SiteID:      siteID,
		AfterPrefix: afterPrefix,
		RowLimit:    int64(limit),
	})
	if err != nil {
		return nil, mapErr("list policies", err)
	}
	entries, err := r.db.q.ListOwnerSiteAllowlist(ctx, sqlcgen.ListOwnerSiteAllowlistParams{OwnerID: ownerID, SiteID: siteID})
	if err != nil {
		return nil, mapErr("list allowlist", err)
	}
	return withAllowlists(policiesFromRows(rows), entries), nil
}

func (r policiesRepo) Set(ctx context.Context, actor Actor, p Policy) (Policy, error) {
	ownerID, err := r.db.authorizeSite(ctx, actor, p.SiteID, RoleEditor)
	if err != nil {
		return Policy{}, err
	}
	var out Policy
	by, token := actor.MadeBy()
	p.CreatedBy, p.CreatedByToken = by, token
	err = r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
		if err := requireSite(ctx, q, ownerID, p.SiteID); err != nil {
			return err
		}
		row, err := q.GetPolicyByPrefix(ctx, sqlcgen.GetPolicyByPrefixParams{OwnerID: ownerID, SiteID: p.SiteID, PathPrefix: p.PathPrefix})
		if errors.Is(err, sql.ErrNoRows) && p.PasswordHash == KeepPasswordHash {
			return ErrNoPasswordToKeep
		}
		if errors.Is(err, sql.ErrNoRows) {
			out = p
			out.Version = 1
			out.CreatedAt = p.UpdatedAt
			if out.KeepAllowlist {
				out.Allowlist, out.KeepAllowlist = Allowlist{}, false
			}
			if out.KeepGitHubOrgs {
				out.Allowlist.GitHubOrgs, out.KeepGitHubOrgs = nil, false
			}
			if out.EmailEnabled && len(out.Allowlist.Emails)+len(out.Allowlist.Domains) == 0 {
				return ErrEmailNeedsAllowlist
			}
			if out.OAuthEnabled && out.Allowlist.Len() == 0 {
				return ErrOAuthNeedsAllowlist
			}
			if err := mapErr("create policy", q.CreatePolicy(ctx, sqlcgen.CreatePolicyParams{
				ID:              out.ID,
				SiteID:          out.SiteID,
				PathPrefix:      out.PathPrefix,
				Visibility:      string(out.Visibility),
				LinksEnabled:    boolInt(out.LinksEnabled),
				PasswordEnabled: boolInt(out.PasswordEnabled),
				EmailEnabled:    boolInt(out.EmailEnabled),
				OauthEnabled:    boolInt(out.OAuthEnabled),
				PasswordHash:    nullString(out.PasswordHash),
				Version:         1,
				CreatedAt:       ms(out.CreatedAt),
				UpdatedAt:       ms(out.UpdatedAt),
				CreatedBy:       nullString(by),
				CreatedByToken:  nullString(token),
			})); err != nil {
				return err
			}
			return addAllowlist(ctx, q, out)
		}
		if err != nil {
			return mapErr("get policy", err)
		}
		cur := policyFromRow(row)
		entries, err := q.ListPolicyAllowlist(ctx, sqlcgen.ListPolicyAllowlistParams{SiteID: cur.SiteID, PolicyID: cur.ID})
		if err != nil {
			return mapErr("get allowlist", err)
		}
		cur.Allowlist = allowlistsFromRows(entries)[cur.ID]
		if p.PasswordHash == KeepPasswordHash {
			if !cur.PasswordEnabled || cur.PasswordHash == "" {
				return ErrNoPasswordToKeep
			}
			p.PasswordHash = cur.PasswordHash
		}
		if p.KeepAllowlist {
			p.Allowlist, p.KeepAllowlist = cur.Allowlist, false
		}
		if p.KeepGitHubOrgs {
			p.Allowlist.GitHubOrgs, p.KeepGitHubOrgs = cur.Allowlist.GitHubOrgs, false
		}
		if p.EmailEnabled && len(p.Allowlist.Emails)+len(p.Allowlist.Domains) == 0 {
			return ErrEmailNeedsAllowlist
		}
		if p.OAuthEnabled && p.Allowlist.Len() == 0 {
			return ErrOAuthNeedsAllowlist
		}
		if cur.SameSettings(p) {
			out = cur
			return nil
		}
		out = p
		out.ID, out.CreatedAt, out.Version = cur.ID, cur.CreatedAt, cur.Version+1
		if cur.PasswordHash != "" && cur.PasswordHash != out.PasswordHash {
			// A new or removed password ends the policy's password
			// sessions outright, in the same transaction (A2.3).
			if err := q.RevokePolicyPasswordSessions(ctx, sqlcgen.RevokePolicyPasswordSessionsParams{
				RevokedAt: nullMs(out.UpdatedAt), SiteID: out.SiteID, PolicyID: out.ID,
			}); err != nil {
				return mapErr("revoke password sessions", err)
			}
		}
		if !cur.Allowlist.Equal(out.Allowlist) {
			// A changed allowlist ends the sessions it let in outright,
			// in the same transaction (A2.4).
			if err := q.RevokePolicyAllowlistSessions(ctx, sqlcgen.RevokePolicyAllowlistSessionsParams{
				RevokedAt: nullMs(out.UpdatedAt), SiteID: out.SiteID, PolicyID: out.ID,
			}); err != nil {
				return mapErr("revoke allowlist sessions", err)
			}
			if err := q.ClearPolicyAllowlist(ctx, sqlcgen.ClearPolicyAllowlistParams{SiteID: out.SiteID, PolicyID: out.ID}); err != nil {
				return mapErr("clear allowlist", err)
			}
			if err := addAllowlist(ctx, q, out); err != nil {
				return err
			}
		}
		return mapErr("update policy", q.UpdatePolicy(ctx, sqlcgen.UpdatePolicyParams{
			ID:              out.ID,
			SiteID:          out.SiteID,
			Visibility:      string(out.Visibility),
			LinksEnabled:    boolInt(out.LinksEnabled),
			PasswordEnabled: boolInt(out.PasswordEnabled),
			EmailEnabled:    boolInt(out.EmailEnabled),
			OauthEnabled:    boolInt(out.OAuthEnabled),
			PasswordHash:    nullString(out.PasswordHash),
			Version:         int64(out.Version),
			UpdatedAt:       ms(out.UpdatedAt),
			CreatedBy:       nullString(by),
			CreatedByToken:  nullString(token),
		}))
	})
	if err != nil {
		return Policy{}, err
	}
	return out, nil
}

func (r policiesRepo) Delete(ctx context.Context, actor Actor, siteID, id string) error {
	ownerID, err := r.db.authorizeSite(ctx, actor, siteID, RoleEditor)
	if err != nil {
		return err
	}
	n, err := r.db.q.DeletePolicy(ctx, sqlcgen.DeletePolicyParams{ID: id, SiteID: siteID, OwnerID: ownerID})
	return rowsAffected("delete policy", n, err)
}
