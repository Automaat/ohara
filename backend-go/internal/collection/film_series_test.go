package collection_test

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Automaat/ohara/backend-go/internal/collection"
	"github.com/Automaat/ohara/backend-go/internal/dbtest"
	"github.com/Automaat/ohara/backend-go/internal/lookup/movie"
	"github.com/Automaat/ohara/backend-go/internal/metrics"
	"github.com/Automaat/ohara/backend-go/internal/server"
)

// Film TMDB IDs of the double: the Harry Potter collection (id 1241) has
// three released films, listed out of order, one without a release date
// and one released in 2999; filmLoner is in no collection.
const (
	filmFirst      = 671
	filmSecond     = 672
	filmThird      = 673
	filmUndated    = 674
	filmUnreleased = 675
	filmLoner      = 900
	collectionID   = 1241
)

// tmdbDouble answers /3/movie/{id} and /3/collection/{id} and counts the
// requests to each, refusing any not asked in Polish.
type tmdbDouble struct {
	t      *testing.T
	status int
	delay  time.Duration

	mu          sync.Mutex
	movies      int
	collections int
}

func (d *tmdbDouble) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if got := r.URL.Query().Get("language"); got != "pl-PL" {
		d.t.Errorf("%s asked in %q, want pl-PL", r.URL.Path, got)
	}
	d.mu.Lock()
	switch {
	case strings.HasPrefix(r.URL.Path, "/3/movie/"):
		d.movies++
	case strings.HasPrefix(r.URL.Path, "/3/collection/"):
		d.collections++
	}
	d.mu.Unlock()
	if d.delay > 0 {
		select {
		case <-r.Context().Done():
		case <-time.After(d.delay):
		}
		return
	}
	if d.status != 0 {
		http.Error(w, "unavailable", d.status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/3/collection/" + strconv.Itoa(collectionID):
		fmt.Fprintf(w, `{"id":%d,"name":"Harry Potter - kolekcja","parts":[
			{"id":%d,"release_date":"2004-05-31"},
			{"id":%d,"release_date":""},
			{"id":%d,"release_date":"2001-11-16"},
			{"id":%d,"release_date":"2999-01-01"},
			{"id":%d,"release_date":"2002-11-13"}]}`,
			collectionID, filmThird, filmUndated, filmFirst, filmUnreleased, filmSecond)
	case "/3/movie/" + strconv.Itoa(filmLoner):
		fmt.Fprintf(w, `{"id":%d,"title":"Samotnik","belongs_to_collection":null}`, filmLoner)
	default:
		id := strings.TrimPrefix(r.URL.Path, "/3/movie/")
		fmt.Fprintf(w, `{"id":%s,"belongs_to_collection":{"id":%d,"name":"Harry Potter - kolekcja"}}`, id, collectionID)
	}
}

func (d *tmdbDouble) requests() (int, int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.movies, d.collections
}

func newFilmSeriesAPI(t *testing.T, double *tmdbDouble) *api {
	t.Helper()
	double.t = t
	tmdb := httptest.NewServer(double)
	t.Cleanup(tmdb.Close)
	pool := dbtest.Migrated(t)
	logger := slog.New(slog.DiscardHandler)
	finder := movie.TMDB{Client: tmdb.Client(), BaseURL: tmdb.URL, Key: "test-key"}
	h := collection.NewHandler(collection.NewStore(pool), logger).WithFilmSeries(finder).
		WithFilmSeriesTimeout(200 * time.Millisecond)
	srv := httptest.NewServer(server.New(server.Config{}, logger, server.Deps{DB: pool, API: []server.Mounter{h}}))
	t.Cleanup(srv.Close)
	return &api{t: t, url: srv.URL}
}

func film(name string, tmdbID int64) map[string]any {
	return map[string]any{"kind": collection.KindMovie, "name": name, "tmdb_id": tmdbID}
}

func seriesTotal(a *api, id *int64) *int {
	a.t.Helper()
	if id == nil {
		a.t.Fatal("title has no series")
		return nil
	}
	var d collection.SeriesDetail
	a.mustDo(http.MethodGet, fmt.Sprintf("/api/series/%d", *id), nil, &d, http.StatusOK)
	return d.TotalVolumes
}

func TestCreateFilmJoinsItsTMDBCollection(t *testing.T) {
	tests := []struct {
		name       string
		tmdbID     int64
		wantNumber *int
	}{
		{name: "second of three released films", tmdbID: filmSecond, wantNumber: new(2)},
		{name: "first film", tmdbID: filmFirst, wantNumber: new(1)},
		{name: "unreleased film is not numbered", tmdbID: filmUnreleased},
		{name: "film without a release date is not numbered", tmdbID: filmUndated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			double := &tmdbDouble{}
			a := newFilmSeriesAPI(t, double)

			var created collection.Title
			a.mustDo(http.MethodPost, "/api/titles", film("Harry Potter", tt.tmdbID), &created, http.StatusCreated)

			if created.SeriesName == nil || *created.SeriesName != "Harry Potter" {
				t.Fatalf("series = %v, want Harry Potter", created.SeriesName)
			}
			if fmt.Sprint(intOrNil(created.SeriesNumber)) != fmt.Sprint(intOrNil(tt.wantNumber)) {
				t.Errorf("number = %v, want %v", intOrNil(created.SeriesNumber), intOrNil(tt.wantNumber))
			}
			if total := seriesTotal(a, created.SeriesID); total == nil || *total != 3 {
				t.Errorf("series total = %v, want 3", intOrNil(total))
			}
			if movies, collections := double.requests(); movies != 1 || collections != 1 {
				t.Errorf("TMDB requests: %d movie, %d collection; want 1 and 1", movies, collections)
			}
		})
	}
}

func intOrNil(n *int) any {
	if n == nil {
		return nil
	}
	return *n
}

func TestCreateFilmWithoutCollectionGetsNoSeries(t *testing.T) {
	tv := map[string]any{"kind": collection.KindTV, "name": "Dark", "tmdb_id": 70523, "season": 1}
	own := film("Harry Potter", filmSecond)
	own["series_name"], own["series_number"] = "Moje filmy", 7
	none := film("Harry Potter", filmSecond)
	none["series_name"] = nil
	tests := []struct {
		name            string
		body            map[string]any
		wantSeries      string
		wantMovies      int
		wantCollections int
	}{
		{name: "no TMDB ID", body: map[string]any{"kind": collection.KindMovie, "name": "Bez ID"}},
		{name: "TMDB movie in no collection", body: film("Samotnik", filmLoner), wantMovies: 1},
		{name: "user's own series kept", body: own, wantSeries: "Moje filmy"},
		{name: "explicit no series kept", body: none},
		{name: "TV season", body: tv},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			double := &tmdbDouble{}
			a := newFilmSeriesAPI(t, double)

			var created collection.Title
			a.mustDo(http.MethodPost, "/api/titles", tt.body, &created, http.StatusCreated)

			got := ""
			if created.SeriesName != nil {
				got = *created.SeriesName
			}
			if got != tt.wantSeries {
				t.Errorf("series = %q, want %q", got, tt.wantSeries)
			}
			if movies, collections := double.requests(); movies != tt.wantMovies || collections != tt.wantCollections {
				t.Errorf("TMDB requests: %d movie, %d collection; want %d and %d",
					movies, collections, tt.wantMovies, tt.wantCollections)
			}
		})
	}
}

func TestCreateFilmSavesWithoutSeriesWhenTMDBFails(t *testing.T) {
	tests := []struct {
		name   string
		double *tmdbDouble
		status string
	}{
		{name: "server error", double: &tmdbDouble{status: http.StatusInternalServerError}, status: "http_500"},
		{name: "slow answer", double: &tmdbDouble{delay: 5 * time.Second}, status: metrics.SourceError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newFilmSeriesAPI(t, tt.double)
			before := sourceRequests(t, tt.status)

			start := time.Now()
			var created collection.Title
			a.mustDo(http.MethodPost, "/api/titles", film("Harry Potter", filmSecond), &created, http.StatusCreated)

			if elapsed := time.Since(start); elapsed > 3*time.Second {
				t.Errorf("save took %s, want the lookup cut at its timeout", elapsed)
			}
			if created.SeriesName != nil {
				t.Errorf("series = %q, want none", *created.SeriesName)
			}
			if after := sourceRequests(t, tt.status); after != before+1 {
				t.Errorf("tmdb %s requests went from %v to %v, want one more", tt.status, before, after)
			}
		})
	}
}

func TestCreateFilmSeriesKeepsTakenNumberAndLargerTotal(t *testing.T) {
	a := newFilmSeriesAPI(t, &tmdbDouble{})
	mine := film("Komnata tajemnic", filmThird)
	mine["series_name"], mine["series_number"] = "harry potter", 2
	var first collection.Title
	a.mustDo(http.MethodPost, "/api/titles", mine, &first, http.StatusCreated)
	a.mustDo(http.MethodPut, fmt.Sprintf("/api/series/%d", deref64(first.SeriesID)),
		map[string]any{"total_volumes": 8}, nil, http.StatusOK)

	var created collection.Title
	a.mustDo(http.MethodPost, "/api/titles", film("Harry Potter", filmSecond), &created, http.StatusCreated)

	if created.SeriesID == nil || *created.SeriesID != deref64(first.SeriesID) {
		t.Fatalf("series = %v, want the user's series %v", created.SeriesID, first.SeriesID)
	}
	if created.SeriesNumber != nil {
		t.Errorf("number = %d, want none: the user's film holds 2", *created.SeriesNumber)
	}
	if total := seriesTotal(a, created.SeriesID); total == nil || *total != 8 {
		t.Errorf("series total = %v, want the user's 8", intOrNil(total))
	}
}

func TestCreateFilmSeriesRaisesSmallerTotal(t *testing.T) {
	a := newFilmSeriesAPI(t, &tmdbDouble{})
	mine := map[string]any{"kind": collection.KindMovie, "name": "Kamień filozoficzny", "series_name": "Harry Potter"}
	var first collection.Title
	a.mustDo(http.MethodPost, "/api/titles", mine, &first, http.StatusCreated)
	a.mustDo(http.MethodPut, fmt.Sprintf("/api/series/%d", deref64(first.SeriesID)),
		map[string]any{"total_volumes": 2}, nil, http.StatusOK)

	var created collection.Title
	a.mustDo(http.MethodPost, "/api/titles", film("Więzień Azkabanu", filmThird), &created, http.StatusCreated)

	if created.SeriesNumber == nil || *created.SeriesNumber != 3 {
		t.Errorf("number = %v, want 3", intOrNil(created.SeriesNumber))
	}
	if total := seriesTotal(a, created.SeriesID); total == nil || *total != 3 {
		t.Errorf("series total = %v, want 3", intOrNil(total))
	}
}

func deref64(n *int64) int64 {
	if n == nil {
		return 0
	}
	return *n
}

// sourceRequests reads ohara_source_requests_total for tmdb and status.
func sourceRequests(t *testing.T, status string) float64 {
	t.Helper()
	rec := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", http.NoBody))
	prefix := fmt.Sprintf(`ohara_source_requests_total{source="tmdb",status=%q} `, status)
	sc := bufio.NewScanner(rec.Body)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), prefix); ok {
			n, err := strconv.ParseFloat(v, 64)
			if err != nil {
				t.Fatalf("metric %s: %v", sc.Text(), err)
			}
			return n
		}
	}
	return 0
}
