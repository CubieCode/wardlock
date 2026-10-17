// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"time"

	"wardlock.nz/server/internal/store/sqlcgen"
)

// Passkey is an owner's WebAuthn credential (C1.6).
type Passkey struct {
	ID      string
	OwnerID string
	// CredentialID is the authenticator's credential ID, base64url without
	// padding; unique across the instance.
	CredentialID string
	// Credential is the WebAuthn credential record as JSON (public key,
	// sign count, flags, transports, AAGUID). The store keeps it opaque.
	Credential []byte
	Name       string
	CreatedAt  time.Time
	LastUsedAt time.Time // zero: never used to sign in
}

// Passkeys stores owner passkeys.
type Passkeys interface {
	// Create stores a new passkey. A credential ID already registered (by
	// any owner) is ErrConflict.
	Create(ctx context.Context, p Passkey) error
	// ListOwner returns all of ownerID's passkeys, oldest first.
	ListOwner(ctx context.Context, ownerID string) ([]Passkey, error)
	// GetByCredentialID finds a passkey by credential ID, whoever owns it;
	// passkey sign-in starts with nothing else.
	GetByCredentialID(ctx context.Context, credentialID string) (Passkey, error)
	// RecordUse replaces ownerID's passkey id's credential record (its sign
	// count and flags move on each sign-in) and sets its last use. Another
	// owner's or a missing passkey is ErrNotFound.
	RecordUse(ctx context.Context, ownerID, id string, credential []byte, at time.Time) error
	// Delete removes ownerID's passkey id. Another owner's or a missing
	// passkey is ErrNotFound.
	Delete(ctx context.Context, ownerID, id string) error
}

// Passkeys returns the SQLite Passkeys repository.
func (db *DB) Passkeys() Passkeys { return passkeysRepo{db} }

type passkeysRepo struct{ db *DB }

func passkeyFromRow(row sqlcgen.OwnerPasskey) Passkey {
	return Passkey{
		ID:           row.ID,
		OwnerID:      row.OwnerID,
		CredentialID: row.CredentialID,
		Credential:   []byte(row.Credential),
		Name:         row.Name,
		CreatedAt:    fromMs(row.CreatedAt),
		LastUsedAt:   fromNullMs(row.LastUsedAt),
	}
}

func (r passkeysRepo) Create(ctx context.Context, p Passkey) error {
	return mapErr("create passkey", r.db.q.CreatePasskey(ctx, sqlcgen.CreatePasskeyParams{
		ID:           p.ID,
		OwnerID:      p.OwnerID,
		CredentialID: p.CredentialID,
		Credential:   string(p.Credential),
		Name:         p.Name,
		CreatedAt:    ms(p.CreatedAt),
		LastUsedAt:   nullMs(p.LastUsedAt),
	}))
}

func (r passkeysRepo) ListOwner(ctx context.Context, ownerID string) ([]Passkey, error) {
	rows, err := r.db.q.ListOwnerPasskeys(ctx, ownerID)
	if err != nil {
		return nil, mapErr("list passkeys", err)
	}
	out := make([]Passkey, len(rows))
	for i, row := range rows {
		out[i] = passkeyFromRow(row)
	}
	return out, nil
}

func (r passkeysRepo) GetByCredentialID(ctx context.Context, credentialID string) (Passkey, error) {
	row, err := r.db.q.GetPasskeyByCredentialID(ctx, credentialID)
	if err != nil {
		return Passkey{}, mapErr("get passkey", err)
	}
	return passkeyFromRow(row), nil
}

func (r passkeysRepo) RecordUse(ctx context.Context, ownerID, id string, credential []byte, at time.Time) error {
	n, err := r.db.q.RecordPasskeyUse(ctx, sqlcgen.RecordPasskeyUseParams{
		ID: id, OwnerID: ownerID, Credential: string(credential), LastUsedAt: nullMs(at),
	})
	return rowsAffected("record passkey use", n, err)
}

func (r passkeysRepo) Delete(ctx context.Context, ownerID, id string) error {
	n, err := r.db.q.DeleteOwnerPasskey(ctx, sqlcgen.DeleteOwnerPasskeyParams{ID: id, OwnerID: ownerID})
	return rowsAffected("delete passkey", n, err)
}
