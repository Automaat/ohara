package metrics

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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
	const src = "test-duration"
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
