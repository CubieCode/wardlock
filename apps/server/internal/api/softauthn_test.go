// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"testing"

	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
	"github.com/go-webauthn/webauthn/protocol/webauthncose"
)

// softAuthenticator is a software WebAuthn authenticator holding one
// discoverable ES256 credential, for ceremony tests. Its fields can be
// changed between calls to forge bad responses.
type softAuthenticator struct {
	t      *testing.T
	key    *ecdsa.PrivateKey
	credID []byte
	// userHandle is set from the creation options' user.id.
	userHandle []byte
	// count is the signature counter; each assertion increments it first.
	count uint32
}

func newSoftAuthenticator(t *testing.T) *softAuthenticator {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 16)
	_, _ = rand.Read(id)
	return &softAuthenticator{t: t, key: key, credID: id}
}

var b64 = base64.RawURLEncoding

// ceremonyOptions is the part of a challenge's options the authenticator
// reads.
type ceremonyOptions struct {
	Challenge string `json:"challenge"`
	RPID      string `json:"rpId"` // sign-in
	RP        struct {
		ID string `json:"id"`
	} `json:"rp"` // registration
	User struct {
		ID string `json:"id"`
	} `json:"user"`
}

func decodeOptions(t *testing.T, options map[string]any) ceremonyOptions {
	t.Helper()
	raw, err := json.Marshal(options)
	if err != nil {
		t.Fatal(err)
	}
	var o ceremonyOptions
	if err := json.Unmarshal(raw, &o); err != nil {
		t.Fatal(err)
	}
	return o
}

// clientData is what the browser signs over.
type clientData struct {
	Type        string `json:"type"`
	Challenge   string `json:"challenge"`
	Origin      string `json:"origin"`
	CrossOrigin bool   `json:"crossOrigin"`
}

const (
	flagUP = 0x01
	flagUV = 0x04
	flagAT = 0x40
)

func (a *softAuthenticator) authData(rpID string, flags byte, attested []byte) []byte {
	h := sha256.Sum256([]byte(rpID))
	out := append(h[:], flags)
	out = binary.BigEndian.AppendUint32(out, a.count)
	return append(out, attested...)
}

// create answers registration options as a browser on origin would, with
// "none" attestation. cd and rpID override what the options say when set.
func (a *softAuthenticator) create(options map[string]any, origin string, edit func(cd *clientData, rpID *string)) map[string]any {
	a.t.Helper()
	o := decodeOptions(a.t, options)
	handle, err := b64.DecodeString(o.User.ID)
	if err != nil {
		a.t.Fatalf("user.id: %v", err)
	}
	a.userHandle = handle
	cd := clientData{Type: "webauthn.create", Challenge: o.Challenge, Origin: origin}
	rpID := o.RP.ID
	if edit != nil {
		edit(&cd, &rpID)
	}
	cose, err := webauthncbor.Marshal(webauthncose.EC2PublicKeyData{
		PublicKeyData: webauthncose.PublicKeyData{KeyType: int64(webauthncose.EllipticKey), Algorithm: int64(webauthncose.AlgES256)},
		Curve:         1, // P-256
		XCoord:        a.key.X.FillBytes(make([]byte, 32)),
		YCoord:        a.key.Y.FillBytes(make([]byte, 32)),
	})
	if err != nil {
		a.t.Fatal(err)
	}
	attested := make([]byte, 16) // zero AAGUID
	attested = binary.BigEndian.AppendUint16(attested, uint16(len(a.credID)))
	attested = append(attested, a.credID...)
	attested = append(attested, cose...)
	att, err := webauthncbor.Marshal(map[string]any{
		"fmt":      "none",
		"attStmt":  map[string]any{},
		"authData": a.authData(rpID, flagUP|flagUV|flagAT, attested),
	})
	if err != nil {
		a.t.Fatal(err)
	}
	return map[string]any{
		"id":    b64.EncodeToString(a.credID),
		"rawId": b64.EncodeToString(a.credID),
		"type":  "public-key",
		"response": map[string]any{
			"clientDataJSON":    b64.EncodeToString(a.json(cd)),
			"attestationObject": b64.EncodeToString(att),
			"transports":        []string{"internal"},
		},
		"clientExtensionResults":  map[string]any{},
		"authenticatorAttachment": "platform",
	}
}

// get answers sign-in options as a browser on origin would. edit may
// change the client data and relying party before signing.
func (a *softAuthenticator) get(options map[string]any, origin string, edit func(cd *clientData, rpID *string)) map[string]any {
	a.t.Helper()
	o := decodeOptions(a.t, options)
	a.count++
	cd := clientData{Type: "webauthn.get", Challenge: o.Challenge, Origin: origin}
	rpID := o.RPID
	if edit != nil {
		edit(&cd, &rpID)
	}
	cdJSON := a.json(cd)
	auth := a.authData(rpID, flagUP|flagUV, nil)
	cdHash := sha256.Sum256(cdJSON)
	digest := sha256.Sum256(append(append([]byte{}, auth...), cdHash[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, a.key, digest[:])
	if err != nil {
		a.t.Fatal(err)
	}
	return map[string]any{
		"id":    b64.EncodeToString(a.credID),
		"rawId": b64.EncodeToString(a.credID),
		"type":  "public-key",
		"response": map[string]any{
			"clientDataJSON":    b64.EncodeToString(cdJSON),
			"authenticatorData": b64.EncodeToString(auth),
			"signature":         b64.EncodeToString(sig),
			"userHandle":        b64.EncodeToString(a.userHandle),
		},
		"clientExtensionResults":  map[string]any{},
		"authenticatorAttachment": "platform",
	}
}

func (a *softAuthenticator) json(v any) []byte {
	a.t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		a.t.Fatal(err)
	}
	return b
}
