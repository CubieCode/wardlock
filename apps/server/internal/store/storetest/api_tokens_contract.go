// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"strings"
	"testing"
	"time"

	"wardlock.nz/server/internal/store"
)

func apiTokensContract(t *testing.T, open Opener) {
	t.Run("Create", func(t *testing.T) {
		t.Parallel()
		runInsertCases(t, open,
			func(f *fixture) store.APIToken {
				return store.APIToken{
					ID: store.NewID(), OwnerID: f.owner().ID, PublicID: f.publicID(), TokenHash: f.hash(),
					Name: "CI", ScopeDeploy: true, CreatedAt: now,
				}
			},
			func(f *fixture, tok store.APIToken) error { return f.r.APITokens.Create(f.t.Context(), tok) },
			[]insertCase[store.APIToken]{
				{name: "valid"},
				{name: "expiring, manage only", mutate: func(_ *fixture, tok *store.APIToken) {
					tok.ScopeDeploy, tok.ScopeManage, tok.ExpiresAt = false, true, now.Add(time.Hour)
				}},
				{name: "duplicate public ID across owners", mutate: func(f *fixture, tok *store.APIToken) {
					other := *tok
					other.ID, other.OwnerID, other.TokenHash = store.NewID(), f.owner().ID, f.hash()
					f.must(f.r.APITokens.Create(f.t.Context(), other))
				}, want: store.ErrConflict},
				{name: "short public ID", mutate: func(_ *fixture, tok *store.APIToken) { tok.PublicID = "abc" }, want: store.ErrConstraint},
				{name: "uppercase public ID", mutate: func(_ *fixture, tok *store.APIToken) { tok.PublicID = "ABCDEFGHIJ" }, want: store.ErrConstraint},
				{name: "no scopes", mutate: func(_ *fixture, tok *store.APIToken) { tok.ScopeDeploy = false }, want: store.ErrConstraint},
				{name: "empty name", mutate: func(_ *fixture, tok *store.APIToken) { tok.Name = "" }, want: store.ErrConstraint},
				{name: "long name", mutate: func(_ *fixture, tok *store.APIToken) { tok.Name = strings.Repeat("n", 101) }, want: store.ErrConstraint},
				{name: "expires at creation", mutate: func(_ *fixture, tok *store.APIToken) { tok.ExpiresAt = tok.CreatedAt }, want: store.ErrConstraint},
				{name: "unknown owner", mutate: func(_ *fixture, tok *store.APIToken) { tok.OwnerID = store.NewID() }, want: store.ErrConstraint},
			})
	})
	t.Run("CreateCLILogin", func(t *testing.T) {
		t.Parallel()
		runInsertCases(t, open,
			func(f *fixture) store.CLILoginRequest {
				return store.CLILoginRequest{
					ID: store.NewID(), DeviceCodeHash: f.hash(), UserCode: "WDJB-MJHT", Status: store.CLILoginPending,
					ScopeDeploy: true, CreatedAt: now, ExpiresAt: now.Add(10 * time.Minute),
				}
			},
			func(f *fixture, l store.CLILoginRequest) error { return f.r.APITokens.CreateCLILogin(f.t.Context(), l) },
			[]insertCase[store.CLILoginRequest]{
				{name: "pending"},
				{name: "approved", mutate: func(f *fixture, l *store.CLILoginRequest) {
					l.Status, l.OwnerID, l.DecidedAt = store.CLILoginApproved, f.owner().ID, now
				}},
				{name: "redeemed", mutate: func(f *fixture, l *store.CLILoginRequest) {
					o := f.owner()
					tok := store.APIToken{ID: store.NewID(), OwnerID: o.ID, PublicID: f.publicID(), TokenHash: f.hash(), Name: "cli", ScopeDeploy: true, CreatedAt: now}
					f.must(f.r.APITokens.Create(f.t.Context(), tok))
					l.Status, l.OwnerID, l.APITokenID = store.CLILoginRedeemed, o.ID, tok.ID
				}},
				{name: "pending with owner", mutate: func(f *fixture, l *store.CLILoginRequest) { l.OwnerID = f.owner().ID }, want: store.ErrConstraint},
				{name: "approved without owner", mutate: func(_ *fixture, l *store.CLILoginRequest) { l.Status = store.CLILoginApproved }, want: store.ErrConstraint},
				{name: "redeemed without token", mutate: func(f *fixture, l *store.CLILoginRequest) {
					l.Status, l.OwnerID = store.CLILoginRedeemed, f.owner().ID
				}, want: store.ErrConstraint},
				{name: "unknown status", mutate: func(_ *fixture, l *store.CLILoginRequest) { l.Status = "expired" }, want: store.ErrConstraint},
				{name: "short user code", mutate: func(_ *fixture, l *store.CLILoginRequest) { l.UserCode = "AB" }, want: store.ErrConstraint},
				{name: "duplicate user code", mutate: func(f *fixture, l *store.CLILoginRequest) {
					other := *l
					other.ID, other.DeviceCodeHash = store.NewID(), f.hash()
					f.must(f.r.APITokens.CreateCLILogin(f.t.Context(), other))
				}, want: store.ErrConflict},
				{name: "duplicate device code", mutate: func(f *fixture, l *store.CLILoginRequest) {
					other := *l
					other.ID, other.UserCode = store.NewID(), "XXXX-YYYY"
					f.must(f.r.APITokens.CreateCLILogin(f.t.Context(), other))
				}, want: store.ErrConflict},
				{name: "expires at creation", mutate: func(_ *fixture, l *store.CLILoginRequest) { l.ExpiresAt = l.CreatedAt }, want: store.ErrConstraint},
			})
	})
}
