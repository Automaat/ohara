// Package metrics holds the app's Prometheus collectors and the /metrics
// handler. Collectors live on a private registry (not the global default) so
// tests and multiple New() calls don't double-register, and so the exposed
// series are exactly the ones we define here.
package metrics

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	registry = prometheus.NewRegistry()

	httpRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "ohara_http_requests_total",
		Help: "Total HTTP requests by method, route pattern and status code.",
	}, []string{"method", "route", "status"})

	httpDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "ohara_http_request_duration_seconds",
		Help:    "HTTP request latency by method and route pattern.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "route"})

	buildInfo = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "ohara_build_info",
		Help: "Always 1; the version label names the running build.",
	}, []string{"version"})

	lookupRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "ohara_lookup_provider_requests_total",
		Help: "Barcode lookups sent to a metadata provider (cache misses).",
	}, []string{"provider"})

	lookupErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "ohara_lookup_provider_errors_total",
		Help: "Provider lookups that failed to fetch or parse.",
	}, []string{"provider"})

	lookupCacheHits = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "ohara_lookup_cache_hits_total",
		Help: "Barcode lookups answered from lookup_cache.",
	}, []string{"provider"})

	collectionValue = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "ohara_collection_value_pln",
		Help: "Owned collection value in PLN by bound (floor: buy-back, mid: used asking, ceiling: new), summed over the copies that have the bound.",
	}, []string{"bound"})

	collectionValuedCopies = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "ohara_collection_valued_copies",
		Help: "Owned copies that have a value for the bound.",
	}, []string{"bound"})

	collectionCopies = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "ohara_collection_copies",
		Help: "Owned copies at the last valuation.",
	})

	collectionSpent = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "ohara_collection_spent_pln",
		Help: "What the owned copies cost in PLN, at the last valuation.",
	})

	collectionLowConfidence = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "ohara_collection_low_confidence_copies",
		Help: "Owned copies whose used-price estimate rests on too few observations.",
	})

	valuationLastRun = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "ohara_valuation_last_run_timestamp_seconds",
		Help: "When the stored collection value was last computed; 0 before the first valuation.",
	})
)

func init() {
	registry.MustRegister(
		httpRequests,
		httpDuration,
		buildInfo,
		lookupRequests,
		lookupErrors,
		lookupCacheHits,
		collectionValue,
		collectionValuedCopies,
		collectionCopies,
		collectionSpent,
		collectionLowConfidence,
		valuationLastRun,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
}

// Handler serves the Prometheus exposition format for the app's registry.
func Handler() http.Handler {
	return promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
}

// SetBuildInfo publishes the running version as ohara_build_info.
func SetBuildInfo(version string) {
	buildInfo.Reset()
	buildInfo.WithLabelValues(version).Set(1)
}

// PoolStats is the snapshot of pgx pool health the collector exposes. Kept as
// a plain struct so this package doesn't import pgx — the caller adapts
// pool.Stat() into it.
type PoolStats struct {
	Total             int32
	Idle              int32
	Acquired          int32
	Max               int32
	AcquireCount      int64
	EmptyAcquireCount int64
}

// RegisterPoolCollector wires a pgx pool's live stats into /metrics. snapshot
// is called on each scrape (pool.Stat() is cheap). EmptyAcquire is the "pool
// waits" signal: a growing value is the evidence needed before raising
// MaxConns. Call once at startup when a pool exists.
func RegisterPoolCollector(snapshot func() PoolStats) {
	registry.MustRegister(&poolCollector{snapshot: snapshot})
}

type poolCollector struct {
	snapshot func() PoolStats
}

var (
	poolTotalDesc = prometheus.NewDesc("ohara_db_pool_total_conns", "Open connections in the pgx pool.", nil, nil)
	poolIdleDesc  = prometheus.NewDesc("ohara_db_pool_idle_conns", "Idle connections in the pgx pool.", nil, nil)
	poolAcqDesc   = prometheus.NewDesc("ohara_db_pool_acquired_conns", "Currently in-use connections.", nil, nil)
	poolMaxDesc   = prometheus.NewDesc("ohara_db_pool_max_conns", "Configured max connections.", nil, nil)
	poolAcqTotal  = prometheus.NewDesc("ohara_db_pool_acquire_total", "Cumulative successful acquires.", nil, nil)
	poolEmptyDesc = prometheus.NewDesc("ohara_db_pool_empty_acquire_total",
		"Cumulative acquires that had to wait for a connection (pool was empty).", nil, nil)
)

func (c *poolCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- poolTotalDesc
	ch <- poolIdleDesc
	ch <- poolAcqDesc
	ch <- poolMaxDesc
	ch <- poolAcqTotal
	ch <- poolEmptyDesc
}

func (c *poolCollector) Collect(ch chan<- prometheus.Metric) {
	s := c.snapshot()
	ch <- prometheus.MustNewConstMetric(poolTotalDesc, prometheus.GaugeValue, float64(s.Total))
	ch <- prometheus.MustNewConstMetric(poolIdleDesc, prometheus.GaugeValue, float64(s.Idle))
	ch <- prometheus.MustNewConstMetric(poolAcqDesc, prometheus.GaugeValue, float64(s.Acquired))
	ch <- prometheus.MustNewConstMetric(poolMaxDesc, prometheus.GaugeValue, float64(s.Max))
	ch <- prometheus.MustNewConstMetric(poolAcqTotal, prometheus.CounterValue, float64(s.AcquireCount))
	ch <- prometheus.MustNewConstMetric(poolEmptyDesc, prometheus.CounterValue, float64(s.EmptyAcquireCount))
}

// ObserveRequest records one served request. route is the chi route pattern
// (not the raw path) so cardinality stays bounded.
func ObserveRequest(method, route string, status int, dur time.Duration) {
	if route == "" {
		route = "unmatched"
	}
	httpRequests.WithLabelValues(method, route, statusText(status)).Inc()
	httpDuration.WithLabelValues(method, route).Observe(dur.Seconds())
}

// statusText buckets a status code into its class (2xx/4xx/5xx) to keep the
// status label low-cardinality while still distinguishing success from error.
func statusText(status int) string {
	switch {
	case status >= 500:
		return "5xx"
	case status >= 400:
		return "4xx"
	case status >= 300:
		return "3xx"
	case status >= 200:
		return "2xx"
	default:
		return "1xx"
	}
}

// InitLookupProvider exports zeroed lookup counters for provider, so rate
// and alert queries see the series before its first request.
func InitLookupProvider(provider string) {
	lookupRequests.WithLabelValues(provider)
	lookupErrors.WithLabelValues(provider)
	lookupCacheHits.WithLabelValues(provider)
}

// ObserveLookupRequest counts one request sent to provider.
func ObserveLookupRequest(provider string) {
	lookupRequests.WithLabelValues(provider).Inc()
}

// ObserveLookupError counts one failed provider lookup.
func ObserveLookupError(provider string) {
	lookupErrors.WithLabelValues(provider).Inc()
}

// ObserveLookupCacheHit counts one lookup served from the cache.
func ObserveLookupCacheHit(provider string) {
	lookupCacheHits.WithLabelValues(provider).Inc()
}

// CollectionValue is one stored valuation of the owned collection.
type CollectionValue struct {
	Copies        int
	SpentCents    int64
	FloorCents    int64
	FloorCopies   int
	MidCents      int64
	MidCopies     int
	CeilingCents  int64
	CeilingCopies int
	LowConfidence int
	ComputedAt    time.Time
}

// SetCollectionValue publishes v as the collection value gauges.
func SetCollectionValue(v CollectionValue) {
	for _, b := range []struct {
		bound  string
		cents  int64
		copies int
	}{
		{"floor", v.FloorCents, v.FloorCopies},
		{"mid", v.MidCents, v.MidCopies},
		{"ceiling", v.CeilingCents, v.CeilingCopies},
	} {
		collectionValue.WithLabelValues(b.bound).Set(float64(b.cents) / 100)
		collectionValuedCopies.WithLabelValues(b.bound).Set(float64(b.copies))
	}
	collectionCopies.Set(float64(v.Copies))
	collectionSpent.Set(float64(v.SpentCents) / 100)
	collectionLowConfidence.Set(float64(v.LowConfidence))
	valuationLastRun.Set(float64(v.ComputedAt.Unix()))
}
