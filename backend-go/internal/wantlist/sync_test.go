package wantlist

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Automaat/ohara/backend-go/internal/dbtest"
	"github.com/Automaat/ohara/backend-go/internal/discogs"
	"github.com/Automaat/ohara/backend-go/internal/server"
)

const owner = "collector"

// fakeDiscogs is a Discogs API double holding one user's wantlist. A
// write numbered failWrite (from 1) answers 429 instead.
type fakeDiscogs struct {
	t *testing.T

	mu        sync.Mutex
	wants     []int64
	writes    []string
	pages     []int
	failWrite int
}

func newFakeDiscogs(t *testing.T, wants ...int64) (*fakeDiscogs, *discogs.Client) {
	t.Helper()
	f := &fakeDiscogs{t: t, wants: wants}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /oauth/identity", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]string{"username": owner})
	})
	mux.HandleFunc("GET /users/"+owner+"/wants", f.list)
	mux.HandleFunc("PUT /users/"+owner+"/wants/{id}", f.write)
	mux.HandleFunc("DELETE /users/"+owner+"/wants/{id}", f.write)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return f, &discogs.Client{HTTP: srv.Client(), BaseURL: srv.URL, Token: "token"}
}

func (f *fakeDiscogs) list(w http.ResponseWriter, r *http.Request) {
	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || page < 1 {
		page = 1
	}
	per, err := strconv.Atoi(r.URL.Query().Get("per_page"))
	if err != nil || per < 1 {
		per = 50
	}
	f.mu.Lock()
	f.pages = append(f.pages, page)
	all := slices.Clone(f.wants)
	f.mu.Unlock()
	pages := max(1, (len(all)+per-1)/per)
	type want struct {
		ID int64 `json:"id"`
	}
	body := struct {
		Pagination map[string]int `json:"pagination"`
		Wants      []want         `json:"wants"`
	}{Pagination: map[string]int{"page": page, "pages": pages, "per_page": per, "items": len(all)}, Wants: []want{}}
	for _, id := range all[min(len(all), (page-1)*per):min(len(all), page*per)] {
		body.Wants = append(body.Wants, want{ID: id})
	}
	writeJSON(f.t, w, body)
}

func (f *fakeDiscogs) write(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, r.Method+" "+strconv.FormatInt(id, 10))
	if len(f.writes) == f.failWrite {
		http.Error(w, `{"message": "You are making requests too quickly."}`, http.StatusTooManyRequests)
		return
	}
	at := slices.Index(f.wants, id)
	switch r.Method {
	case http.MethodPut:
		if at < 0 {
			f.wants = append(f.wants, id)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id": `+strconv.FormatInt(id, 10)+`, "rating": 0}`)
	case http.MethodDelete:
		if at < 0 {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		f.wants = slices.Delete(f.wants, at, at+1)
		w.WriteHeader(http.StatusNoContent)
	}
}

// took returns the writes since the last call and the wantlist now.
func (f *fakeDiscogs) took() ([]string, []int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	writes := f.writes
	f.writes = nil
	wants := slices.Clone(f.wants)
	slices.Sort(wants)
	return writes, wants
}

func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Errorf("encode: %v", err)
	}
}

// wish puts a copy of a new title of kind on the wishlist, its edition on
// Discogs release when release is not 0, and returns the copy's ID.
func wish(t *testing.T, pool *pgxpool.Pool, kind string, release int64) int64 {
	t.Helper()
	ctx := context.Background()
	format := map[string]string{"music": "cd", "movie": "dvd", "book": "paperback"}[kind]
	var rel *int64
	if release != 0 {
		rel = &release
	}
	var copyID int64
	err := pool.QueryRow(ctx, `WITH t AS (INSERT INTO title (kind, name) VALUES ($1, 'Album') RETURNING id),
		e AS (INSERT INTO edition (title_id, format, discogs_release_id) SELECT id, $2, $3 FROM t RETURNING id)
		INSERT INTO copy (edition_id, status) SELECT id, 'wishlist' FROM e RETURNING id`,
		kind, format, rel).Scan(&copyID)
	if err != nil {
		t.Fatalf("wish: %v", err)
	}
	return copyID
}

func exec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

func recorded(t *testing.T, store *Store) []int64 {
	t.Helper()
	ids, err := store.Added(context.Background(), owner)
	if err != nil {
		t.Fatalf("added: %v", err)
	}
	return ids
}

func mustSync(t *testing.T, s *Syncer) Result {
	t.Helper()
	res, err := s.Sync(context.Background())
	if err != nil {
		t.Fatalf("sync: %v (%+v)", err, res)
	}
	return res
}

func call(t *testing.T, method, url string, out any) int {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, url, http.NoBody)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("decode: %v", err)
		}
	}
	return resp.StatusCode
}

func serve(t *testing.T, pool *pgxpool.Pool, s *Syncer) string {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	srv := httptest.NewServer(server.New(server.Config{}, logger, server.Deps{
		DB: pool, API: []server.Mounter{NewHandler(s, logger)},
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/api/discogs/wantlist"
}

func TestSyncFromOkazje(t *testing.T) {
	pool := dbtest.Migrated(t)
	fake, client := newFakeDiscogs(t, 2)
	store := NewStore(pool)
	syncer := NewSyncer(store, client, slog.New(slog.DiscardHandler))
	base := serve(t, pool, syncer)
	wish(t, pool, "music", 1)
	wish(t, pool, "music", 2)
	wish(t, pool, "music", 0)
	wish(t, pool, "movie", 0)

	var st Status
	if code := call(t, http.MethodGet, base, &st); code != http.StatusOK || !st.Enabled || st.LastResult != nil {
		t.Fatalf("GET before = %d, %+v", code, st)
	}
	if code := call(t, http.MethodPost, base+"/sync", &st); code != http.StatusAccepted || !st.Running {
		t.Fatalf("POST sync = %d, %+v", code, st)
	}
	syncer.Wait()
	if code := call(t, http.MethodGet, base, &st); code != http.StatusOK || st.Running || st.LastFinishedAt == nil {
		t.Fatalf("GET after = %d, %+v", code, st)
	}
	if got, want := *st.LastResult, (Result{Added: 1, Already: 1, Skipped: 1}); got != want {
		t.Fatalf("result = %+v, want %+v", got, want)
	}
	writes, wants := fake.took()
	if !slices.Equal(writes, []string{"PUT 1"}) || !slices.Equal(wants, []int64{1, 2}) {
		t.Fatalf("writes %v, wantlist %v", writes, wants)
	}
	if got := recorded(t, store); !slices.Equal(got, []int64{1}) {
		t.Fatalf("recorded %v, want only the want Ohara added", got)
	}
}

func TestSyncDisabledWithoutToken(t *testing.T) {
	pool := dbtest.Migrated(t)
	syncer := NewSyncer(NewStore(pool), nil, slog.New(slog.DiscardHandler))
	base := serve(t, pool, syncer)

	var st Status
	if code := call(t, http.MethodGet, base, &st); code != http.StatusOK || st.Enabled {
		t.Fatalf("GET = %d, %+v", code, st)
	}
	var body struct{ Error string }
	if code := call(t, http.MethodPost, base+"/sync", &body); code != http.StatusServiceUnavailable || body.Error == "" {
		t.Fatalf("POST sync = %d, %+v", code, body)
	}
	if syncer.Trigger() {
		t.Fatal("a disabled syncer started a sync")
	}
}

func TestSyncRemovesOnlyTheWantsOharaAdded(t *testing.T) {
	pool := dbtest.Migrated(t)
	fake, client := newFakeDiscogs(t, 5, 9)
	store := NewStore(pool)
	syncer := NewSyncer(store, client, nil)
	deleted := wish(t, pool, "music", 3)
	bought := wish(t, pool, "music", 4)
	before := wish(t, pool, "music", 5)
	wish(t, pool, "music", 6)

	if got, want := mustSync(t, syncer), (Result{Added: 3, Already: 1}); got != want {
		t.Fatalf("first sync = %+v, want %+v", got, want)
	}
	fake.took()

	exec(t, pool, `DELETE FROM copy WHERE id = $1`, deleted)
	exec(t, pool, `UPDATE copy SET status = 'owned' WHERE id = $1`, bought)
	exec(t, pool, `DELETE FROM copy WHERE id = $1`, before)
	if got, want := mustSync(t, syncer), (Result{Already: 1, Removed: 2}); got != want {
		t.Fatalf("second sync = %+v, want %+v", got, want)
	}
	writes, wants := fake.took()
	if !slices.Equal(writes, []string{"DELETE 3", "DELETE 4"}) {
		t.Fatalf("writes %v, want the two wants Ohara added", writes)
	}
	if !slices.Equal(wants, []int64{5, 6, 9}) {
		t.Fatalf("wantlist %v, want the earlier wants kept", wants)
	}
	if got := recorded(t, store); !slices.Equal(got, []int64{6}) {
		t.Fatalf("recorded %v", got)
	}
}

func TestSecondSyncWritesNothing(t *testing.T) {
	pool := dbtest.Migrated(t)
	fake, client := newFakeDiscogs(t, 8)
	syncer := NewSyncer(NewStore(pool), client, nil)
	wish(t, pool, "music", 7)
	wish(t, pool, "music", 8)
	mustSync(t, syncer)
	if writes, _ := fake.took(); len(writes) != 1 {
		t.Fatalf("first sync wrote %v", writes)
	}

	if got, want := mustSync(t, syncer), (Result{Already: 2}); got != want {
		t.Fatalf("second sync = %+v, want %+v", got, want)
	}
	if writes, _ := fake.took(); len(writes) != 0 {
		t.Fatalf("second sync wrote %v", writes)
	}
}

func TestSyncReadsTheWholeWantlist(t *testing.T) {
	pool := dbtest.Migrated(t)
	long := make([]int64, 0, 150)
	for id := int64(1001); id <= 1150; id++ {
		long = append(long, id)
	}
	fake, client := newFakeDiscogs(t, long...)
	syncer := NewSyncer(NewStore(pool), client, nil)
	wish(t, pool, "music", 1150)

	if got, want := mustSync(t, syncer), (Result{Already: 1}); got != want {
		t.Fatalf("sync = %+v, want %+v", got, want)
	}
	if writes, _ := fake.took(); len(writes) != 0 {
		t.Fatalf("wrote %v for a want on the second page", writes)
	}
	if !slices.Equal(fake.pages, []int{1, 2}) {
		t.Fatalf("read pages %v", fake.pages)
	}
}

func TestSyncFailureKeepsWhatSucceeded(t *testing.T) {
	pool := dbtest.Migrated(t)
	fake, client := newFakeDiscogs(t)
	store := NewStore(pool)
	syncer := NewSyncer(store, client, slog.New(slog.DiscardHandler))
	base := serve(t, pool, syncer)
	for _, release := range []int64{11, 12, 13} {
		wish(t, pool, "music", release)
	}
	fake.failWrite = 2

	if code := call(t, http.MethodPost, base+"/sync", nil); code != http.StatusAccepted {
		t.Fatalf("POST sync = %d", code)
	}
	syncer.Wait()
	var st Status
	call(t, http.MethodGet, base, &st)
	if st.LastResult == nil || st.LastResult.Added != 1 || !strings.Contains(st.LastResult.Failure, "429") {
		t.Fatalf("failed sync = %+v", st.LastResult)
	}
	if writes, wants := fake.took(); !slices.Equal(writes, []string{"PUT 11", "PUT 12"}) ||
		!slices.Equal(wants, []int64{11}) {
		t.Fatalf("writes %v, wantlist %v", writes, wants)
	}
	if got := recorded(t, store); !slices.Equal(got, []int64{11}) {
		t.Fatalf("recorded %v, want only the write that succeeded", got)
	}

	fake.failWrite = 0
	if got, want := mustSync(t, syncer), (Result{Added: 2, Already: 1}); got != want {
		t.Fatalf("retry = %+v, want %+v", got, want)
	}
	if got := recorded(t, store); !slices.Equal(got, []int64{11, 12, 13}) {
		t.Fatalf("recorded %v", got)
	}
}
