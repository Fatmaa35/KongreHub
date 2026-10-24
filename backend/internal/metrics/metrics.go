// Package metrics is a tiny Prometheus text-format exporter (counters,
// gauges, histograms with labels). Bağımlılık eklemeden /metrics sağlar.
package metrics

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
)

type kind int

const (
	counter kind = iota
	gauge
	histogram
)

type series struct {
	labels  []string
	value   float64
	buckets []uint64
	sum     float64
	count   uint64
}

type metric struct {
	name, help string
	kind       kind
	labelNames []string
	bounds     []float64
	mu         sync.Mutex
	series     map[string]*series
}

// Registry holds metrics.
type Registry struct {
	mu      sync.Mutex
	metrics []*metric
	collect []func()
}

func NewRegistry() *Registry { return &Registry{} }

func (r *Registry) add(m *metric) *metric {
	r.mu.Lock()
	defer r.mu.Unlock()
	m.series = map[string]*series{}
	r.metrics = append(r.metrics, m)
	return m
}

// OnCollect registers a callback run before each scrape (gauges from pool stats).
func (r *Registry) OnCollect(f func()) { r.collect = append(r.collect, f) }

type Counter struct{ m *metric }
type Gauge struct{ m *metric }
type Histogram struct{ m *metric }

func (r *Registry) Counter(name, help string, labels ...string) Counter {
	return Counter{r.add(&metric{name: name, help: help, kind: counter, labelNames: labels})}
}
func (r *Registry) Gauge(name, help string, labels ...string) Gauge {
	return Gauge{r.add(&metric{name: name, help: help, kind: gauge, labelNames: labels})}
}
func (r *Registry) Histogram(name, help string, bounds []float64, labels ...string) Histogram {
	return Histogram{r.add(&metric{name: name, help: help, kind: histogram, labelNames: labels, bounds: bounds})}
}

// DefBuckets are latency buckets in seconds.
var DefBuckets = []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 20}

func (m *metric) get(lv []string) *series {
	if len(lv) != len(m.labelNames) {
		panic(fmt.Sprintf("metric %s: %d label values for %d names", m.name, len(lv), len(m.labelNames)))
	}
	key := strings.Join(lv, "\xff")
	s, ok := m.series[key]
	if !ok {
		s = &series{labels: append([]string(nil), lv...)}
		if m.kind == histogram {
			s.buckets = make([]uint64, len(m.bounds))
		}
		m.series[key] = s
	}
	return s
}

func (c Counter) Inc(lv ...string)            { c.Add(1, lv...) }
func (c Counter) Add(v float64, lv ...string) { c.m.mu.Lock(); c.m.get(lv).value += v; c.m.mu.Unlock() }
func (g Gauge) Set(v float64, lv ...string)   { g.m.mu.Lock(); g.m.get(lv).value = v; g.m.mu.Unlock() }
func (h Histogram) Observe(v float64, lv ...string) {
	h.m.mu.Lock()
	defer h.m.mu.Unlock()
	s := h.m.get(lv)
	for i, b := range h.m.bounds {
		if v <= b {
			s.buckets[i]++
		}
	}
	s.sum += v
	s.count++
}

func esc(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(s)
}

func labelStr(names, values []string, extra ...string) string {
	parts := []string{}
	for i, n := range names {
		parts = append(parts, fmt.Sprintf(`%s="%s"`, n, esc(values[i])))
	}
	for i := 0; i+1 < len(extra); i += 2 {
		parts = append(parts, fmt.Sprintf(`%s="%s"`, extra[i], extra[i+1]))
	}
	if len(parts) == 0 {
		return ""
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func fmtF(v float64) string {
	if math.IsInf(v, 1) {
		return "+Inf"
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%f", v), "0"), ".")
}

// WriteTo renders the exposition format.
func (r *Registry) Render() string {
	for _, f := range r.collect {
		f()
	}
	var b strings.Builder
	r.mu.Lock()
	ms := append([]*metric(nil), r.metrics...)
	r.mu.Unlock()
	for _, m := range ms {
		m.mu.Lock()
		typ := map[kind]string{counter: "counter", gauge: "gauge", histogram: "histogram"}[m.kind]
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s %s\n", m.name, m.help, m.name, typ)
		keys := make([]string, 0, len(m.series))
		for k := range m.series {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			s := m.series[k]
			switch m.kind {
			case histogram:
				for i, bound := range m.bounds {
					fmt.Fprintf(&b, "%s_bucket%s %d\n", m.name, labelStr(m.labelNames, s.labels, "le", fmtF(bound)), s.buckets[i])
				}
				fmt.Fprintf(&b, "%s_bucket%s %d\n", m.name, labelStr(m.labelNames, s.labels, "le", "+Inf"), s.count)
				fmt.Fprintf(&b, "%s_sum%s %s\n", m.name, labelStr(m.labelNames, s.labels), fmtF(s.sum))
				fmt.Fprintf(&b, "%s_count%s %d\n", m.name, labelStr(m.labelNames, s.labels), s.count)
			default:
				fmt.Fprintf(&b, "%s%s %s\n", m.name, labelStr(m.labelNames, s.labels), fmtF(s.value))
			}
		}
		m.mu.Unlock()
	}
	return b.String()
}

// Handler serves /metrics.
func (r *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = w.Write([]byte(r.Render()))
	})
}
