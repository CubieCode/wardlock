// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package moderation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// TestQuarantineLoadKeepsAdds checks a reload whose list was read before
// the worker added a hash keeps that hash, while one begun after the add
// is the truth (a release removes it).
func TestQuarantineLoadKeepsAdds(t *testing.T) {
	a, b := strings.Repeat("a", 64), strings.Repeat("b", 64)
	q := NewQuarantine()
	// The worker quarantines a while the load's list is being read: the
	// list does not have it yet.
	err := q.Load(t.Context(), func(context.Context) ([]string, error) {
		q.add(a)
		return []string{b}, nil
	})
	if err != nil || !q.Has(a) || !q.Has(b) || q.Len() != 2 {
		t.Fatalf("after the racing load: %v, a %v, b %v", err, q.Has(a), q.Has(b))
	}
	// The next load began after the add, so its list decides: a was
	// released.
	if err := q.Load(t.Context(), func(context.Context) ([]string, error) { return []string{b}, nil }); err != nil {
		t.Fatal(err)
	}
	if q.Has(a) || !q.Has(b) {
		t.Fatalf("after a later load: a %v, b %v", q.Has(a), q.Has(b))
	}
	// A failed load reports and keeps the set.
	if err := q.Load(t.Context(), func(context.Context) ([]string, error) { return nil, errors.New("database gone") }); err == nil || !q.Has(b) {
		t.Fatalf("failed load: %v, b %v", err, q.Has(b))
	}
}

// TestQuarantineConcurrent runs adds against reloads (under -race): no
// added hash is ever lost to a reload whose list predates it.
func TestQuarantineConcurrent(t *testing.T) {
	q := NewQuarantine()
	var mu sync.Mutex
	var stored []string // what the database holds
	list := func(context.Context) ([]string, error) {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), stored...), nil
	}
	var wg sync.WaitGroup
	done := make(chan struct{})
	wg.Go(func() {
		for {
			select {
			case <-done:
				return
			default:
				_ = q.Load(t.Context(), list)
			}
		}
	})
	for i := range 500 {
		h := fmt.Sprintf("%064x", i)
		// As the worker does: record, then add.
		mu.Lock()
		stored = append(stored, h)
		mu.Unlock()
		q.add(h)
		if !q.Has(h) {
			t.Fatalf("hash %d lost right after its add", i)
		}
	}
	close(done)
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	for _, h := range stored {
		if !q.Has(h) {
			t.Fatalf("lost %s", h)
		}
	}
}
