// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// Package countrytest writes tiny synthetic MaxMind DB (MMDB) files for
// tests of the country lookup (V5.13), so no real DB-IP or MaxMind data is
// ever committed. It follows the MaxMind DB format specification
// (https://maxmind.github.io/MaxMind-DB/): a binary search tree of 24-bit
// records over 128-bit addresses (IPv4 under ::/96), a 16-byte separator,
// a data section of records shaped like DB-IP's and GeoLite2's country
// databases ({"country": {"iso_code": "NZ"}}) and the metadata map.
package countrytest

import (
	"fmt"
	"maps"
	"math/big"
	"net/netip"
	"os"
	"slices"
)

// Networks maps a prefix, such as "203.0.113.0/24" or "2001:db8:1::/48",
// to the country.iso_code its record holds. An empty code writes a record
// with a continent but no country, as a database may for an address
// assigned to a region.
type Networks map[string]string

// Build returns an IPv6 database (ip_version 6) holding networks.
// Prefixes must not overlap. It panics on a bad prefix: it is test code.
func Build(networks Networks) []byte {
	t := &tree{nodes: []node{{empty, empty}}}
	var records [][]byte
	for _, p := range slices.Sorted(maps.Keys(networks)) {
		prefix := netip.MustParsePrefix(p).Masked()
		addr, bits := prefix.Addr(), prefix.Bits()
		if addr.Is4() {
			addr, bits = netip.AddrFrom16(addr.As16()), bits+96
			// As16 of an IPv4 address is ::ffff:a.b.c.d; the database
			// keeps IPv4 under ::/96, as real ones do.
			b := addr.As16()
			b[10], b[11] = 0, 0
			addr = netip.AddrFrom16(b)
		}
		t.insert(addr.As16(), bits, len(records))
		records = append(records, record(networks[p]))
	}

	// Each data record is written once, at the offset the tree points to.
	var data []byte
	offsets := make([]int, len(records))
	for i, r := range records {
		offsets[i] = len(data)
		data = append(data, r...)
	}
	nodeCount := len(t.nodes)
	value := func(r int) int {
		switch {
		case r == empty:
			return nodeCount
		case r <= dataBase:
			return nodeCount + 16 + offsets[dataBase-r]
		}
		return r
	}
	var out []byte
	for _, n := range t.nodes {
		for _, r := range n {
			out = append(out, bigEndian(value(r), 3)...)
		}
	}
	out = append(out, make([]byte, 16)...)
	out = append(out, data...)
	out = append(out, "\xab\xcd\xefMaxMind.com"...)
	out = append(out, mapOf(
		"binary_format_major_version", uintv(5, 2),
		"binary_format_minor_version", uintv(5, 0),
		"build_epoch", uintv(9, 1_790_000_000),
		"database_type", str("Wardlock-Test-Country"),
		"description", mapOf("en", str("Synthetic test data")),
		"ip_version", uintv(5, 6),
		"languages", array(str("en")),
		"node_count", uintv(6, nodeCount),
		"record_size", uintv(5, 24),
	)...)
	return out
}

// Write builds a database holding networks at path, replacing any file
// there in place.
func Write(path string, networks Networks) error {
	if err := os.WriteFile(path, Build(networks), 0o600); err != nil {
		return fmt.Errorf("write test country database: %w", err)
	}
	return nil
}

// Tree records: empty, a node index (>= 0, below dataBase's magnitude) or
// a data record (dataBase - i for record i).
const (
	empty    = -1
	dataBase = -2
)

type node [2]int

type tree struct{ nodes []node }

// insert points the record bits deep along addr at data record i,
// creating nodes on the way.
func (t *tree) insert(addr [16]byte, bits, i int) {
	n := 0
	for depth := range bits {
		bit := addr[depth/8] >> (7 - depth%8) & 1
		if depth == bits-1 {
			if t.nodes[n][bit] != empty {
				panic("countrytest: overlapping networks")
			}
			t.nodes[n][bit] = dataBase - i
			return
		}
		next := t.nodes[n][bit]
		if next == empty {
			next = len(t.nodes)
			t.nodes = append(t.nodes, node{empty, empty})
			t.nodes[n][bit] = next
		} else if next <= dataBase {
			panic("countrytest: overlapping networks")
		}
		n = next
	}
}

func record(iso string) []byte {
	if iso == "" {
		return mapOf("continent", mapOf("code", str("OC")))
	}
	return mapOf("country", mapOf("iso_code", str(iso)))
}

// MaxMind DB data encoding: a control byte holding the type in its top
// three bits (0 for an extended type, given in the next byte as type - 7)
// and the size in its low five; every value here is small enough for a
// size below 29.

func ctrl(typ, size int) []byte {
	if size >= 29 {
		panic("countrytest: value too large")
	}
	if typ <= 7 {
		return bigEndian(typ<<5|size, 1)
	}
	return append(bigEndian(size, 1), bigEndian(typ-7, 1)...)
}

// bigEndian is v in exactly n bytes; it panics when v does not fit.
func bigEndian(v, n int) []byte {
	return big.NewInt(int64(v)).FillBytes(make([]byte, n))
}

func str(s string) []byte { return append(ctrl(2, len(s)), s...) }

// uintv encodes v as an unsigned integer of MaxMind DB type typ (5
// uint16, 6 uint32, 9 uint64) in as few bytes as it needs.
func uintv(typ, v int) []byte {
	b := big.NewInt(int64(v)).Bytes()
	return append(ctrl(typ, len(b)), b...)
}

func array(items ...[]byte) []byte {
	out := ctrl(11, len(items))
	for _, item := range items {
		out = append(out, item...)
	}
	return out
}

// mapOf takes alternating string keys and encoded values.
func mapOf(kv ...any) []byte {
	out := ctrl(7, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		out = append(out, str(kv[i].(string))...)
		out = append(out, kv[i+1].([]byte)...)
	}
	return out
}
