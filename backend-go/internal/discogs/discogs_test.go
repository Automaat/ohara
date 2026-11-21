package discogs_test

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/Automaat/ohara/backend-go/internal/discogs"
	"github.com/Automaat/ohara/backend-go/internal/metrics"
)

const testToken = "test-token"

const (
	errorsSeries      = `ohara_source_errors_total{source="discogs"}`
	parseErrorsSeries = `ohara_source_parse_errors_total{source="discogs"}`
)

// seen is the request the upstream received.
type seen struct {
	path, query, auth, userAgent string
}

// upstream serves status and body for every GET and records the last one;
// like Discogs, it answers any other method with 405.
func upstream(t *testing.T, status int, body []byte) (*discogs.Client, *seen) {
	t.Helper()
	got := &seen{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
			w.Header().Set("Allow", http.MethodGet)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		got.path = r.URL.Path
		got.query = r.URL.RawQuery
		got.auth = r.Header.Get("Authorization")
		got.userAgent = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if _, err := w.Write(body); err != nil {
			t.Errorf("write body: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	return &discogs.Client{HTTP: srv.Client(), BaseURL: srv.URL, Token: testToken}, got
}

// fixture reads a Discogs answer from testdata; testdata/README.md names
// the request each one was recorded from.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func scrape(t *testing.T) string {
	t.Helper()
	rec := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", http.NoBody))
	return rec.Body.String()
}

// metricValue is series' value in a scrape, 0 when it is absent.
func metricValue(t *testing.T, body, series string) float64 {
	t.Helper()
	for line := range strings.Lines(body) {
		if rest, ok := strings.CutPrefix(line, series+" "); ok {
			v, err := strconv.ParseFloat(strings.TrimSpace(rest), 64)
			if err != nil {
				t.Fatalf("%s: %v", series, err)
			}
			return v
		}
	}
	return 0
}

func requestsSeries(status string) string {
	return `ohara_source_requests_total{source="discogs",status="` + status + `"}`
}

// wantDelta checks that each series grew by its count between two scrapes.
func wantDelta(t *testing.T, before, after string, want map[string]float64) {
	t.Helper()
	for series, n := range want {
		if d := metricValue(t, after, series) - metricValue(t, before, series); d != n {
			t.Errorf("%s grew by %v, want %v", series, d, n)
		}
	}
}

func checkHeaders(t *testing.T, got *seen) {
	t.Helper()
	if want := "Discogs token=" + testToken; got.auth != want {
		t.Errorf("Authorization = %q, want %q", got.auth, want)
	}
	if got.userAgent != discogs.UserAgent {
		t.Errorf("User-Agent = %q, want %q", got.userAgent, discogs.UserAgent)
	}
}

func TestStats(t *testing.T) {
	cases := []struct {
		name     string
		fixture  string
		id       int64
		currency string
		want     discogs.Stats
	}{
		{
			name:     "for sale",
			fixture:  "stats_for_sale.json",
			id:       1873013,
			currency: "EUR",
			want:     discogs.Stats{LowestPrice: &discogs.Money{Currency: "EUR", Value: 70}, NumForSale: 118},
		},
		{
			name:     "none for sale",
			fixture:  "stats_none_for_sale.json",
			id:       30000001,
			currency: "USD",
			want:     discogs.Stats{LowestPrice: nil, NumForSale: 0},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, got := upstream(t, http.StatusOK, fixture(t, tc.fixture))
			before := scrape(t)
			s, err := c.Stats(t.Context(), tc.id, tc.currency)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(s, tc.want) {
				t.Errorf("Stats = %+v (lowest %+v), want %+v (lowest %+v)", s, s.LowestPrice, tc.want, tc.want.LowestPrice)
			}
			if want := "/marketplace/stats/" + strconv.FormatInt(tc.id, 10); got.path != want {
				t.Errorf("path = %q, want %q", got.path, want)
			}
			if want := "curr_abbr=" + tc.currency; got.query != want {
				t.Errorf("query = %q, want %q", got.query, want)
			}
			checkHeaders(t, got)
			wantDelta(t, before, scrape(t), map[string]float64{requestsSeries("ok"): 1, errorsSeries: 0})
		})
	}
}

func TestPriceSuggestions(t *testing.T) {
	c, got := upstream(t, http.StatusOK, fixture(t, "price_suggestions.json"))
	before := scrape(t)
	s, err := c.PriceSuggestions(t.Context(), 1873013)
	if err != nil {
		t.Fatal(err)
	}
	valuationGrades := map[string]discogs.Money{
		"Very Good Plus (VG+)": {Currency: "USD", Value: 7.9302},
		"Mint (M)":             {Currency: "USD", Value: 10.5313},
	}
	for grade, want := range valuationGrades {
		if m, ok := s[grade]; !ok || m.Currency != want.Currency || math.Abs(m.Value-want.Value) > 1e-9 {
			t.Errorf("%s = %+v (present %v), want %+v", grade, m, ok, want)
		}
	}
	if len(s) != 8 {
		t.Errorf("got %d grades, want 8", len(s))
	}
	if got.path != "/marketplace/price_suggestions/1873013" {
		t.Errorf("path = %q", got.path)
	}
	checkHeaders(t, got)
	wantDelta(t, before, scrape(t), map[string]float64{requestsSeries("ok"): 1, errorsSeries: 0})
}

// TestInvalidBaseURLIsTimed checks that a base URL that does not parse,
// which counts as an error without a request sent, is timed as well.
func TestInvalidBaseURLIsTimed(t *testing.T) {
	c := &discogs.Client{BaseURL: "http://[", Token: testToken}
	before := scrape(t)
	if _, err := c.Stats(t.Context(), 1873013, "EUR"); err == nil {
		t.Fatal("no error")
	}
	wantDelta(t, before, scrape(t), map[string]float64{
		requestsSeries("error"): 1,
		errorsSeries:            1,
		`ohara_source_request_duration_seconds_count{source="discogs"}`: 1,
	})
}

func TestMarketplaceFailuresCountInMetrics(t *testing.T) {
	stats := func(ctx context.Context, c *discogs.Client) error {
		_, err := c.Stats(ctx, 1873013, "EUR")
		return err
	}
	suggestions := func(ctx context.Context, c *discogs.Client) error {
		_, err := c.PriceSuggestions(ctx, 1873013)
		return err
	}
	notJSON := []byte("<html>maintenance</html>")
	trailingGarbage := append(fixture(t, "stats_for_sale.json"), notJSON...)
	cases := []struct {
		name       string
		call       func(context.Context, *discogs.Client) error
		status     int
		body       []byte
		parseError bool
	}{
		{"stats unknown release", stats, http.StatusNotFound, fixture(t, "stats_not_found.json"), false},
		{"stats rate limited", stats, http.StatusTooManyRequests, []byte(`{"message":"You are making requests too quickly."}`), false},
		{"stats not JSON", stats, http.StatusOK, notJSON, true},
		{"stats JSON then garbage", stats, http.StatusOK, trailingGarbage, true},
		{"stats created, not ok", stats, http.StatusCreated, fixture(t, "stats_for_sale.json"), false},
		{"suggestions without token", suggestions, http.StatusUnauthorized, fixture(t, "price_suggestions_unauthenticated.json"), false},
		{"suggestions without seller settings", suggestions, http.StatusUnprocessableEntity, []byte(`{"message":"You must fill out your seller settings first."}`), false},
		{"suggestions not JSON", suggestions, http.StatusOK, notJSON, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := upstream(t, tc.status, tc.body)
			before := scrape(t)
			err := tc.call(t.Context(), c)
			after := scrape(t)
			if err == nil {
				t.Fatal("no error")
			}
			se, isStatus := errors.AsType[discogs.StatusError](err)
			if tc.parseError {
				if isStatus {
					t.Fatalf("err = %v, want a parse error", err)
				}
				wantDelta(t, before, after, map[string]float64{
					requestsSeries("parse_error"): 1,
					parseErrorsSeries:             1,
					errorsSeries:                  1,
					requestsSeries("ok"):          0,
				})
				return
			}
			if !isStatus || se.Code != tc.status {
				t.Fatalf("err = %v, want StatusError{%d}", err, tc.status)
			}
			wantDelta(t, before, after, map[string]float64{
				requestsSeries("http_" + strconv.Itoa(tc.status)): 1,
				errorsSeries:         1,
				parseErrorsSeries:    0,
				requestsSeries("ok"): 0,
			})
		})
	}
}
