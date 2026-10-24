package metrics

import (
	"strings"
	"testing"
)

func TestRender(t *testing.T) {
	r := NewRegistry()
	c := r.Counter("http_requests_total", "Requests", "route", "status")
	h := r.Histogram("lat_seconds", "Latency", []float64{0.1, 1}, "route")
	g := r.Gauge("pool_conns", "Conns")
	c.Inc("/a", "200")
	c.Add(2, "/a", "200")
	h.Observe(0.05, "/a")
	h.Observe(0.5, "/a")
	r.OnCollect(func() { g.Set(7) })
	out := r.Render()
	for _, want := range []string{
		`http_requests_total{route="/a",status="200"} 3`,
		`lat_seconds_bucket{route="/a",le="0.1"} 1`,
		`lat_seconds_bucket{route="/a",le="1"} 2`,
		`lat_seconds_bucket{route="/a",le="+Inf"} 2`,
		`lat_seconds_count{route="/a"} 2`,
		`pool_conns 7`,
		"# TYPE lat_seconds histogram",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}
