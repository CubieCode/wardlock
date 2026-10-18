// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// Package sitestest provides an in-memory sites.Resolver for tests.
package sitestest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"wardlock.nz/server/internal/sites"
)

// Resolver is a fake DNS zone for custom-domain verification. Names are
// lowercase without a trailing dot. It answers like the Go DNS client: a
// name's canonical name is the end of its CNAME chain, which must have
// address records.
type Resolver struct {
	mu sync.Mutex
	// cname maps a name to its CNAME target, addr marks names with A or
	// AAAA records, txt holds TXT strings, and errs fails lookups of a
	// name with an error (a timeout, SERVFAIL) instead of answering.
	cname map[string]string
	addr  map[string]bool
	txt   map[string][]string
	errs  map[string]error
	// Lookups counts lookups, so tests can tell a check ran.
	lookups int
}

var _ sites.Resolver = (*Resolver)(nil)

// NewResolver returns an empty zone in which every name in addrs has
// address records (the sites wildcard's names, typically).
func NewResolver(addrs ...string) *Resolver {
	r := &Resolver{cname: map[string]string{}, addr: map[string]bool{}, txt: map[string][]string{}, errs: map[string]error{}}
	for _, a := range addrs {
		r.addr[a] = true
	}
	return r
}

// CNAME sets name's CNAME to target, replacing any other record at name.
func (r *Resolver) CNAME(name, target string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.addr, name)
	r.cname[name] = strings.TrimSuffix(strings.ToLower(target), ".")
}

// Addr gives name address records (and no CNAME).
func (r *Resolver) Addr(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.cname, name)
	r.addr[name] = true
}

// TXT sets name's TXT strings; none removes them.
func (r *Resolver) TXT(name string, values ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(values) == 0 {
		delete(r.txt, name)
		return
	}
	r.txt[name] = values
}

// Fail makes lookups of name fail with err; nil answers again.
func (r *Resolver) Fail(name string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err == nil {
		delete(r.errs, name)
		return
	}
	r.errs[name] = err
}

// Remove deletes every record at name.
func (r *Resolver) Remove(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.cname, name)
	delete(r.addr, name)
	delete(r.txt, name)
}

// Lookups is the number of lookups answered so far.
func (r *Resolver) Lookups() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lookups
}

// ErrHang, given to Fail, makes lookups of a name wait until their context
// ends, like a resolver that never answers.
var ErrHang = errors.New("hang")

// CanonicalName implements sites.Resolver.
func (r *Resolver) CanonicalName(ctx context.Context, host string) (string, error) {
	name, err := r.canonical(host)
	if err != nil {
		return "", wait(ctx, err)
	}
	return name, nil
}

func (r *Resolver) canonical(host string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lookups++
	name := strings.TrimSuffix(strings.ToLower(host), ".")
	for range 16 {
		if err := r.errs[name]; err != nil {
			return "", err
		}
		next, ok := r.cname[name]
		if !ok {
			break
		}
		name = next
	}
	if !r.addr[name] {
		return "", fmt.Errorf("CNAME %s: %w", host, sites.ErrNoRecord)
	}
	return name, nil
}

// LookupTXT implements sites.Resolver. Like DNS, it does not follow a
// CNAME at the name.
func (r *Resolver) LookupTXT(ctx context.Context, name string) ([]string, error) {
	v, err := r.lookupTXT(name)
	if err != nil {
		return nil, wait(ctx, err)
	}
	return v, nil
}

func (r *Resolver) lookupTXT(name string) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lookups++
	name = strings.TrimSuffix(strings.ToLower(name), ".")
	if err := r.errs[name]; err != nil {
		return nil, err
	}
	v, ok := r.txt[name]
	if !ok {
		return nil, fmt.Errorf("TXT %s: %w", name, sites.ErrNoRecord)
	}
	return append([]string(nil), v...), nil
}

// wait turns ErrHang into waiting for ctx; other errors pass through.
func wait(ctx context.Context, err error) error {
	if !errors.Is(err, ErrHang) {
		return err
	}
	<-ctx.Done()
	return fmt.Errorf("lookup timed out: %w", ctx.Err())
}
