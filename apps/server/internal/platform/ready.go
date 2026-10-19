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
	"time"
)

// ReadyTimeout bounds one readiness check.
const ReadyTimeout = 5 * time.Second

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
	checks []Check
	log    *slog.Logger

	mu      sync.Mutex
	failing map[string]bool
}

// NewReadiness returns readiness over checks, logging when one starts
// failing and when it recovers.
func NewReadiness(log *slog.Logger, checks ...Check) *Readiness {
	return &Readiness{checks: checks, log: log, failing: map[string]bool{}}
}

// Run runs every check concurrently, each within ReadyTimeout, and returns
// the results in the order the checks were given.
func (r *Readiness) Run(ctx context.Context) []CheckResult {
	results := make([]CheckResult, len(r.checks))
	var wg sync.WaitGroup
	for i, c := range r.checks {
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(ctx, ReadyTimeout)
			defer cancel()
			results[i] = CheckResult{Name: c.Name, Err: c.Run(ctx)}
		})
	}
	wg.Wait()
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

// Handler answers /readyz: 200 when every check passes, 503 otherwise, with
// each check's outcome. It is served only on the operator listener, so the
// reasons (paths, certificate names) reach operators and not visitors.
func (r *Readiness) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body := struct {
			Status string            `json:"status"`
			Checks map[string]string `json:"checks"`
		}{Status: "ready", Checks: map[string]string{}}
		for _, res := range r.Run(req.Context()) {
			body.Checks[res.Name] = "ok"
			if res.Err != nil {
				body.Status = "unready"
				body.Checks[res.Name] = res.Err.Error()
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
