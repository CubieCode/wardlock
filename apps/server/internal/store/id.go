// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store

import "github.com/google/uuid"

// NewID returns a new UUIDv7 in canonical text form. IDs are generated in Go
// rather than by the database so every backend stores the same values, and
// v7's time ordering keeps primary-key inserts append-only.
func NewID() string {
	// NewV7 fails only if crypto/rand does, which since Go 1.24 crashes the
	// program instead of returning an error.
	return uuid.Must(uuid.NewV7()).String()
}
