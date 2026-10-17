// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package owners

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"wardlock.nz/server/internal/store"
)

// Passkey limits (C1.6).
const (
	// CeremonyValidity is how long a registration or sign-in challenge
	// stays usable: the WebAuthn timeout the browser is given.
	CeremonyValidity = 5 * time.Minute
	// MaxPasskeys is the most passkeys one owner may register.
	MaxPasskeys = 20
	// MaxPasskeyName is the longest passkey name, in characters.
	MaxPasskeyName = 100
	// DefaultPasskeyName names a passkey registered without a name.
	DefaultPasskeyName = "Passkey"
	// maxCeremonies bounds the challenges held in memory across the
	// instance. When full, new ceremonies are refused until old ones
	// expire, which can only delay passkey sign-in, never exhaust memory.
	maxCeremonies = 10_000
)

var (
	// ErrInvalidPasskey covers every rejected passkey response: an unknown,
	// used or expired ceremony, a malformed response, a wrong challenge,
	// origin or relying party, a bad signature, an unknown or removed
	// passkey, and a sign count that went backwards. Callers cannot tell
	// these apart.
	ErrInvalidPasskey = errors.New("invalid passkey")
	// ErrInvalidPasskeyName means the name is too long or has control
	// characters.
	ErrInvalidPasskeyName = errors.New("invalid passkey name")
	// ErrPasskeyLimit means the owner already has MaxPasskeys passkeys.
	ErrPasskeyLimit = errors.New("passkey limit reached")
)

// Passkeys is passkey (WebAuthn) registration, sign-in and management for
// owners. The relying party is the dashboard host, and only the dashboard
// origin is accepted. It is safe for concurrent use.
//
// Ceremony state (the challenge between the begin and finish calls) is
// held in memory, single use, for CeremonyValidity; a restart only cancels
// ceremonies in progress.
type Passkeys struct {
	svc        *Service
	repo       store.Passkeys
	wa         *webauthn.WebAuthn
	ceremonies *ceremonies
}

// NewPasskeys returns passkey support for svc's owners, stored in repo.
func NewPasskeys(svc *Service, repo store.Passkeys) (*Passkeys, error) {
	if svc == nil || repo == nil {
		return nil, errors.New("owners: missing dependency")
	}
	origin := svc.dash.Scheme + "://" + svc.dash.Host
	wa, err := webauthn.New(&webauthn.Config{
		RPID:          svc.dash.Hostname(),
		RPDisplayName: "Wardlock",
		RPOrigins:     []string{origin},
		// Discoverable credentials, so sign-in needs no email address.
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:        protocol.ResidentKeyRequirementRequired,
			RequireResidentKey: protocol.ResidentKeyRequired(),
			UserVerification:   protocol.VerificationPreferred,
		},
		AttestationPreference: protocol.PreferNoAttestation,
		Timeouts: webauthn.TimeoutsConfig{
			Login:        webauthn.TimeoutConfig{Timeout: CeremonyValidity, TimeoutUVD: CeremonyValidity},
			Registration: webauthn.TimeoutConfig{Timeout: CeremonyValidity, TimeoutUVD: CeremonyValidity},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("owners: webauthn: %w", err)
	}
	return &Passkeys{svc: svc, repo: repo, wa: wa, ceremonies: newCeremonies(svc.now)}, nil
}

// PasskeyChallenge starts a ceremony. Options is the WebAuthn
// PublicKeyCredentialCreationOptions (registration) or
// PublicKeyCredentialRequestOptions (sign-in) in their JSON form, binary
// values base64url-encoded; the client passes CeremonyID back with the
// authenticator's response.
type PasskeyChallenge struct {
	CeremonyID string
	Options    json.RawMessage
}

// PasskeyInfo is one of an owner's passkeys as the dashboard lists it.
type PasskeyInfo struct {
	ID         string
	Name       string
	CreatedAt  time.Time
	LastUsedAt time.Time // zero: never used to sign in
}

// BeginRegistration starts adding a passkey for the signed-in owner.
func (p *Passkeys) BeginRegistration(ctx context.Context, ownerID string) (PasskeyChallenge, error) {
	user, err := p.user(ctx, ownerID)
	if err != nil {
		return PasskeyChallenge{}, err
	}
	if len(user.creds) >= MaxPasskeys {
		return PasskeyChallenge{}, ErrPasskeyLimit
	}
	creation, data, err := p.wa.BeginRegistration(user,
		// Authenticators that already hold one of this owner's passkeys
		// refuse to make another.
		webauthn.WithExclusions(webauthn.Credentials(user.creds).CredentialDescriptors()),
	)
	if err != nil {
		return PasskeyChallenge{}, fmt.Errorf("begin passkey registration: %w", err)
	}
	return p.challenge(ceremony{kind: ceremonyRegister, ownerID: ownerID, data: *data}, creation.Response)
}

// FinishRegistration checks the authenticator's response to the ceremony
// and stores the new passkey under name (DefaultPasskeyName if blank).
func (p *Passkeys) FinishRegistration(ctx context.Context, ownerID, ceremonyID, name string, response []byte) (PasskeyInfo, error) {
	name, err := passkeyName(name)
	if err != nil {
		return PasskeyInfo{}, err
	}
	c, ok := p.ceremonies.take(ceremonyID, ceremonyRegister)
	if !ok || c.ownerID != ownerID {
		return PasskeyInfo{}, ErrInvalidPasskey
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(response)
	if err != nil {
		return PasskeyInfo{}, p.rejected(ctx, ownerID, "registration", err)
	}
	user, err := p.user(ctx, ownerID)
	if err != nil {
		return PasskeyInfo{}, err
	}
	if len(user.creds) >= MaxPasskeys {
		return PasskeyInfo{}, ErrPasskeyLimit
	}
	cred, err := p.wa.CreateCredential(user, c.data, parsed)
	if err != nil {
		return PasskeyInfo{}, p.rejected(ctx, ownerID, "registration", err)
	}
	record, err := json.Marshal(cred)
	if err != nil {
		return PasskeyInfo{}, fmt.Errorf("encode passkey: %w", err)
	}
	row := store.Passkey{
		ID:           store.NewID(),
		OwnerID:      ownerID,
		CredentialID: base64.RawURLEncoding.EncodeToString(cred.ID),
		Credential:   record,
		Name:         name,
		CreatedAt:    p.svc.now(),
	}
	if err := p.repo.Create(ctx, row); err != nil {
		if errors.Is(err, store.ErrConflict) {
			// The credential is already registered, to this owner or
			// another; say no more than for any other rejection.
			return PasskeyInfo{}, p.rejected(ctx, ownerID, "registration", err)
		}
		return PasskeyInfo{}, err
	}
	p.svc.log.LogAttrs(ctx, slog.LevelInfo, "passkey added", slog.String("owner_id", ownerID), slog.String("passkey_id", row.ID))
	return passkeyInfo(row), nil
}

// BeginSignIn starts a passkey sign-in. It names no owner: the browser
// offers whichever of the dashboard's passkeys it holds.
func (p *Passkeys) BeginSignIn(_ context.Context, c Client) (PasskeyChallenge, error) {
	if err := p.svc.allow("passkey-ip:"+ipKey(c.IP), p.svc.limits.VerifyPerIP); err != nil {
		return PasskeyChallenge{}, err
	}
	assertion, data, err := p.wa.BeginDiscoverableLogin()
	if err != nil {
		return PasskeyChallenge{}, fmt.Errorf("begin passkey sign-in: %w", err)
	}
	return p.challenge(ceremony{kind: ceremonySignIn, data: *data}, assertion.Response)
}

// FinishSignIn checks the authenticator's response to a sign-in ceremony
// and signs in the passkey's owner. The ceremony is spent whether or not
// the response is good, so a replayed response always fails.
func (p *Passkeys) FinishSignIn(ctx context.Context, c Client, ceremonyID string, response []byte) (store.Owner, NewSession, error) {
	cer, ok := p.ceremonies.take(ceremonyID, ceremonySignIn)
	if !ok {
		return store.Owner{}, NewSession{}, ErrInvalidPasskey
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(response)
	if err != nil {
		return store.Owner{}, NewSession{}, p.rejected(ctx, "", "sign-in", err)
	}
	var (
		row       store.Passkey
		lookupErr error // an unexpected store error, not a rejection
	)
	find := func(rawID, userHandle []byte) (webauthn.User, error) {
		var err error
		row, err = p.repo.GetByCredentialID(ctx, base64.RawURLEncoding.EncodeToString(rawID))
		if err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				lookupErr = err
			}
			return nil, err
		}
		if string(userHandle) != row.OwnerID {
			return nil, errors.New("user handle does not match the passkey's owner")
		}
		user, err := p.user(ctx, row.OwnerID)
		if err != nil && !errors.Is(err, ErrUnauthenticated) {
			lookupErr = err
		}
		return user, err
	}
	_, cred, err := p.wa.ValidatePasskeyLogin(find, cer.data, parsed)
	if lookupErr != nil {
		return store.Owner{}, NewSession{}, lookupErr
	}
	if err != nil {
		return store.Owner{}, NewSession{}, p.rejected(ctx, row.OwnerID, "sign-in", err)
	}
	if cred.Authenticator.CloneWarning {
		// The sign count did not move forward: a cloned authenticator or a
		// replayed assertion. Refuse it and keep the stored count.
		return store.Owner{}, NewSession{}, p.rejected(ctx, row.OwnerID, "sign-in", errors.New("sign count did not increase"))
	}
	record, err := json.Marshal(cred)
	if err != nil {
		return store.Owner{}, NewSession{}, fmt.Errorf("encode passkey: %w", err)
	}
	if err := p.repo.RecordUse(ctx, row.OwnerID, row.ID, record, p.svc.now()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.Owner{}, NewSession{}, ErrInvalidPasskey // removed meanwhile
		}
		return store.Owner{}, NewSession{}, err
	}
	return p.svc.signIn(ctx, c, row.OwnerID, "passkey")
}

// List returns all of ownerID's passkeys, oldest first.
func (p *Passkeys) List(ctx context.Context, ownerID string) ([]PasskeyInfo, error) {
	rows, err := p.repo.ListOwner(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	out := make([]PasskeyInfo, len(rows))
	for i, r := range rows {
		out[i] = passkeyInfo(r)
	}
	return out, nil
}

// Remove deletes ownerID's passkey id. Another owner's passkey and a
// missing one are both ErrNotFound.
func (p *Passkeys) Remove(ctx context.Context, ownerID, id string) error {
	err := p.repo.Delete(ctx, ownerID, id)
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	p.svc.log.LogAttrs(ctx, slog.LevelInfo, "passkey removed", slog.String("owner_id", ownerID), slog.String("passkey_id", id))
	return nil
}

// challenge records c and returns its ID with the options to send.
func (p *Passkeys) challenge(c ceremony, options any) (PasskeyChallenge, error) {
	raw, err := json.Marshal(options)
	if err != nil {
		return PasskeyChallenge{}, fmt.Errorf("encode passkey options: %w", err)
	}
	id, err := p.ceremonies.put(c)
	if err != nil {
		return PasskeyChallenge{}, err
	}
	return PasskeyChallenge{CeremonyID: id, Options: raw}, nil
}

// rejected logs why a passkey response failed and returns
// ErrInvalidPasskey. WebAuthn errors describe the check that failed; they
// carry no secrets (a passkey response holds only public values).
func (p *Passkeys) rejected(ctx context.Context, ownerID, ceremony string, err error) error {
	reason := err.Error()
	var perr *protocol.Error
	if errors.As(err, &perr) && perr.Details != "" {
		reason = perr.Details
	}
	attrs := []slog.Attr{slog.String("ceremony", ceremony), slog.String("reason", reason)}
	if ownerID != "" {
		attrs = append(attrs, slog.String("owner_id", ownerID))
	}
	p.svc.log.LogAttrs(ctx, slog.LevelInfo, "passkey rejected", attrs...)
	return ErrInvalidPasskey
}

// user loads an owner and their passkeys as a WebAuthn user.
func (p *Passkeys) user(ctx context.Context, ownerID string) (*passkeyUser, error) {
	o, err := p.svc.owners.Get(ctx, ownerID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrUnauthenticated
	}
	if err != nil {
		return nil, err
	}
	rows, err := p.repo.ListOwner(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	u := &passkeyUser{owner: o, creds: make([]webauthn.Credential, 0, len(rows))}
	for _, r := range rows {
		var c webauthn.Credential
		if err := json.Unmarshal(r.Credential, &c); err != nil {
			return nil, fmt.Errorf("decode passkey %s: %w", r.ID, err)
		}
		u.creds = append(u.creds, c)
	}
	return u, nil
}

// passkeyUser is an owner as go-webauthn sees them. The user handle is the
// owner ID: stable, unique and not personal data.
type passkeyUser struct {
	owner store.Owner
	creds []webauthn.Credential
}

func (u *passkeyUser) WebAuthnID() []byte                         { return []byte(u.owner.ID) }
func (u *passkeyUser) WebAuthnName() string                       { return u.owner.Email }
func (u *passkeyUser) WebAuthnDisplayName() string                { return u.owner.Email }
func (u *passkeyUser) WebAuthnCredentials() []webauthn.Credential { return u.creds }

func passkeyInfo(r store.Passkey) PasskeyInfo {
	return PasskeyInfo{ID: r.ID, Name: r.Name, CreatedAt: r.CreatedAt, LastUsedAt: r.LastUsedAt}
}

// passkeyName trims name, defaults it and checks it.
func passkeyName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return DefaultPasskeyName, nil
	}
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) > MaxPasskeyName {
		return "", ErrInvalidPasskeyName
	}
	for _, r := range name {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return "", ErrInvalidPasskeyName
		}
	}
	return name, nil
}

type ceremonyKind int

const (
	ceremonyRegister ceremonyKind = iota + 1
	ceremonySignIn
)

// ceremony is the server's half of a WebAuthn ceremony in progress.
type ceremony struct {
	kind    ceremonyKind
	ownerID string // registration only: who started it
	data    webauthn.SessionData
	expires time.Time
}

// ceremonies holds ceremonies in progress by random ID, each usable once.
type ceremonies struct {
	now func() time.Time
	max int

	mu sync.Mutex
	m  map[string]ceremony
}

func newCeremonies(now func() time.Time) *ceremonies {
	return &ceremonies{now: now, max: maxCeremonies, m: map[string]ceremony{}}
}

// put stores c for CeremonyValidity and returns its ID. When the table is
// full even after dropping expired entries, it refuses with a
// RateLimitError for the time until the oldest entry expires.
func (cs *ceremonies) put(c ceremony) (string, error) {
	now := cs.now()
	c.expires = now.Add(CeremonyValidity)
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if len(cs.m) >= cs.max {
		next := c.expires
		for id, old := range cs.m {
			if !now.Before(old.expires) {
				delete(cs.m, id)
			} else if old.expires.Before(next) {
				next = old.expires
			}
		}
		if len(cs.m) >= cs.max {
			return "", &RateLimitError{RetryAfter: next.Sub(now)}
		}
	}
	id := randomToken()
	cs.m[id] = c
	return id, nil
}

// take removes and returns ceremony id if it is of kind and unexpired. A
// ceremony of another kind is left alone.
func (cs *ceremonies) take(id string, kind ceremonyKind) (ceremony, bool) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	c, ok := cs.m[id]
	if !ok || c.kind != kind {
		return ceremony{}, false
	}
	delete(cs.m, id)
	return c, cs.now().Before(c.expires)
}
