// Package metrics holds the app's Prometheus collectors and the /metrics
// handler. Collectors live on a private registry (not the global default) so
// tests and multiple New() calls don't double-register, and so the exposed
// series are exactly the ones we define here.
package metrics

import (
	"context"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// sourceDurationBuckets reach past the lookup client's 8 s timeout and the
// deal client's 30 s one, so a request cut off by either lands in a finite
// bucket.
var sourceDurationBuckets = []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 7.5, 10, 20, 30, 60}

// runDurationBuckets span a background run from one second to two hours.
var runDurationBuckets = []float64{1, 5, 15, 30, 60, 120, 300, 600, 1200, 1800, 3600, 5400, 7200}

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

	sourceRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "ohara_source_requests_total",
		Help: "Requests to external sources behind a lookup provider, by source and outcome (ok, http_<code>, error, parse_error, oauth_error).",
	}, []string{"source", "status"})

	sourceErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "ohara_source_errors_total",
		Help: "Failed requests to external sources: any outcome other than ok.",
	}, []string{"source"})

	sourceParseErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "ohara_source_parse_errors_total",
		Help: "Responses from an external source that could not be read (status parse_error).",
	}, []string{"source"})

	sourceDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "ohara_source_request_duration_seconds",
		Help:    "Time from sending a request to an external source to its last body byte read or its failure, whatever the outcome.",
		Buckets: sourceDurationBuckets,
	}, []string{"source"})

	sourceLastSuccess = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "ohara_source_last_success_timestamp_seconds",
		Help: "Unix time of the last ok request to an external source; absent until one succeeds.",
	}, []string{"source"})

	sourceResults = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "ohara_source_results",
		Help: "Results of a source's last answer: listings across the last deal search run (canary included) for a deal source, products or candidates of the last ok request for a lookup source.",
	}, []string{"source"})

	sourceCanary = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "ohara_source_canary_ok",
		Help: "1 when the deal source's canary query in the last run returned listings, else 0.",
	}, []string{"source"})

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
		Help: "Owned collection value in PLN by bound (floor: buy-back or cheapest Discogs copy, mid: used asking or Discogs VG+ suggestion, ceiling: new or Discogs Mint suggestion), summed over the copies that have the bound.",
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

	dbUnknownMigrations = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "ohara_db_unknown_migrations",
		Help: "Migrations applied to the database that this binary has no file for, counted at startup; above 0 after a rollback to an older release.",
	})

	backgroundRuns = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "ohara_background_runs_total",
		Help: "Finished background runs, scheduled or manual, by task (deals, valuation) and outcome (ok, failed: the run returned an error).",
	}, []string{"task", "outcome"})

	backgroundRunDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "ohara_background_run_duration_seconds",
		Help:    "How long a finished background run took, by task.",
		Buckets: runDurationBuckets,
	}, []string{"task"})

	backgroundLastSuccess = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "ohara_background_last_success_timestamp_seconds",
		Help: "Unix time a background run of the task last ended ok; absent until one does.",
	}, []string{"task"})

	dealAlerts = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "ohara_deal_alerts_total",
		Help: "Deal alerts sent to Telegram, by outcome (sent, failed).",
	}, []string{"outcome"})

	prunedListings = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "ohara_price_observations_pruned_total",
		Help: "Stored listings deleted for being last seen before OHARA_PRICE_RETENTION_DAYS.",
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
		dbUnknownMigrations,
		backgroundRuns,
		backgroundRunDuration,
		backgroundLastSuccess,
		dealAlerts,
		prunedListings,
		sourceRequests,
		sourceErrors,
		sourceParseErrors,
		sourceDuration,
		sourceLastSuccess,
		sourceResults,
		sourceCanary,
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

// SetUnknownMigrations records how many applied migrations the binary has
// no file for.
func SetUnknownMigrations(n int) {
	dbUnknownMigrations.Set(float64(n))
}

// Background tasks for ObserveBackgroundRun.
const (
	// TaskDeals is the wishlist deal search.
	TaskDeals = "deals"
	// TaskValuation is the collection valuation.
	TaskValuation = "valuation"
)

// Background run and deal alert outcomes.
const (
	outcomeOK     = "ok"
	outcomeFailed = "failed"
	outcomeSent   = "sent"
)

// InitBackgroundTask exports zeroed run counters for task, so increase()
// sees the first run after a restart.
func InitBackgroundTask(task string) {
	backgroundRuns.WithLabelValues(task, outcomeOK)
	backgroundRuns.WithLabelValues(task, outcomeFailed)
}

// ObserveBackgroundRun records one finished run of task that took d. A
// run that returned an error counts as failed; any other run as ok,
// which also moves the task's last-success timestamp.
func ObserveBackgroundRun(task string, d time.Duration, err error) {
	backgroundRunDuration.WithLabelValues(task).Observe(d.Seconds())
	if err != nil {
		backgroundRuns.WithLabelValues(task, outcomeFailed).Inc()
		return
	}
	backgroundRuns.WithLabelValues(task, outcomeOK).Inc()
	backgroundLastSuccess.WithLabelValues(task).SetToCurrentTime()
}

// InitDealAlerts exports zeroed deal alert counters; call it when
// Telegram is configured.
func InitDealAlerts() {
	dealAlerts.WithLabelValues(outcomeSent)
	dealAlerts.WithLabelValues(outcomeFailed)
}

// ObserveDealAlert counts one Telegram send of a deal alert.
func ObserveDealAlert(err error) {
	if err != nil {
		dealAlerts.WithLabelValues(outcomeFailed).Inc()
		return
	}
	dealAlerts.WithLabelValues(outcomeSent).Inc()
}

// AddPrunedListings counts n stored listings deleted by a prune.
func AddPrunedListings(n int64) {
	prunedListings.Add(float64(n))
}

// Source request outcomes for ObserveSource.
const (
	// SourceOK is a usable answer, a valid "no match" included.
	SourceOK = "ok"
	// SourceError is a transport failure.
	SourceError = "error"
	// SourceParseError is a response that could not be read.
	SourceParseError = "parse_error"
)

// InitSource exports zeroed counters and an empty request duration
// histogram for source, so increase() and alert queries see the first
// failure after a restart.
func InitSource(source string) {
	sourceRequests.WithLabelValues(source, SourceOK)
	sourceErrors.WithLabelValues(source)
	sourceParseErrors.WithLabelValues(source)
	sourceDuration.WithLabelValues(source)
}

// ObserveSource counts one request to an external source. status is
// SourceOK for a usable answer, "http_<code>" for an unexpected status,
// SourceError for a transport failure and SourceParseError for a response
// that could not be read. Anything but SourceOK also counts as an error; a
// SourceOK moves the last-success timestamp.
func ObserveSource(source, status string) {
	sourceRequests.WithLabelValues(source, status).Inc()
	switch status {
	case SourceOK:
		sourceLastSuccess.WithLabelValues(source).SetToCurrentTime()
		return
	case SourceParseError:
		sourceParseErrors.WithLabelValues(source).Inc()
	}
	sourceErrors.WithLabelValues(source).Inc()
}

// ObserveSourceDuration records how long one request to source took,
// whatever its outcome.
func ObserveSourceDuration(source string, d time.Duration) {
	sourceDuration.WithLabelValues(source).Observe(d.Seconds())
}

// SetSourceResults records how many results source returned: listings in a
// deal run, or products or candidates in one lookup request.
func SetSourceResults(source string, n int) {
	sourceResults.WithLabelValues(source).Set(float64(n))
}

// SetSourceCanary records whether source's canary query returned listings.
func SetSourceCanary(source string, ok bool) {
	v := 0.0
	if ok {
		v = 1
	}
	sourceCanary.WithLabelValues(source).Set(v)
}

// scanQueueTimeout bounds the queue count a scrape runs.
const scanQueueTimeout = 2 * time.Second

var scanQueueDesc = prometheus.NewDesc("ohara_scan_queue_size",
	"Barcodes no lookup provider resolved, waiting in the scan queue.", nil, nil)

// RegisterScanQueueCollector exports the scan queue size, counted on each
// scrape. A failed count leaves the series out of that scrape instead of
// failing the whole scrape. Call once at startup.
func RegisterScanQueueCollector(count func(ctx context.Context) (int64, error)) {
	registry.MustRegister(&scanQueueCollector{count: count})
}

type scanQueueCollector struct {
	count func(ctx context.Context) (int64, error)
}

func (c *scanQueueCollector) Describe(ch chan<- *prometheus.Desc) { ch <- scanQueueDesc }

func (c *scanQueueCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), scanQueueTimeout)
	defer cancel()
	n, err := c.count(ctx)
	if err != nil {
		return
	}
	ch <- prometheus.MustNewConstMetric(scanQueueDesc, prometheus.GaugeValue, float64(n))
}
