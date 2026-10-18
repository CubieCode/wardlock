// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"errors"
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
	Version         int
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// SameSettings reports whether p and q decide alike: the same visibility,
// methods and password hash. Version, IDs and times are ignored.
func (p Policy) SameSettings(q Policy) bool {
	return p.Visibility == q.Visibility && p.LinksEnabled == q.LinksEnabled &&
		p.PasswordEnabled == q.PasswordEnabled && p.EmailEnabled == q.EmailEnabled &&
		p.OAuthEnabled == q.OAuthEnabled && p.PasswordHash == q.PasswordHash
}

// KeepPasswordHash, as a Policy's PasswordHash passed to Policies.Set, keeps
// the stored policy's password hash, read inside Set's transaction so a
// concurrent password change is never undone. With no stored password Set
// returns ErrNoPasswordToKeep. It is never a valid hash, so never stored.
const KeepPasswordHash = "keep:"

// ErrNoPasswordToKeep is returned by Policies.Set for KeepPasswordHash when
// the policy has no password enabled.
var ErrNoPasswordToKeep = errors.New("no password to keep")

// Policies stores site and path policies, one per site and prefix.
type Policies interface {
	// Create adds p to one of ownerID's sites.
	Create(ctx context.Context, ownerID string, p Policy) error
	// ForSite returns every policy of siteID ordered by path prefix. It is
	// an instance-wide read for the edge and must never back an owner API
	// handler.
	ForSite(ctx context.Context, siteID string) ([]Policy, error)
	// EnsureDefault inserts p as siteID's default policy (PathPrefix "/",
	// Version 1, UpdatedAt = CreatedAt) unless the site has one already, in
	// which case it changes nothing. Instance-wide, like ForSite.
	EnsureDefault(ctx context.Context, p Policy) error
	// List returns up to limit of the policies of ownerID's site siteID
	// ordered by path prefix, after afterPrefix ("" for the first page).
	List(ctx context.Context, ownerID, siteID, afterPrefix string, limit int) ([]Policy, error)
	// Set stores p's settings as the policy for (p.SiteID, p.PathPrefix) on
	// one of ownerID's sites, in one transaction. Without a policy for the
	// prefix it inserts p with Version 1. With one whose settings differ it
	// keeps that policy's ID and CreatedAt, takes p's settings and
	// UpdatedAt, and increments its Version, which ends every session bound
	// to the old version; a changed or removed password hash also revokes
	// the policy's password sessions. With identical settings it changes
	// nothing. A PasswordHash of KeepPasswordHash keeps the stored one (see
	// there). It returns the stored policy.
	Set(ctx context.Context, ownerID string, p Policy) (Policy, error)
	// Delete removes policy id of ownerID's site siteID together with the
	// site sessions bound to it.
	Delete(ctx context.Context, ownerID, siteID, id string) error
}

// Policies returns the SQLite Policies repository.
func (db *DB) Policies() Policies { return policiesRepo{db} }

type policiesRepo struct{ db *DB }

func (r policiesRepo) Create(ctx context.Context, ownerID string, p Policy) error {
	return r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
		if err := requireSite(ctx, q, ownerID, p.SiteID); err != nil {
			return err
		}
		return mapErr("create policy", q.CreatePolicy(ctx, sqlcgen.CreatePolicyParams{
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
		}))
	})
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
	rows, err := r.db.q.ListSitePolicies(ctx, siteID)
	if err != nil {
		return nil, mapErr("list site policies", err)
	}
	return policiesFromRows(rows), nil
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

func (r policiesRepo) List(ctx context.Context, ownerID, siteID, afterPrefix string, limit int) ([]Policy, error) {
	rows, err := r.db.q.ListOwnerPolicies(ctx, sqlcgen.ListOwnerPoliciesParams{
		OwnerID:     ownerID,
		SiteID:      siteID,
		AfterPrefix: afterPrefix,
		RowLimit:    int64(limit),
	})
	if err != nil {
		return nil, mapErr("list policies", err)
	}
	return policiesFromRows(rows), nil
}

func (r policiesRepo) Set(ctx context.Context, ownerID string, p Policy) (Policy, error) {
	var out Policy
	err := r.db.withTx(ctx, func(_ *sql.Tx, q *sqlcgen.Queries) error {
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
			return mapErr("create policy", q.CreatePolicy(ctx, sqlcgen.CreatePolicyParams{
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
			}))
		}
		if err != nil {
			return mapErr("get policy", err)
		}
		cur := policyFromRow(row)
		if p.PasswordHash == KeepPasswordHash {
			if !cur.PasswordEnabled || cur.PasswordHash == "" {
				return ErrNoPasswordToKeep
			}
			p.PasswordHash = cur.PasswordHash
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
		}))
	})
	if err != nil {
		return Policy{}, err
	}
	return out, nil
}

func (r policiesRepo) Delete(ctx context.Context, ownerID, siteID, id string) error {
	n, err := r.db.q.DeletePolicy(ctx, sqlcgen.DeletePolicyParams{ID: id, SiteID: siteID, OwnerID: ownerID})
	return rowsAffected("delete policy", n, err)
}
