package warc

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

const (
	// totalDataWritten is the name of the metric that tracks the total data written to WARC files.
	totalDataWritten     string = "total_data_written"
	totalDataWrittenHelp string = "Total data written to WARC files in bytes"

	// localDedupedBytesTotal is the name of the metric that tracks the total bytes deduped using local dedupe.
	localDedupedBytesTotal     string = "local_deduped_bytes_total"
	localDedupedBytesTotalHelp string = "Total bytes deduped using local dedupe"

	// localDedupedTotal is the name of the metric that tracks the total records deduped using local dedupe.
	localDedupedTotal     string = "local_deduped_total"
	localDedupedTotalHelp string = "Total records deduped using local dedupe"

	// doppelgangerDedupedBytesTotal is the name of the metric that tracks the total bytes deduped using Doppelganger.
	doppelgangerDedupedBytesTotal     string = "doppelganger_deduped_bytes_total"
	doppelgangerDedupedBytesTotalHelp string = "Total bytes deduped using Doppelganger"

	// doppelgangerDedupedTotal is the name of the metric that tracks the total records deduped using Doppelganger.
	doppelgangerDedupedTotal     string = "doppelganger_deduped_total"
	doppelgangerDedupedTotalHelp string = "Total records deduped using Doppelganger"

	// cdxDedupedBytesTotal is the name of the metric that tracks the total bytes deduped using CDX.
	cdxDedupedBytesTotal     string = "cdx_deduped_bytes_total"
	cdxDedupedBytesTotalHelp string = "Total bytes deduped using CDX"

	// cdxDedupedTotal is the name of the metric that tracks the total records deduped using CDX.
	cdxDedupedTotal     string = "cdx_deduped_total"
	cdxDedupedTotalHelp string = "Total records deduped using CDX"

	// proxyRequestsTotal is the name of the metric that tracks the total number of requests gone through a proxy.
	proxyRequestsTotal     string = "proxy_requests_total"
	proxyRequestsTotalHelp string = "Total number of requests gone through a proxy"

	// proxyErrorsTotal is the name of the metric that tracks the total number of errors occurred with a proxy.
	proxyErrorsTotal     string = "proxy_errors_total"
	proxyErrorsTotalHelp string = "Total number of errors occurred with a proxy"

	// proxyLastUsedNanoseconds is the name of the metric that tracks the last time a proxy was used.
	proxyLastUsedNanoseconds     string = "proxy_last_used_nanoseconds"
	proxyLastUsedNanosecondsHelp string = "Last time a proxy was used in seconds (unix timestamp ns)"
)

// Labels represents Prometheus-style label values as key-value pairs.
// Labels are used with WithLabels() to specify concrete label values when recording metrics.
//
// Example usage with labels:
//
//	registry := newLocalRegistry()
//
//	// Register a counter with label names (declares which dimensions the metric tracks)
//	httpRequests := registry.RegisterCounter("http_requests_total", "Total HTTP requests", []string{"method", "status"})
//
//	// Use WithLabels to specify label values when recording
//	httpRequests.WithLabels(Labels{"method": "GET", "status": "200"}).Inc()
//	httpRequests.WithLabels(Labels{"method": "POST", "status": "201"}).Add(5)
//	httpRequests.WithLabels(Labels{"method": "GET", "status": "404"}).Inc()
//
//	// Each unique combination of label values creates a separate metric series
//	// The counter with method=GET,status=200 has value 1
//	// The counter with method=POST,status=201 has value 5
//	// The counter with method=GET,status=404 has value 1
//
// Example usage without labels:
//
//	// For metrics without labels, pass nil or empty slice when registering
//	totalCounter := registry.RegisterCounter("requests_total", "All requests", nil)
//	// WithLabels(nil) is required but the labels parameter is ignored
//	totalCounter.WithLabels(nil).Inc()
//
// Labels are internally sorted by key to ensure consistent metric identification
// regardless of the order in which they are specified. This means:
//
//	Labels{"method": "GET", "status": "200"} == Labels{"status": "200", "method": "GET"}
type Labels map[string]string

// labelsToString converts labels to a consistent string representation for use as map keys.
// Labels are sorted by key to ensure consistent ordering.
func labelsToString(labels Labels) string {
	if len(labels) == 0 {
		return ""
	}

	// Sort keys for consistent ordering
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Build string representation
	parts := make([]string, 0, len(labels))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%q", k, labels[k]))
	}
	return strings.Join(parts, ",")
}

// makeMetricKey creates a unique key for a metric with labels.
func makeMetricKey(name string, labels Labels) string {
	if len(labels) == 0 {
		return name
	}
	return name + "{" + labelsToString(labels) + "}"
}

// RegistryOpts is an interface that provides a WithLabels method to get a metric with specific labels.
type RegistryOpts[T any] interface {
	// WithLabels returns a Counter for the given label values.
	// If the counter has no labels, labels parameter is ignored.
	WithLabels(labels Labels) T
}

// Counter represents a monotonically increasing metric.
type Counter interface {
	RegistryOpts[Counter]
	// Inc increments the counter by 1.
	Inc()
	// Add adds the given value to the counter.
	Add(value int64)
	// Get returns the current value of the counter.
	// This is used to support unit-testing of the metrics.
	Get() int64
}

// Gauge represents a metric that can go up or down.
type Gauge interface {
	RegistryOpts[Gauge]
	// Set sets the gauge to the given value.
	Set(value int64)
	// Inc increments the gauge by 1.
	Inc()
	// Dec decrements the gauge by 1.
	Dec()
	// Add adds the given value to the gauge.
	Add(value int64)
	// Sub subtracts the given value from the gauge.
	Sub(value int64)
	// Get returns the current value of the gauge.
	// This is used to support unit-testing of the metrics.
	Get() int64
}

// Histogram represents a metric for observing distributions of values.
type Histogram interface {
	RegistryOpts[Histogram]
	// Observe adds a single observation to the histogram.
	Observe(value int64)
}

// StatsRegistry provides a registry for external libraries to register and update metrics.
// The StatsRegistry implementation is expected to be thread-safe so that gowarc can safely register and update metrics from multiple goroutines.
type StatsRegistry interface {
	// RegisterCounter registers a new counter metric with optional label names.
	// labelNames specifies which labels this counter will use (e.g., []string{"method", "status"}).
	// Returns a Counter that can be used with WithLabels() to specify label values.
	// If labelNames is nil or empty, returns a Counter that ignores labels.
	RegisterCounter(name, help string, labelNames []string) Counter

	// RegisterGauge registers a new gauge metric with optional label names.
	// labelNames specifies which labels this gauge will use (e.g., []string{"location", "type"}).
	// Returns a Gauge that can be used with WithLabels() to specify label values.
	// If labelNames is nil or empty, returns a Gauge that ignores labels.
	RegisterGauge(name, help string, labelNames []string) Gauge

	// RegisterHistogram registers a new histogram metric with the given buckets and optional label names.
	// If buckets is nil, uses Prometheus default buckets.
	// labelNames specifies which labels this histogram will use (e.g., []string{"endpoint", "method"}).
	// Returns a Histogram that can be used with WithLabels() to specify label values.
	// If labelNames is nil or empty, returns a Histogram that ignores labels.
	RegisterHistogram(name, help string, buckets []int64, labelNames []string) Histogram
}

// Nil-safe implementations for when no StatsRegistry is provided.
type localCounter struct {
	v atomic.Int64
}

func (n *localCounter) WithLabels(_ Labels) Counter { return n }
func (n *localCounter) Inc()                        { n.v.Add(1) }
func (n *localCounter) Add(value int64)             { n.v.Add(value) }
func (n *localCounter) Get() int64                  { return n.v.Load() }

type localGauge struct {
	v atomic.Int64
}

func (n *localGauge) WithLabels(_ Labels) Gauge { return n }
func (n *localGauge) Set(value int64)           { n.v.Store(value) }
func (n *localGauge) Inc()                      { n.v.Add(1) }
func (n *localGauge) Dec()                      { n.v.Add(-1) }
func (n *localGauge) Add(value int64)           { n.v.Add(value) }
func (n *localGauge) Sub(value int64)           { n.v.Add(-value) }
func (n *localGauge) Get() int64                { return n.v.Load() }

type localHistogram struct{}

func (n *localHistogram) WithLabels(_ Labels) Histogram { return n }
func (n *localHistogram) Observe(_ int64)               {}

// labeledCounter implements Counter with label support
type labeledCounter struct {
	name     string
	registry *localRegistry
}

func (l *labeledCounter) WithLabels(labels Labels) Counter {
	return l.registry.getOrCreateCounter(l.name, labels)
}

func (l *labeledCounter) Inc() {
	l.registry.getOrCreateCounter(l.name, nil).Inc()
}

func (l *labeledCounter) Add(value int64) {
	l.registry.getOrCreateCounter(l.name, nil).Add(value)
}

func (l *labeledCounter) Get() int64 {
	return l.registry.getOrCreateCounter(l.name, nil).Get()
}

// labeledGauge implements Gauge with label support
type labeledGauge struct {
	name     string
	registry *localRegistry
}

func (l *labeledGauge) WithLabels(labels Labels) Gauge {
	return l.registry.getOrCreateGauge(l.name, labels)
}

func (l *labeledGauge) Set(value int64) {
	l.registry.getOrCreateGauge(l.name, nil).Set(value)
}

func (l *labeledGauge) Inc() {
	l.registry.getOrCreateGauge(l.name, nil).Inc()
}

func (l *labeledGauge) Dec() {
	l.registry.getOrCreateGauge(l.name, nil).Dec()
}

func (l *labeledGauge) Add(value int64) {
	l.registry.getOrCreateGauge(l.name, nil).Add(value)
}

func (l *labeledGauge) Sub(value int64) {
	l.registry.getOrCreateGauge(l.name, nil).Sub(value)
}

func (l *labeledGauge) Get() int64 {
	return l.registry.getOrCreateGauge(l.name, nil).Get()
}

// labeledHistogram implements Histogram with label support
type labeledHistogram struct {
	name     string
	registry *localRegistry
}

func (l *labeledHistogram) WithLabels(labels Labels) Histogram {
	return l.registry.getOrCreateHistogram(l.name, labels)
}

func (l *labeledHistogram) Observe(value int64) {
	l.registry.getOrCreateHistogram(l.name, nil).Observe(value)
}

type localRegistry struct {
	sync.Mutex
	gauges     map[string]*localGauge
	counters   map[string]*localCounter
	histograms map[string]*localHistogram
}

func newLocalRegistry() *localRegistry {
	return &localRegistry{}
}

func (n *localRegistry) getOrCreateCounter(name string, labels Labels) Counter {
	n.Lock()
	defer n.Unlock()
	if n.counters == nil {
		n.counters = make(map[string]*localCounter)
	}
	key := makeMetricKey(name, labels)
	if c, ok := n.counters[key]; ok {
		return c
	}
	c := &localCounter{}
	n.counters[key] = c
	return c
}

func (n *localRegistry) getOrCreateGauge(name string, labels Labels) Gauge {
	n.Lock()
	defer n.Unlock()
	if n.gauges == nil {
		n.gauges = make(map[string]*localGauge)
	}
	key := makeMetricKey(name, labels)
	if g, ok := n.gauges[key]; ok {
		return g
	}
	g := &localGauge{}
	n.gauges[key] = g
	return g
}

func (n *localRegistry) getOrCreateHistogram(name string, labels Labels) Histogram {
	n.Lock()
	defer n.Unlock()
	if n.histograms == nil {
		n.histograms = make(map[string]*localHistogram)
	}
	key := makeMetricKey(name, labels)
	if h, ok := n.histograms[key]; ok {
		return h
	}
	h := &localHistogram{}
	n.histograms[key] = h
	return h
}

func (n *localRegistry) RegisterCounter(name, _ string, _ []string) Counter {
	return &labeledCounter{
		name:     name,
		registry: n,
	}
}

func (n *localRegistry) RegisterGauge(name, _ string, _ []string) Gauge {
	return &labeledGauge{
		name:     name,
		registry: n,
	}
}

func (n *localRegistry) RegisterHistogram(name, _ string, _ []int64, _ []string) Histogram {
	return &labeledHistogram{
		name:     name,
		registry: n,
	}
}
