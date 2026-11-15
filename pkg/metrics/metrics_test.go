package metrics

import (
	"strings"
	"testing"
)

func TestExpositionHasTypesAndRuntime(t *testing.T) {
	r := NewRegistry()
	Default = r
	IncHTTPRequest("GET", "/api/v1/works", 200)
	IncHTTPRequest("GET", "/api/v1/works", 500)
	IncCacheRequest("hit")
	ObserveHTTPDuration("/api/v1/works", 0.03)

	var b strings.Builder
	r.WritePrometheus(&b)
	out := b.String()

	for _, want := range []string{
		"# TYPE http_requests_total counter\n",
		`http_requests_total{method="GET",route="/api/v1/works",status="500"} 1`,
		"# TYPE cache_requests_total counter\n",
		`http_request_duration_seconds_bucket{route="/api/v1/works",le="0.05"} 1`,
		`http_request_duration_seconds_bucket{route="/api/v1/works",le="0.025"} 0`,
		"go_goroutines ", "go_memstats_heap_alloc_bytes ", "process_start_time_seconds ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("exposition is missing %q", want)
		}
	}
	if n := strings.Count(out, "# TYPE http_requests_total"); n != 1 {
		t.Errorf("TYPE line for a family must appear once, got %d", n)
	}
}
