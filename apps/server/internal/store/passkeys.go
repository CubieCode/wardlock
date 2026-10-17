// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"errors"
	"fmt"
	"math"
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
	// SignCount is the credential record's signature counter, stored
	// beside it for RecordUse's compare-and-set.
	SignCount  uint32
	Name       string
	CreatedAt  time.Time
	LastUsedAt time.Time // zero: never used to sign in
}

// ErrPasskeyLimit means the owner already has as many passkeys as Create
// allows.
var ErrPasskeyLimit = errors.New("passkey limit reached")

// Passkeys stores owner passkeys.
type Passkeys interface {
	// Create stores a new passkey if its owner has fewer than limit, checked
	// in the same statement as the insert; otherwise ErrPasskeyLimit. A
	// credential ID already registered (by any owner) is ErrConflict.
	Create(ctx context.Context, p Passkey, limit int) error
	// ListOwner returns all of ownerID's passkeys, oldest first.
	ListOwner(ctx context.Context, ownerID string) ([]Passkey, error)
	// GetByCredentialID finds a passkey by credential ID, whoever owns it;
	// passkey sign-in starts with nothing else.
	GetByCredentialID(ctx context.Context, credentialID string) (Passkey, error)
	// RecordUse replaces ownerID's passkey id's credential record and sign
	// count (both move on each sign-in) and sets its last use, but only if
	// its sign count is still previous. Another owner's or a missing
	// passkey, and one whose count has moved since it was read (a
	// concurrent sign-in), are ErrNotFound.
	RecordUse(ctx context.Context, ownerID, id string, previous, signCount uint32, credential []byte, at time.Time) error
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
		SignCount:    uint32(min(max(row.SignCount, 0), math.MaxUint32)),
		Name:         row.Name,
		CreatedAt:    fromMs(row.CreatedAt),
		LastUsedAt:   fromNullMs(row.LastUsedAt),
	}
}

func (r passkeysRepo) Create(ctx context.Context, p Passkey, limit int) error {
	n, err := r.db.q.CreatePasskey(ctx, sqlcgen.CreatePasskeyParams{
		ID:           p.ID,
		OwnerID:      p.OwnerID,
		CredentialID: p.CredentialID,
		Credential:   string(p.Credential),
		SignCount:    int64(p.SignCount),
		Name:         p.Name,
		CreatedAt:    ms(p.CreatedAt),
		LastUsedAt:   nullMs(p.LastUsedAt),
		MaxPasskeys:  int64(limit),
	})
	if err != nil {
		return mapErr("create passkey", err)
	}
	if n == 0 {
		return fmt.Errorf("create passkey: %w", ErrPasskeyLimit)
	}
	return nil
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

func (r passkeysRepo) RecordUse(ctx context.Context, ownerID, id string, previous, signCount uint32, credential []byte, at time.Time) error {
	n, err := r.db.q.RecordPasskeyUse(ctx, sqlcgen.RecordPasskeyUseParams{
		ID: id, OwnerID: ownerID, Credential: string(credential), LastUsedAt: nullMs(at),
		SignCount: int64(signCount), PreviousSignCount: int64(previous),
	})
	return rowsAffected("record passkey use", n, err)
}

func (r passkeysRepo) Delete(ctx context.Context, ownerID, id string) error {
	n, err := r.db.q.DeleteOwnerPasskey(ctx, sqlcgen.DeleteOwnerPasskeyParams{ID: id, OwnerID: ownerID})
	return rowsAffected("delete passkey", n, err)
}
