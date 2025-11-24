package warc

import (
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

	proxyPrefix         string = "proxy_"
	proxyRequestsSuffix string = "_requests_total"
	proxyRequestsHelp   string = "Total number of requests gone through this proxy"
	proxyErrorsSuffix   string = "_errors_total"
	proxyErrorsHelp     string = "Total number of errors occurred with this proxy"
	proxyLastUsedSuffix string = "_last_used_nanoseconds"
	proxyLastUsedHelp   string = "Last time this proxy was used in seconds (unix timestamp ns)"
)

func makeProxyRequestsMetricName(proxyName string) (string, string) {
	return proxyPrefix + proxyName + proxyRequestsSuffix, proxyRequestsHelp
}

func makeProxyErrorsMetricName(proxyName string) (string, string) {
	return proxyPrefix + proxyName + proxyErrorsSuffix, proxyErrorsHelp
}

func makeProxyLastUsedMetricName(proxyName string) (string, string) {
	return proxyPrefix + proxyName + proxyLastUsedSuffix, proxyLastUsedHelp
}

// Counter represents a monotonically increasing metric.
type Counter interface {
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
	// Observe adds a single observation to the histogram.
	Observe(value int64)
}

// StatsRegistry provides a registry for external libraries to register and update metrics.
// The StatsRegistry implementation is expected to be thread-safe so that gowarc can safely register and update metrics from multiple goroutines.
type StatsRegistry interface {
	// RegisterCounter registers a new counter metric.
	// Returns an existing counter if one with the same name was already registered.
	RegisterCounter(name, help string) Counter

	// RegisterGauge registers a new gauge metric.
	// Returns an existing gauge if one with the same name was already registered.
	RegisterGauge(name, help string) Gauge

	// RegisterHistogram registers a new histogram metric with the given buckets.
	// If buckets is nil, uses Prometheus default buckets.
	// Returns an existing histogram if one with the same name was already registered.
	RegisterHistogram(name, help string, buckets []int64) Histogram
}

// Nil-safe implementations for when no StatsRegistry is provided.
type localCounter struct {
	v atomic.Int64
}

func (n *localCounter) Inc()            { n.v.Add(1) }
func (n *localCounter) Add(value int64) { n.v.Add(value) }
func (n *localCounter) Get() int64      { return n.v.Load() }

type localGauge struct {
	v atomic.Int64
}

func (n *localGauge) Set(value int64) { n.v.Store(value) }
func (n *localGauge) Inc()            { n.v.Add(1) }
func (n *localGauge) Dec()            { n.v.Add(-1) }
func (n *localGauge) Add(value int64) { n.v.Add(value) }
func (n *localGauge) Sub(value int64) { n.v.Add(-value) }
func (n *localGauge) Get() int64      { return n.v.Load() }

type localHistogram struct{}

func (n *localHistogram) Observe(_ int64) {}

type localRegistry struct {
	sync.Mutex
	gauges     map[string]*localGauge
	counters   map[string]*localCounter
	histograms map[string]*localHistogram
}

func newLocalRegistry() *localRegistry {
	return &localRegistry{}
}

func (n *localRegistry) RegisterCounter(name, _ string) Counter {
	n.Lock()
	defer n.Unlock()
	var c *localCounter
	var ok bool
	if n.counters == nil {
		n.counters = make(map[string]*localCounter)
	}
	if c, ok = n.counters[name]; ok {
		return c
	}
	c = &localCounter{}
	n.counters[name] = c
	return c
}

func (n *localRegistry) RegisterGauge(name, _ string) Gauge {
	n.Lock()
	defer n.Unlock()
	var g *localGauge
	var ok bool
	if n.gauges == nil {
		n.gauges = make(map[string]*localGauge)
	}
	if g, ok = n.gauges[name]; ok {
		return g
	}
	g = &localGauge{}
	n.gauges[name] = g
	return g
}

func (n *localRegistry) RegisterHistogram(name, _ string, _ []int64) Histogram {
	n.Lock()
	defer n.Unlock()
	var h *localHistogram
	var ok bool
	if n.histograms == nil {
		n.histograms = make(map[string]*localHistogram)
	}
	if h, ok = n.histograms[name]; ok {
		return h
	}
	h = &localHistogram{}
	n.histograms[name] = h
	return h
}
