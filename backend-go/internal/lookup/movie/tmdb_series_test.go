package movie

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Automaat/ohara/backend-go/internal/metrics"
)

func TestSeriesNameDropsTheCollectionWord(t *testing.T) {
	tests := []struct{ in, want string }{
		{in: "Harry Potter - kolekcja", want: "Harry Potter"},
		{in: "Szybcy i wściekli – Kolekcja", want: "Szybcy i wściekli"},
		{in: "Obcy (kolekcja)", want: "Obcy"},
		{in: "Hobbit  Kolekcja", want: "Hobbit"},
		{in: "James Bond Collection", want: "James Bond"},
		{in: "Kolekcja", want: "Kolekcja"},
		{in: "  Matrix  ", want: "Matrix"},
		{in: "Rekolekcja", want: "Rekolekcja"},
		{in: "", want: ""},
	}
	for _, tt := range tests {
		if got := seriesName(tt.in); got != tt.want {
			t.Errorf("seriesName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestReleasedPartsOrderAndSkipUnreleased(t *testing.T) {
	parts := []tmdbCollectionPart{
		{ID: 3, ReleaseDate: "2004-05-31"},
		{ID: 9, ReleaseDate: "2002-11-13"},
		{ID: 4, ReleaseDate: ""},
		{ID: 8, ReleaseDate: "2002-11-13"},
		{ID: 5, ReleaseDate: "2026-11-23"},
		{ID: 6, ReleaseDate: "2026-11-22"},
		{ID: 7, ReleaseDate: "soon"},
		{ReleaseDate: "2000-01-01"},
	}
	got := releasedParts(parts, "2026-11-22")
	want := []int64{8, 9, 3, 6}
	if len(got) != len(want) {
		t.Fatalf("released = %v, want IDs %v", got, want)
	}
	for i, p := range got {
		if p.ID != want[i] {
			t.Errorf("released[%d] = %d, want %d", i, p.ID, want[i])
		}
	}
}

func TestFilmRefusesAnAnswerWithoutTheMovie(t *testing.T) {
	for _, body := range []string{`null`, `{}`, `{"title":"Bez ID"}`} {
		s := serve(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(body))
		})
		tmdb := TMDB{Client: s.Client(), BaseURL: s.URL, Key: "k"}
		before := tmdbMetrics(t)
		if _, err := tmdb.Film(t.Context(), 1); err == nil {
			t.Errorf("Film(%s) = nil error, want a parse error", body)
		}
		if after := tmdbMetrics(t); countOf(after, "parse_error") != countOf(before, "parse_error")+1 ||
			countOf(after, "ok") != countOf(before, "ok") {
			t.Errorf("Film(%s) metrics: parse_error %v -> %v, ok %v -> %v", body,
				countOf(before, "parse_error"), countOf(after, "parse_error"), countOf(before, "ok"), countOf(after, "ok"))
		}
	}
}

func tmdbMetrics(t *testing.T) string {
	t.Helper()
	rec := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", http.NoBody))
	return rec.Body.String()
}

// countOf reads ohara_source_requests_total for tmdb and status from a
// metrics scrape, 0 when absent.
func countOf(scrape, status string) float64 {
	prefix := `ohara_source_requests_total{source="tmdb",status="` + status + `"} `
	for line := range strings.SplitSeq(scrape, "\n") {
		if v, ok := strings.CutPrefix(line, prefix); ok {
			n, err := strconv.ParseFloat(v, 64)
			if err == nil {
				return n
			}
		}
	}
	return 0
}
