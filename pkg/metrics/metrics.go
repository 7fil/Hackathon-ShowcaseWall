// Package metrics provides lock-free Prometheus counters, gauges and histograms
// and exposes them in Prometheus text format on the internal metrics port (:9100).
package metrics

import (
	"fmt"
	"io"
	"net/http"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var processStart = time.Now()

var Default = NewRegistry()

type Registry struct {
	mu         sync.RWMutex
	counters   map[string]*int64
	gauges     map[string]*int64
	histograms map[string]*histogram
}

type histogram struct {
	mu      sync.Mutex
	buckets []float64
	counts  []uint64
	sum     float64
	total   uint64
}

var defaultBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0}

func NewRegistry() *Registry {
	return &Registry{
		counters:   make(map[string]*int64),
		gauges:     make(map[string]*int64),
		histograms: make(map[string]*histogram),
	}
}

func (r *Registry) counterPtr(key string) *int64 {
	r.mu.RLock()
	p, ok := r.counters[key]
	r.mu.RUnlock()
	if ok {
		return p
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if p, ok = r.counters[key]; ok {
		return p
	}
	var v int64
	r.counters[key] = &v
	return &v
}

func (r *Registry) gaugePtr(key string) *int64 {
	r.mu.RLock()
	p, ok := r.gauges[key]
	r.mu.RUnlock()
	if ok {
		return p
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if p, ok = r.gauges[key]; ok {
		return p
	}
	var v int64
	r.gauges[key] = &v
	return &v
}

func (r *Registry) histPtr(key string) *histogram {
	r.mu.RLock()
	h, ok := r.histograms[key]
	r.mu.RUnlock()
	if ok {
		return h
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if h, ok = r.histograms[key]; ok {
		return h
	}
	h = &histogram{
		buckets: defaultBuckets,
		counts:  make([]uint64, len(defaultBuckets)),
	}
	r.histograms[key] = h
	return h
}

// ---- Spec Metrics Helpers ----

func IncHTTPRequest(method, route string, status int) {
	key := fmt.Sprintf(`http_requests_total{method=%q,route=%q,status="%d"}`, method, route, status)
	atomic.AddInt64(Default.counterPtr(key), 1)
}

func ObserveHTTPDuration(route string, seconds float64) {
	h := Default.histPtr(route)
	h.mu.Lock()
	for i, b := range h.buckets {
		if seconds <= b {
			h.counts[i]++
		}
	}
	h.sum += seconds
	h.total++
	h.mu.Unlock()
}

func AddWSActiveConnections(delta int64) {
	atomic.AddInt64(Default.gaugePtr("ws_active_connections"), delta)
}

func WSActiveConnections() int64 {
	return atomic.LoadInt64(Default.gaugePtr("ws_active_connections"))
}

func IncWSEventsPublished() {
	atomic.AddInt64(Default.counterPtr("ws_events_published_total"), 1)
}

func AddWSEventsDelivered(n int64) {
	if n > 0 {
		atomic.AddInt64(Default.counterPtr("ws_events_delivered_total"), n)
	}
}

func IncWSHandshakeRejected(reason string) {
	key := fmt.Sprintf(`ws_handshakes_rejected_total{reason=%q}`, reason)
	atomic.AddInt64(Default.counterPtr(key), 1)
}

func IncWSClientDropped(reason string) {
	key := fmt.Sprintf(`ws_clients_dropped_total{reason=%q}`, reason)
	atomic.AddInt64(Default.counterPtr(key), 1)
}

func IncLoginAttempt(result string) {
	key := fmt.Sprintf(`login_attempts_total{result=%q}`, result)
	atomic.AddInt64(Default.counterPtr(key), 1)
}

func IncOTPRequest(result string) {
	key := fmt.Sprintf(`otp_requests_total{result=%q}`, result)
	atomic.AddInt64(Default.counterPtr(key), 1)
}

func IncOTPVerification(result string) {
	key := fmt.Sprintf(`otp_verifications_total{result=%q}`, result)
	atomic.AddInt64(Default.counterPtr(key), 1)
}

func IncWorksAction(action string) {
	key := fmt.Sprintf(`works_total{action=%q}`, action)
	atomic.AddInt64(Default.counterPtr(key), 1)
}

func IncCacheRequest(result string) {
	key := fmt.Sprintf(`cache_requests_total{result=%q}`, result)
	atomic.AddInt64(Default.counterPtr(key), 1)
}

func IncMediaRequest(result string) {
	key := fmt.Sprintf(`media_requests_total{result=%q}`, result)
	atomic.AddInt64(Default.counterPtr(key), 1)
}

// Handler returns an http.Handler serving the Prometheus text exposition format.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		Default.WritePrometheus(w)
	})
}

func (r *Registry) WritePrometheus(w io.Writer) {
	var b strings.Builder

	b.WriteString("# TYPE ws_active_connections gauge\n")
	fmt.Fprintf(&b, "ws_active_connections %d\n", atomic.LoadInt64(r.gaugePtr("ws_active_connections")))

	b.WriteString("# TYPE ws_events_published_total counter\n")
	fmt.Fprintf(&b, "ws_events_published_total %d\n", atomic.LoadInt64(r.counterPtr("ws_events_published_total")))

	b.WriteString("# TYPE ws_events_delivered_total counter\n")
	fmt.Fprintf(&b, "ws_events_delivered_total %d\n", atomic.LoadInt64(r.counterPtr("ws_events_delivered_total")))

	r.mu.RLock()
	keys := make([]string, 0, len(r.counters))
	for k := range r.counters {
		if k == "ws_events_published_total" || k == "ws_events_delivered_total" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lastFamily := ""
	for _, k := range keys {
		family := k
		if i := strings.IndexByte(k, '{'); i >= 0 {
			family = k[:i]
		}
		if family != lastFamily {
			fmt.Fprintf(&b, "# TYPE %s counter\n", family)
			lastFamily = family
		}
		val := atomic.LoadInt64(r.counters[k])
		fmt.Fprintf(&b, "%s %d\n", k, val)
	}

	routes := make([]string, 0, len(r.histograms))
	for route := range r.histograms {
		routes = append(routes, route)
	}
	sort.Strings(routes)
	if len(routes) > 0 {
		b.WriteString("# TYPE http_request_duration_seconds histogram\n")
	}
	for _, route := range routes {
		h := r.histograms[route]
		h.mu.Lock()
		for i, le := range h.buckets {
			fmt.Fprintf(&b, "http_request_duration_seconds_bucket{route=%q,le=\"%g\"} %d\n", route, le, h.counts[i])
		}
		fmt.Fprintf(&b, "http_request_duration_seconds_bucket{route=%q,le=\"+Inf\"} %d\n", route, h.total)
		fmt.Fprintf(&b, "http_request_duration_seconds_sum{route=%q} %.6f\n", route, h.sum)
		fmt.Fprintf(&b, "http_request_duration_seconds_count{route=%q} %d\n", route, h.total)
		h.mu.Unlock()
	}
	r.mu.RUnlock()

	writeRuntime(&b)
	_, _ = io.WriteString(w, b.String())
}

// writeRuntime adds process and Go runtime gauges so the dashboard can show
// uptime, memory and goroutines without an extra exporter.
func writeRuntime(b *strings.Builder) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Fprintf(b, "# TYPE process_start_time_seconds gauge\nprocess_start_time_seconds %d\n", processStart.Unix())
	fmt.Fprintf(b, "# TYPE go_goroutines gauge\ngo_goroutines %d\n", runtime.NumGoroutine())
	fmt.Fprintf(b, "# TYPE go_memstats_heap_alloc_bytes gauge\ngo_memstats_heap_alloc_bytes %d\n", m.HeapAlloc)
	fmt.Fprintf(b, "# TYPE go_memstats_sys_bytes gauge\ngo_memstats_sys_bytes %d\n", m.Sys)
	fmt.Fprintf(b, "# TYPE go_gc_cycles_total counter\ngo_gc_cycles_total %d\n", m.NumGC)
}
