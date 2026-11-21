package metrics

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestObserveSourceTracksHealth(t *testing.T) {
	const src = "test-health"
	InitSource(src)
	if got := testutil.ToFloat64(sourceErrors.WithLabelValues(src)); got != 0 {
		t.Fatalf("errors after init = %v", got)
	}
	if n := testutil.CollectAndCount(sourceLastSuccess, "ohara_source_last_success_timestamp_seconds"); n != 0 {
		t.Fatalf("last success exported before any success: %d series", n)
	}

	before := float64(time.Now().Unix())
	ObserveSource(src, SourceOK)
	if got := testutil.ToFloat64(sourceLastSuccess.WithLabelValues(src)); got < before {
		t.Fatalf("last success = %v, want >= %v", got, before)
	}

	ObserveSource(src, SourceParseError)
	ObserveSource(src, "http_429")
	ObserveSource(src, SourceError)
	cases := []struct {
		name string
		got  float64
		want float64
	}{
		{"requests ok", testutil.ToFloat64(sourceRequests.WithLabelValues(src, SourceOK)), 1},
		{"requests parse_error", testutil.ToFloat64(sourceRequests.WithLabelValues(src, SourceParseError)), 1},
		{"requests http_429", testutil.ToFloat64(sourceRequests.WithLabelValues(src, "http_429")), 1},
		{"errors", testutil.ToFloat64(sourceErrors.WithLabelValues(src)), 3},
		{"parse errors", testutil.ToFloat64(sourceParseErrors.WithLabelValues(src)), 1},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

func TestSourceRunGauges(t *testing.T) {
	const src = "test-run"
	SetSourceResults(src, 12)
	SetSourceCanary(src, true)
	if got := testutil.ToFloat64(sourceResults.WithLabelValues(src)); got != 12 {
		t.Fatalf("results = %v", got)
	}
	if got := testutil.ToFloat64(sourceCanary.WithLabelValues(src)); got != 1 {
		t.Fatalf("canary = %v", got)
	}
	SetSourceCanary(src, false)
	if got := testutil.ToFloat64(sourceCanary.WithLabelValues(src)); got != 0 {
		t.Fatalf("failed canary = %v", got)
	}
}

func TestScanQueueCollector(t *testing.T) {
	ok := &scanQueueCollector{count: func(context.Context) (int64, error) { return 4, nil }}
	want := "# HELP ohara_scan_queue_size Barcodes no lookup provider resolved, waiting in the scan queue.\n" +
		"# TYPE ohara_scan_queue_size gauge\nohara_scan_queue_size 4\n"
	if err := testutil.CollectAndCompare(ok, strings.NewReader(want)); err != nil {
		t.Fatal(err)
	}
	failing := &scanQueueCollector{count: func(context.Context) (int64, error) { return 0, errors.New("db down") }}
	if n := testutil.CollectAndCount(failing); n != 0 {
		t.Fatalf("failed count exported %d series", n)
	}
}

// scrape returns the /metrics exposition text.
func scrape(t *testing.T) string {
	t.Helper()
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil))
	return rec.Body.String()
}

// TestSourceDurationBucketsHoldADealTimeout checks that a request the deal
// client's 30 s timeout cut off, which runs a little past 30 s, lands in a
// finite bucket.
func TestSourceDurationBucketsHoldADealTimeout(t *testing.T) {
	src := "test-duration-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	InitSource(src)
	series := `ohara_source_request_duration_seconds_count{source="` + src + `"} `
	if text := scrape(t); !strings.Contains(text, series+"0\n") {
		t.Fatalf("no empty duration histogram after InitSource, want line %q0", series)
	}

	ObserveSourceDuration(src, 30*time.Second+500*time.Millisecond)
	text := scrape(t)
	for _, want := range []string{
		`ohara_source_request_duration_seconds_bucket{source="` + src + `",le="30"} 0` + "\n",
		`ohara_source_request_duration_seconds_bucket{source="` + src + `",le="60"} 1` + "\n",
		series + "1\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("scrape lacks %q", want)
		}
	}
}

func TestObserveBackgroundRun(t *testing.T) {
	const task = "test-background-run"
	t.Cleanup(func() {
		backgroundRuns.DeleteLabelValues(task, outcomeOK)
		backgroundRuns.DeleteLabelValues(task, outcomeFailed)
		backgroundRunDuration.DeleteLabelValues(task)
		backgroundLastSuccess.DeleteLabelValues(task)
	})
	InitBackgroundTask(task)
	if n := testutil.CollectAndCount(backgroundRuns); n != 2 {
		t.Fatalf("runs exported %d series before the first run, want ok and failed", n)
	}
	if n := testutil.CollectAndCount(backgroundLastSuccess); n != 0 {
		t.Fatalf("last success exported %d series before any run", n)
	}

	start := float64(time.Now().UnixNano()) / 1e9
	ObserveBackgroundRun(task, 3*time.Second, nil)
	lastOK := testutil.ToFloat64(backgroundLastSuccess.WithLabelValues(task))
	if lastOK < start {
		t.Fatalf("last success = %v, want at least %v", lastOK, start)
	}
	ObserveBackgroundRun(task, time.Second, errors.New("db down"))
	if got := testutil.ToFloat64(backgroundRuns.WithLabelValues(task, outcomeOK)); got != 1 {
		t.Errorf("ok runs = %v, want 1", got)
	}
	if got := testutil.ToFloat64(backgroundRuns.WithLabelValues(task, outcomeFailed)); got != 1 {
		t.Errorf("failed runs = %v, want 1", got)
	}
	if got := testutil.ToFloat64(backgroundLastSuccess.WithLabelValues(task)); got != lastOK {
		t.Errorf("last success = %v after a failed run, want %v", got, lastOK)
	}
	bounds := exportedBuckets(t, "ohara_background_run_duration_seconds", task)
	if len(bounds) == 0 || bounds[0] != 1 || bounds[len(bounds)-1] < 7200 {
		t.Errorf("duration buckets = %v s, want 1 to at least 7200", bounds)
	}
	if n := testutil.ToFloat64(backgroundRuns.WithLabelValues(task, outcomeOK)) + testutil.ToFloat64(backgroundRuns.WithLabelValues(task, outcomeFailed)); n != 2 {
		t.Errorf("runs = %v, want 2", n)
	}
}

// exportedBuckets reads the bucket upper bounds the registry exports for
// the histogram name with the given task label.
func exportedBuckets(t *testing.T, name, task string) []float64 {
	t.Helper()
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range families {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() != "task" || l.GetValue() != task {
					continue
				}
				var bounds []float64
				for _, b := range m.GetHistogram().GetBucket() {
					bounds = append(bounds, b.GetUpperBound())
				}
				return bounds
			}
		}
	}
	t.Fatalf("%s{task=%q} not exported", name, task)
	return nil
}

func TestObserveDealAlert(t *testing.T) {
	InitDealAlerts()
	sent, failed := testutil.ToFloat64(dealAlerts.WithLabelValues(outcomeSent)), testutil.ToFloat64(dealAlerts.WithLabelValues(outcomeFailed))
	ObserveDealAlert(nil)
	ObserveDealAlert(errors.New("telegram: status 502"))
	ObserveDealAlert(nil)
	if got := testutil.ToFloat64(dealAlerts.WithLabelValues(outcomeSent)) - sent; got != 2 {
		t.Errorf("sent grew by %v, want 2", got)
	}
	if got := testutil.ToFloat64(dealAlerts.WithLabelValues(outcomeFailed)) - failed; got != 1 {
		t.Errorf("failed grew by %v, want 1", got)
	}
}
