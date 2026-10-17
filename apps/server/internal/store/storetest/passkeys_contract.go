// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"wardlock.nz/server/internal/store"
)

func (f *fixture) validPasskey(ownerID string) store.Passkey {
	return store.Passkey{
		ID: store.NewID(), OwnerID: ownerID, CredentialID: fmt.Sprintf("cred-%d", f.seq()),
		Credential: []byte(`{"id":"Y3JlZA","publicKey":"cGs"}`), Name: "Laptop", CreatedAt: now,
	}
}

func (f *fixture) passkey(ownerID string, createdAt time.Time) store.Passkey {
	f.t.Helper()
	p := f.validPasskey(ownerID)
	p.CreatedAt = createdAt
	f.must(f.r.Passkeys.Create(f.t.Context(), p))
	return p
}

func passkeysContract(t *testing.T, open Opener) {
	t.Run("Create", func(t *testing.T) {
		t.Parallel()
		runInsertCases(t, open,
			func(f *fixture) store.Passkey { return f.validPasskey(f.owner().ID) },
			func(f *fixture, p store.Passkey) error { return f.r.Passkeys.Create(f.t.Context(), p) },
			[]insertCase[store.Passkey]{
				{name: "valid"},
				{name: "used before", mutate: func(_ *fixture, p *store.Passkey) { p.LastUsedAt = now.Add(time.Minute) }},
				{name: "longest credential ID", mutate: func(_ *fixture, p *store.Passkey) { p.CredentialID = strings.Repeat("A", 1364) }},
				{name: "credential ID taken by another owner", mutate: func(f *fixture, p *store.Passkey) {
					other := f.validPasskey(f.owner().ID)
					other.CredentialID = p.CredentialID
					f.must(f.r.Passkeys.Create(f.t.Context(), other))
				}, want: store.ErrConflict},
				{name: "duplicate ID", mutate: func(f *fixture, p *store.Passkey) {
					other := f.validPasskey(p.OwnerID)
					other.ID = p.ID
					f.must(f.r.Passkeys.Create(f.t.Context(), other))
				}, want: store.ErrConflict},
				{name: "empty credential ID", mutate: func(_ *fixture, p *store.Passkey) { p.CredentialID = "" }, want: store.ErrConstraint},
				{name: "long credential ID", mutate: func(_ *fixture, p *store.Passkey) { p.CredentialID = strings.Repeat("A", 1365) }, want: store.ErrConstraint},
				{name: "empty credential", mutate: func(_ *fixture, p *store.Passkey) { p.Credential = nil }, want: store.ErrConstraint},
				{name: "empty name", mutate: func(_ *fixture, p *store.Passkey) { p.Name = "" }, want: store.ErrConstraint},
				{name: "long name", mutate: func(_ *fixture, p *store.Passkey) { p.Name = strings.Repeat("n", 101) }, want: store.ErrConstraint},
				{name: "unknown owner", mutate: func(_ *fixture, p *store.Passkey) { p.OwnerID = store.NewID() }, want: store.ErrConstraint},
			})
	})
	t.Run("ListOwner", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		a, b := f.owner(), f.owner()
		later := f.passkey(a.ID, now.Add(time.Minute))
		first := f.passkey(a.ID, now)
		f.passkey(b.ID, now)
		got, err := f.r.Passkeys.ListOwner(t.Context(), a.ID)
		if err != nil {
			t.Fatal(err)
		}
		if want := []store.Passkey{first, later}; !reflect.DeepEqual(got, want) {
			t.Fatalf("ListOwner() = %+v, want %+v", got, want)
		}
		got, err = f.r.Passkeys.ListOwner(t.Context(), f.owner().ID)
		if err != nil || len(got) != 0 {
			t.Fatalf("ListOwner(no passkeys) = %v, %v; want none", got, err)
		}
	})
	t.Run("GetByCredentialID", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		want := f.passkey(f.owner().ID, now)
		got, err := f.r.Passkeys.GetByCredentialID(t.Context(), want.CredentialID)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("GetByCredentialID() = %+v, want %+v", got, want)
		}
		_, err = f.r.Passkeys.GetByCredentialID(t.Context(), "unknown")
		wantErr(t, err, store.ErrNotFound)
	})
	t.Run("RecordUse", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		a, b := f.owner(), f.owner()
		p := f.passkey(a.ID, now)
		record := []byte(`{"id":"Y3JlZA","publicKey":"cGs","authenticator":{"signCount":7}}`)
		at := now.Add(time.Hour)
		wantErr(t, f.r.Passkeys.RecordUse(t.Context(), b.ID, p.ID, record, at), store.ErrNotFound)
		wantErr(t, f.r.Passkeys.RecordUse(t.Context(), a.ID, store.NewID(), record, at), store.ErrNotFound)
		f.must(f.r.Passkeys.RecordUse(t.Context(), a.ID, p.ID, record, at))
		got, err := f.r.Passkeys.GetByCredentialID(t.Context(), p.CredentialID)
		if err != nil {
			t.Fatal(err)
		}
		if string(got.Credential) != string(record) || !got.LastUsedAt.Equal(at) {
			t.Fatalf("after RecordUse: credential %s, last used %v; want %s, %v", got.Credential, got.LastUsedAt, record, at)
		}
	})
	t.Run("Delete", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, open)
		a, b := f.owner(), f.owner()
		p := f.passkey(a.ID, now)
		wantErr(t, f.r.Passkeys.Delete(t.Context(), b.ID, p.ID), store.ErrNotFound)
		wantErr(t, f.r.Passkeys.Delete(t.Context(), a.ID, store.NewID()), store.ErrNotFound)
		f.must(f.r.Passkeys.Delete(t.Context(), a.ID, p.ID))
		_, err := f.r.Passkeys.GetByCredentialID(t.Context(), p.CredentialID)
		wantErr(t, err, store.ErrNotFound)
		wantErr(t, f.r.Passkeys.Delete(t.Context(), a.ID, p.ID), store.ErrNotFound)
	})
}
