// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package countrytest_test

import (
	"bytes"
	"flag"
	"os"
	"testing"

	"wardlock.nz/server/internal/country/countrytest"
)

var update = flag.Bool("update", false, "rewrite testdata/acceptance.mmdb")

// The database `pnpm browser:test` gives its server (V5.14): every request
// a browser spec makes comes from 127.0.0.1, which it places in NZ, and a
// spec that connects from 127.0.1.1 lands in Iceland, at the map's left
// edge. The countries are made up; loopback is in no country.
const acceptanceFile = "testdata/acceptance.mmdb"

var acceptanceNetworks = countrytest.Networks{"127.0.0.0/24": "NZ", "127.0.1.0/24": "IS"}

func TestAcceptanceDatabase(t *testing.T) {
	want := countrytest.Build(acceptanceNetworks)
	if *update {
		if err := os.WriteFile(acceptanceFile, want, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(acceptanceFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s is stale: go test ./internal/country/countrytest -update", acceptanceFile)
	}
}
