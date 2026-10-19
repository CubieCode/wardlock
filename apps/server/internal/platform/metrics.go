// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package platform

import (
	"bytes"
	"context"
	"math"
	"net/http"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// durationBuckets are the upper bounds, in seconds, of the request
// duration histogram.
var durationBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// Metrics counts requests on the public listeners and renders everything
// in the Prometheus text exposition format (version 0.0.4) for the
// operator listener. Labels are bounded: status classes, check names and
// managed certificate names, never hosts, paths or sites.
type Metrics struct {
	start      time.Time
	inFlight   atomic.Int64
	byClass    [6]atomic.Uint64 // index status/100; 0 holds anything outside 1xx to 5xx
	buckets    []atomic.Uint64  // cumulative counts are computed at render time
	count      atomic.Uint64
	sumNanos   atomic.Int64
	collectors []func(context.Context, *Exposition)
}

// NewMetrics starts counting from now.
func NewMetrics(now time.Time) *Metrics {
	return &Metrics{start: now, buckets: make([]atomic.Uint64, len(durationBuckets)+1)}
}

// Collect adds fn to the families rendered on every scrape, after the
// request and runtime families. fn runs on the scraping goroutine.
func (m *Metrics) Collect(fn func(context.Context, *Exposition)) {
	m.collectors = append(m.collectors, fn)
}

// Instrument counts every request next answers. A handler that panics
// counts as a 500, and the panic carries on to net/http.
func (m *Metrics) Instrument(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		m.inFlight.Add(1)
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		defer func() {
			status := rec.status
			p := recover()
			if p != nil {
				status = http.StatusInternalServerError
			}
			m.inFlight.Add(-1)
			m.observe(status, time.Since(start))
			if p != nil {
				panic(p)
			}
		}()
		next.ServeHTTP(rec, r)
	})
}

func (m *Metrics) observe(status int, d time.Duration) {
	class := status / 100
	if class < 1 || class > 5 {
		class = 0
	}
	m.byClass[class].Add(1)
	i, _ := slices.BinarySearch(durationBuckets, d.Seconds())
	m.buckets[i].Add(1)
	m.count.Add(1)
	m.sumNanos.Add(d.Nanoseconds())
}

// Handler renders the metrics, including one wardlock_ready sample per
// readiness check.
func (m *Metrics) Handler(ready *Readiness) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e := &Exposition{}
		m.render(r.Context(), e, ready)
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(e.buf.Bytes())
	})
}

func (m *Metrics) render(ctx context.Context, e *Exposition, ready *Readiness) {
	e.Gauge("wardlock_build_info", "Version of the running wardlockd.", Sample{Labels: []string{"version", Version}, Value: 1})
	e.Gauge("wardlock_start_time_seconds", "When wardlockd started, in Unix seconds.", Sample{Value: float64(m.start.UnixMilli()) / 1000})

	var classes []Sample
	for i := range m.byClass {
		if n := m.byClass[i].Load(); n > 0 {
			code := "other"
			if i > 0 {
				code = strconv.Itoa(i) + "xx"
			}
			classes = append(classes, Sample{Labels: []string{"code", code}, Value: float64(n)})
		}
	}
	e.Counter("wardlock_http_requests_total", "Requests answered on the site, dashboard and API listeners, by status class.", classes...)
	e.Gauge("wardlock_http_requests_in_flight", "Requests being answered on the site, dashboard and API listeners.", Sample{Value: float64(m.inFlight.Load())})
	e.histogram("wardlock_http_request_duration_seconds", "Time to answer a request on the site, dashboard and API listeners.", m)

	if ready != nil {
		var samples []Sample
		for _, res := range ready.Run(ctx) {
			v := 1.0
			if res.Err != nil {
				v = 0
			}
			samples = append(samples, Sample{Labels: []string{"check", res.Name}, Value: v})
		}
		e.Gauge("wardlock_ready", "1 when the readiness check passes, 0 when it fails.", samples...)
	}

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	e.Gauge("go_goroutines", "Goroutines that currently exist.", Sample{Value: float64(runtime.NumGoroutine())})
	e.Gauge("go_memstats_heap_alloc_bytes", "Bytes of allocated heap objects.", Sample{Value: float64(ms.HeapAlloc)})
	e.Gauge("go_memstats_sys_bytes", "Bytes of memory obtained from the operating system.", Sample{Value: float64(ms.Sys)})
	e.Counter("go_gc_cycles_total", "Completed garbage collection cycles.", Sample{Value: float64(ms.NumGC)})

	for _, c := range m.collectors {
		c(ctx, e)
	}
}

// Sample is one value of a metric family. Labels alternate names and
// values.
type Sample struct {
	Labels []string
	Value  float64
}

// Exposition accumulates metric families in the Prometheus text format.
type Exposition struct{ buf bytes.Buffer }

// Gauge writes a gauge family. A family with no samples writes nothing.
func (e *Exposition) Gauge(name, help string, samples ...Sample) {
	e.family(name, "gauge", help, samples)
}

// Counter writes a counter family; name should end in _total.
func (e *Exposition) Counter(name, help string, samples ...Sample) {
	e.family(name, "counter", help, samples)
}

func (e *Exposition) family(name, kind, help string, samples []Sample) {
	if len(samples) == 0 {
		return
	}
	e.header(name, kind, help)
	for _, s := range samples {
		e.sample(name, s.Labels, s.Value)
	}
}

func (e *Exposition) header(name, kind, help string) {
	e.buf.WriteString("# HELP " + name + " " + escapeHelp(help) + "\n")
	e.buf.WriteString("# TYPE " + name + " " + kind + "\n")
}

func (e *Exposition) histogram(name, help string, m *Metrics) {
	e.header(name, "histogram", help)
	var cum uint64
	for i, le := range durationBuckets {
		cum += m.buckets[i].Load()
		e.sample(name+"_bucket", []string{"le", formatFloat(le)}, float64(cum))
	}
	count := m.count.Load()
	e.sample(name+"_bucket", []string{"le", "+Inf"}, float64(count))
	e.sample(name+"_sum", nil, float64(m.sumNanos.Load())/1e9)
	e.sample(name+"_count", nil, float64(count))
}

func (e *Exposition) sample(name string, labels []string, v float64) {
	e.buf.WriteString(name)
	if len(labels) > 0 {
		e.buf.WriteByte('{')
		for i := 0; i+1 < len(labels); i += 2 {
			if i > 0 {
				e.buf.WriteByte(',')
			}
			e.buf.WriteString(labels[i] + `="` + escapeLabel(labels[i+1]) + `"`)
		}
		e.buf.WriteByte('}')
	}
	e.buf.WriteString(" " + formatFloat(v) + "\n")
}

func formatFloat(v float64) string {
	switch {
	case math.IsInf(v, 1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	case math.IsNaN(v):
		return "NaN"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

var (
	labelEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	helpEscaper  = strings.NewReplacer(`\`, `\\`, "\n", `\n`)
)

func escapeLabel(s string) string { return labelEscaper.Replace(s) }
func escapeHelp(s string) string  { return helpEscaper.Replace(s) }
