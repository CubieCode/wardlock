// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"

	"wardlock.nz/server/internal/store"
)

type actorKey struct{}

// WithActor returns ctx carrying the verified actor. Only the authentication
// middleware (and tests) call it.
func WithActor(ctx context.Context, actor store.Actor) context.Context {
	return context.WithValue(ctx, actorKey{}, actor)
}

// WithOwner returns ctx carrying the verified owner, in no team.
func WithOwner(ctx context.Context, ownerID string) context.Context {
	return WithActor(ctx, store.ActorFor(ownerID))
}

// ActorOf returns the verified actor, if the request is authenticated. It
// comes from the session or token, never from the request.
func ActorOf(ctx context.Context) (store.Actor, bool) {
	a, ok := ctx.Value(actorKey{}).(store.Actor)
	return a, ok && a.OwnerID != ""
}

// OwnerID returns the verified owner ID, if the request is authenticated:
// for operations that are a person's own. A team token's is its team, but
// those operations are session-only or refused to team tokens.
func OwnerID(ctx context.Context) (string, bool) {
	a, ok := ActorOf(ctx)
	return a.OwnerID, ok
}
