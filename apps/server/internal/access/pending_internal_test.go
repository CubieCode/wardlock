// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package access

import (
	"fmt"
	"testing"
	"time"
)

func TestPending(t *testing.T) {
	start := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	life := time.Minute
	p := newPending[int](10, 4)
	put := func(key, site string, at time.Time) bool { return p.put(key, site, 1, at.Add(life), at) }

	// A site's share fills first; other sites are unaffected.
	for i := range 4 {
		if !put(fmt.Sprint("a", i), "site-a", start) {
			t.Fatalf("site-a entry %d refused", i)
		}
	}
	if put("a4", "site-a", start) {
		t.Fatal("site-a went over its share")
	}
	for i := range 4 {
		if !put(fmt.Sprint("b", i), "site-b", start) {
			t.Fatalf("site-b entry %d refused", i)
		}
	}
	// Taking an entry frees its site's slot, once.
	if _, ok := p.take("a0", start); !ok {
		t.Fatal("take a0")
	}
	if _, ok := p.take("a0", start); ok {
		t.Fatal("a0 taken twice")
	}
	if !put("a5", "site-a", start) {
		t.Fatal("site-a slot not freed by take")
	}
	// The whole table is bounded too.
	put("c0", "site-c", start)
	put("c1", "site-c", start)
	if put("d0", "site-d", start) {
		t.Fatal("table went over its bound")
	}
	// Expired entries go in expiry order, without a scan, and free room.
	later := start.Add(life)
	if _, ok := p.take("b1", later); ok {
		t.Fatal("an expired entry was taken")
	}
	if len(p.entries) != 0 || len(p.sites) != 0 {
		t.Fatalf("after expiry: %d entries, sites %v", len(p.entries), p.sites)
	}
	if !put("d0", "site-d", later) {
		t.Fatal("expired entries did not free room")
	}
}

func TestPendingQueueBounded(t *testing.T) {
	start := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	p := newPending[int](5, 5)
	// Put-and-take churn leaves taken keys queued until they expire; the
	// queue never grows past twice the table's bound.
	n := 0
	for i := range 100 {
		k := fmt.Sprint(i)
		if p.put(k, "site", 1, start.Add(time.Minute), start) {
			n++
			p.take(k, start)
		}
	}
	if n != 10 || len(p.queue)-p.head != 10 {
		t.Fatalf("%d puts accepted, %d queued; want 10 and 10", n, len(p.queue)-p.head)
	}
	// Once they expire the queue empties and is reclaimed over time.
	p.expire(start.Add(time.Minute))
	if len(p.queue)-p.head != 0 {
		t.Fatalf("%d still queued after expiry", len(p.queue)-p.head)
	}
}
