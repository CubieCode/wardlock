// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// ReadyTimeout bounds how long Run waits for a check. A check still
// running then reports ErrCheckTimeout; it keeps running in the background
// and later runs wait on it rather than starting another.
const ReadyTimeout = 5 * time.Second

// ErrCheckTimeout is the result of a check that did not finish in time.
var ErrCheckTimeout = errors.New("timeout")

// Check is one readiness check. Name labels it in /readyz, metrics and logs.
type Check struct {
	Name string
	Run  func(ctx context.Context) error
}

// CheckResult is the outcome of one Check; Err is nil when it passed.
type CheckResult struct {
	Name string
	Err  error
}

// Readiness runs the server's readiness checks: the database, the blob
// directory and, in automatic TLS mode, the managed certificates.
type Readiness struct {
	checks   []Check
	log      *slog.Logger
	shutdown atomic.Bool

	mu       sync.Mutex
	failing  map[string]bool
	inFlight map[string]*flight
}

// flight is one running check; done closes once err is set.
type flight struct {
	done chan struct{}
	err  error
}

// NewReadiness returns readiness over checks, logging when one starts
// failing and when it recovers.
func NewReadiness(log *slog.Logger, checks ...Check) *Readiness {
	return &Readiness{checks: checks, log: log, failing: map[string]bool{}, inFlight: map[string]*flight{}}
}

// Shutdown marks the server as shutting down: from now on Handler answers
// 503. platform.Serve calls it as graceful shutdown begins.
func (r *Readiness) Shutdown() { r.shutdown.Store(true) }

// Run runs every check concurrently and returns the results in the order
// the checks were given. It waits at most ReadyTimeout (or until ctx ends,
// if sooner); a check still running then reports ErrCheckTimeout. A check
// that is already running from an earlier call is waited on, not started
// again, so a check that hangs (a blob directory on a stuck network mount
// ignores ctx) costs one goroutine, not one per scrape.
func (r *Readiness) Run(ctx context.Context) []CheckResult {
	ctx, cancel := context.WithTimeout(ctx, ReadyTimeout)
	defer cancel()
	flights := make([]*flight, len(r.checks))
	for i, c := range r.checks {
		flights[i] = r.start(ctx, c)
	}
	results := make([]CheckResult, len(r.checks))
	for i, f := range flights {
		results[i].Name = r.checks[i].Name
		select {
		case <-f.done:
			results[i].Err = f.err
		case <-ctx.Done():
			results[i].Err = ErrCheckTimeout
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, res := range results {
		switch was := r.failing[res.Name]; {
		case res.Err != nil && !was:
			r.log.LogAttrs(ctx, slog.LevelWarn, "readiness check failing", slog.String("check", res.Name), slog.String("error", res.Err.Error()))
		case res.Err == nil && was:
			r.log.LogAttrs(ctx, slog.LevelInfo, "readiness check recovered", slog.String("check", res.Name))
		}
		r.failing[res.Name] = res.Err != nil
	}
	return results
}

// start returns c's running flight, starting one if none is. The check
// gets its own ReadyTimeout, detached from the caller, since later callers
// may wait on it.
func (r *Readiness) start(ctx context.Context, c Check) *flight {
	r.mu.Lock()
	defer r.mu.Unlock()
	if f := r.inFlight[c.Name]; f != nil {
		return f
	}
	f := &flight{done: make(chan struct{})}
	r.inFlight[c.Name] = f
	go func() {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ReadyTimeout)
		defer cancel()
		f.err = c.Run(cctx)
		r.mu.Lock()
		delete(r.inFlight, c.Name)
		r.mu.Unlock()
		close(f.done)
	}()
	return f
}

// Handler answers /readyz: 200 when every check passes, 503 otherwise, with
// each check's outcome, and 503 without running them once shutdown has
// begun. It is served only on the operator listener, so the reasons (paths,
// certificate names) reach operators and not visitors.
func (r *Readiness) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body := struct {
			Status string            `json:"status"`
			Checks map[string]string `json:"checks"`
		}{Status: "ready", Checks: map[string]string{}}
		if r.shutdown.Load() {
			body.Status = "shutting_down"
		} else {
			for _, res := range r.Run(req.Context()) {
				body.Checks[res.Name] = "ok"
				if res.Err != nil {
					body.Status = "unready"
					body.Checks[res.Name] = res.Err.Error()
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if body.Status != "ready" {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(body)
	})
}

// DirWritable checks that dir is a directory this process can create files
// in, by creating and removing a probe file named .ready-*. Deploy blob
// garbage collection only looks under dir/sha256, so it never sees one.
func DirWritable(dir string) func(context.Context) error {
	return func(context.Context) error {
		info, err := os.Stat(dir)
		if err != nil {
			return fmt.Errorf("blob directory: %w", err)
		}
		if !info.IsDir() {
			return errors.New("blob directory: not a directory")
		}
		f, err := os.CreateTemp(dir, ".ready-*")
		if err != nil {
			return fmt.Errorf("blob directory not writable: %w", err)
		}
		name := f.Name()
		cerr := f.Close()
		if err := os.Remove(name); err != nil {
			return fmt.Errorf("blob directory: %w", err)
		}
		if cerr != nil {
			return fmt.Errorf("blob directory: %w", cerr)
		}
		return nil
	}
}

// OperatorHandler serves the operator listener (WARDLOCK_OPERATOR_ADDR):
// /healthz, /readyz and /metrics, and 404 for everything else, on any Host.
// None of these are reachable through the edge's listeners.
func OperatorHandler(ready *Readiness, metrics *Metrics) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /healthz", Healthz())
	mux.Handle("GET /readyz", ready.Handler())
	mux.Handle("GET /metrics", metrics.Handler(ready))
	return mux
}
