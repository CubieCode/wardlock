// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package tls

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
)

// TestZapLoggerDropsCredentials logs, through the adapter, every kind of
// credential-bearing field certmagic and acmez emit or could emit, and
// checks that only the allowlisted fields reach the output.
func TestZapLoggerDropsCredentials(t *testing.T) {
	const (
		contact  = acmeContact
		keyPEM   = "-----BEGIN EC PRIVATE KEY-----\nMHcCAQEEIFakeKeyMaterialNotReal0123456789abcdef\n-----END EC PRIVATE KEY-----"
		bearer   = "fake-provider-token-0123456789"
		keyAuth  = "fakeChallengeToken0123.fakeAccountThumbprint0123"
		txtValue = "fakeDNS01DigestValue0123456789abcdef"
		eabMAC   = "fakeEABMacKey0123456789"
	)
	var buf syncBuffer
	log := zapLogger(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	headers := http.Header{"Authorization": {"Bearer " + bearer}, "Replay-Nonce": {"fakeNonce0123456789"}}
	log.Named("acme_client").With(zap.String("account", contact)).Debug("http request",
		zap.String("identifier", "docs.sites.test"),
		zap.String("method", "POST"),
		zap.Any("headers", headers),
		zap.Any("response_headers", headers),
		zap.Strings("contact", []string{"mailto:" + contact}),
		zap.String("key_location", "acme/ca/users/"+contact+"/"+contact+".key"),
		zap.String("record_data", txtValue),
		zap.String("expected_data", txtValue),
		zap.String("validation_path", "/.well-known/acme-challenge/fakeChallengeToken0123"),
		zap.String("uri", "/.well-known/acme-challenge/fakeChallengeToken0123"),
		zap.String("key_authorization", keyAuth),
		zap.String("private_key", keyPEM),
		zap.String("eab_mac_key", eabMAC),
		zap.String("remote_ip", "192.0.2.10"),
	)
	long := strings.Repeat("x", 2*maxFreeText)
	log.Error("could not solve challenge", zap.Error(errors.New("unexpected status code 401: Authorization: Bearer "+bearer+" token="+bearer+" "+keyPEM+" "+long)))
	out := buf.String()

	for _, secret := range []string{contact, "PRIVATE KEY", "FakeKeyMaterial", bearer, keyAuth, txtValue, eabMAC, "fakeNonce", "fakeChallengeToken", "192.0.2.10"} {
		if strings.Contains(out, secret) {
			t.Errorf("log output contains %q:\n%s", secret, out)
		}
	}
	for _, want := range []string{
		`"identifier":"docs.sites.test"`,
		`"method":"POST"`,
		`"dropped":["account","contact","eab_mac_key","expected_data","headers","key_authorization","key_location","private_key","record_data","remote_ip","response_headers","uri","validation_path"]`,
		`Bearer [redacted]`,
		`token [redacted]`,
		`[redacted private key]`,
		`…[truncated]`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("log output lacks %s:\n%s", want, out)
		}
	}
}

func TestScrubText(t *testing.T) {
	tests := []struct{ in, want string }{
		{"rate limited", "rate limited"},
		{"token expired", "token expired"},
		{"open /data/acme/ca/users/ops@example.test/ops.json: no such file", "open /data/acme/ca/users/[redacted email]/ops.json: no such file"},
		{"contact mailto:ops@example.test refused", "contact mailto:[redacted email] refused"},
		{"Authorization: Token abcdefgh12345678", "Authorization: Token [redacted]"},
		{"bearer=abcdefgh12345678 rest", "bearer [redacted] rest"},
		{"x -----BEGIN RSA PRIVATE KEY-----\nAAAA\n-----END RSA PRIVATE KEY----- y", "x [redacted private key] y"},
		{"x -----BEGIN PRIVATE KEY-----\nAAAA cut off", "x [redacted private key]"},
		{strings.Repeat("é", maxFreeText), strings.Repeat("é", maxFreeText/2) + "…[truncated]"},
	}
	for _, tt := range tests {
		if got := scrubText(tt.in); got != tt.want {
			t.Errorf("scrubText(%.40q) = %.80q, want %.80q", tt.in, got, tt.want)
		}
	}
}

// acmeContact is the synthetic ACME account contact TestIssuance registers.
const acmeContact = "acme-contact@example.test"

// assertNoCredentials checks the debug output TestIssuance captured
// through the adapter from a real issuance: no private key from storage
// (the ACME account's or a certificate's), no key authorisation (which
// ends in the account key's thumbprint), no DNS-01 record value, no ACME
// contact address and no HTTP headers.
func assertNoCredentials(t *testing.T, out, storageDir string, txtValues []string) {
	t.Helper()
	if !strings.Contains(out, "level=DEBUG") || !strings.Contains(out, `msg="http request"`) {
		t.Fatalf("no ACME debug output was captured:\n%s", out)
	}
	forbidden := map[string]string{acmeContact: "ACME contact", "PRIVATE KEY": "PEM private key", "Replay-Nonce": "ACME response header", "User-Agent": "request header"}
	for _, v := range txtValues {
		forbidden[v] = "DNS-01 record value"
	}
	var accountKeys int
	err := filepath.WalkDir(storageDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".key" {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		block, _ := pem.Decode(data)
		if block == nil {
			t.Fatalf("%s holds no PEM block", path)
		}
		// Any 16-character run of the key's encoding would betray it.
		enc := base64.StdEncoding.EncodeToString(block.Bytes)
		for i := 0; i+16 <= len(enc); i += 16 {
			forbidden[enc[i:i+16]] = "private key material from " + filepath.Base(path)
		}
		if strings.Contains(filepath.ToSlash(path), "/users/") {
			accountKeys++
			forbidden[thumbprint(t, block)] = "account key thumbprint (key authorisation)"
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if accountKeys == 0 {
		t.Fatalf("no ACME account key in %s", storageDir)
	}
	for s, what := range forbidden {
		if strings.Contains(out, s) {
			t.Errorf("log output contains %s (%q)", what, s)
			for line := range strings.Lines(out) {
				if strings.Contains(line, s) {
					t.Log(line)
				}
			}
		}
	}
	for line := range strings.Lines(out) {
		if strings.Contains(line, `msg="http request"`) && !strings.Contains(line, "headers") {
			t.Fatalf("an ACME request was logged without dropping its headers: %s", line)
		}
	}
}

// thumbprint is the RFC 7638 JWK thumbprint of an EC account key, as
// key authorisations carry it.
func thumbprint(t *testing.T, block *pem.Block) string {
	t.Helper()
	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		k, err2 := x509.ParsePKCS8PrivateKey(block.Bytes)
		var ok bool
		if key, ok = k.(*ecdsa.PrivateKey); err2 != nil || !ok {
			t.Fatalf("account key is not an EC key: %v", err)
		}
	}
	point, err := key.PublicKey.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	size := (len(point) - 1) / 2
	b64 := base64.RawURLEncoding.EncodeToString
	jwk := `{"crv":"` + key.Curve.Params().Name + `","kty":"EC","x":"` + b64(point[1:1+size]) + `","y":"` + b64(point[1+size:]) + `"}`
	sum := sha256.Sum256([]byte(jwk))
	return b64(sum[:])
}
