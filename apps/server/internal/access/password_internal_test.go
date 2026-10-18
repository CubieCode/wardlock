// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package access

import (
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/argon2"
)

// countingDerive wraps argon2.IDKey and counts calls.
type countingDerive struct {
	mu    sync.Mutex
	calls int
}

func (c *countingDerive) derive(pw, salt []byte, t, m uint32, p uint8, n uint32) []byte {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return argon2.IDKey(pw, salt, t, m, p, n)
}

func (c *countingDerive) take() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := c.calls
	c.calls = 0
	return n
}

func TestHashPassword(t *testing.T) {
	const pw = "harbour-lights-7"
	h := hashPassword(argon2.IDKey, pw)
	if !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("hash %q is not Argon2id with the default parameters", h)
	}
	if strings.Contains(h, pw) {
		t.Fatal("hash contains the password")
	}
	if again := hashPassword(argon2.IDKey, pw); again == h {
		t.Fatal("two hashes of one password share a salt")
	}
	parsed, ok := parseHash(h)
	if !ok || len(parsed.salt) != argonSaltLen || len(parsed.key) != argonKeyLen {
		t.Fatalf("parseHash(%q) = %+v, %v", h, parsed, ok)
	}
	for _, c := range []struct {
		name, pw string
		want     bool
	}{
		{"correct", pw, true},
		{"wrong", "harbour-lights-8", false},
		{"prefix", pw[:len(pw)-1], false},
		{"longer", pw + "!", false},
		{"case", strings.ToUpper(pw), false},
		{"empty", "", false},
	} {
		if got := verifyPassword(argon2.IDKey, h, c.pw); got != c.want {
			t.Errorf("%s: verifyPassword = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestParseHashRejects(t *testing.T) {
	good := hashPassword(argon2.IDKey, "harbour-lights-7")
	f := strings.Split(good, "$")
	for name, h := range map[string]string{
		"empty":         "",
		"argon2i":       strings.Replace(good, "argon2id", "argon2i", 1),
		"old version":   strings.Replace(good, "v=19", "v=16", 1),
		"huge memory":   strings.Replace(good, "m=19456", "m=4194304", 1),
		"many passes":   strings.Replace(good, "t=2", "t=99", 1),
		"no threads":    strings.Replace(good, "p=1", "p=0", 1),
		"bad salt":      strings.Join([]string{"", f[1], f[2], f[3], "!!", f[5]}, "$"),
		"short key":     strings.Join([]string{"", f[1], f[2], f[3], f[4], "AAAA"}, "$"),
		"extra field":   good + "$x",
		"plain text":    "harbour-lights-7",
		"stand-in hash": "$argon2id$stand-in",
	} {
		if _, ok := parseHash(h); ok {
			t.Errorf("%s: parseHash accepted %q", name, h)
		}
	}
}

// Every check derives exactly one key, whether the password is right,
// wrong, or there is no usable hash at all, and compares in constant time,
// so timing tells an attacker nothing about the password or its presence.
func TestVerifyPasswordDoesConstantWork(t *testing.T) {
	c := &countingDerive{}
	h := hashPassword(argon2.IDKey, "harbour-lights-7")
	for name, tc := range map[string]struct{ hash, pw string }{
		"correct":              {h, "harbour-lights-7"},
		"wrong first byte":     {h, "Xarbour-lights-7"},
		"wrong last byte":      {h, "harbour-lights-X"},
		"different length":     {h, "x"},
		"no password set":      {"", "harbour-lights-7"},
		"unreadable hash":      {"$argon2id$stand-in", "harbour-lights-7"},
		"empty attempt, no pw": {"", ""},
	} {
		verifyPassword(c.derive, tc.hash, tc.pw)
		if n := c.take(); n != 1 {
			t.Errorf("%s: derived %d keys, want 1", name, n)
		}
	}
	// The stand-in for a missing hash costs what a real one does.
	if ok := verifyPassword(argon2.IDKey, "", ""); ok {
		t.Fatal("an empty password matched a missing hash")
	}
}

func TestIPKey(t *testing.T) {
	for in, want := range map[string]string{
		"192.0.2.10":             "192.0.2.10",
		"::ffff:192.0.2.10":      "192.0.2.10",
		"2001:db8:1:2:3:4:5:6":   "2001:db8:1:2::/64",
		"2001:db8:1:2:ffff::1":   "2001:db8:1:2::/64",
		"fe80::1%eth0":           "fe80::/64",
		"not an address at all!": "not an address at all!",
	} {
		if got := ipKey(in); got != want {
			t.Errorf("ipKey(%q) = %q, want %q", in, got, want)
		}
	}
}
