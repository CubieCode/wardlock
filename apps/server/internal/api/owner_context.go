// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package api

import "context"

type ownerKey struct{}

// WithOwner returns ctx carrying the verified owner ID. Only the
// authentication middleware (and tests) call it.
func WithOwner(ctx context.Context, ownerID string) context.Context {
	return context.WithValue(ctx, ownerKey{}, ownerID)
}

// OwnerID returns the verified owner ID, if the request is authenticated.
func OwnerID(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(ownerKey{}).(string)
	return id, ok && id != ""
}
