package scanqueue_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Automaat/ohara/backend-go/internal/collection"
	"github.com/Automaat/ohara/backend-go/internal/dbtest"
	"github.com/Automaat/ohara/backend-go/internal/scanqueue"
	"github.com/Automaat/ohara/backend-go/internal/server"
)

type obj = map[string]any

type api struct {
	t   *testing.T
	url string
}

func newAPI(t *testing.T) *api {
	t.Helper()
	pool := dbtest.Migrated(t)
	logger := slog.New(slog.DiscardHandler)
	srv := httptest.NewServer(server.New(server.Config{}, logger, server.Deps{DB: pool, API: []server.Mounter{
		collection.NewHandler(collection.NewStore(pool), logger),
		scanqueue.NewHandler(scanqueue.NewStore(pool), logger),
	}}))
	t.Cleanup(srv.Close)
	return &api{t: t, url: srv.URL}
}

// do sends body as JSON, decodes a non-empty response into out and returns
// the status.
func (a *api) do(method, path string, body, out any) int {
	a.t.Helper()
	var r io.Reader = http.NoBody
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			a.t.Fatalf("marshal: %v", err)
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, a.url+path, r)
	if err != nil {
		a.t.Fatalf("request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		a.t.Fatalf("read: %v", err)
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			a.t.Fatalf("%s %s: decode %q: %v", method, path, raw, err)
		}
	}
	return resp.StatusCode
}

func (a *api) mustDo(method, path string, body, out any, want int) {
	a.t.Helper()
	if got := a.do(method, path, body, out); got != want {
		a.t.Fatalf("%s %s = %d, want %d", method, path, got, want)
	}
}

func (a *api) queue() []scanqueue.Entry {
	a.t.Helper()
	var entries []scanqueue.Entry
	a.mustDo(http.MethodGet, "/api/scan-queue", nil, &entries, http.StatusOK)
	return entries
}

func barcodes(entries []scanqueue.Entry) string {
	codes := make([]string, 0, len(entries))
	for i := range entries {
		codes = append(codes, entries[i].Barcode)
	}
	return strings.Join(codes, ",")
}

func entryPath(id int64) string {
	return "/api/scan-queue/" + strconv.FormatInt(id, 10)
}

func TestQueueLifecycle(t *testing.T) {
	a := newAPI(t)
	if got := a.queue(); len(got) != 0 {
		t.Fatalf("empty queue = %v", got)
	}

	var first scanqueue.Entry
	a.mustDo(http.MethodPost, "/api/scan-queue",
		obj{"barcode": "590-1234-123457", "reason": "not_found"}, &first, http.StatusCreated)
	if first.Barcode != "5901234123457" || first.Reason != "not_found" || first.Scans != 1 {
		t.Fatalf("created entry = %+v", first)
	}

	var again scanqueue.Entry
	a.mustDo(http.MethodPost, "/api/scan-queue",
		obj{"barcode": "5901234123457", "reason": "lookup_failed", "detail": "  bn: timeout  "}, &again, http.StatusOK)
	if again.ID != first.ID || again.Scans != 2 || again.Reason != "lookup_failed" || again.Detail != "bn: timeout" {
		t.Fatalf("rescanned entry = %+v, first %+v", again, first)
	}
	if again.LastScannedAt.Before(first.LastScannedAt) {
		t.Fatalf("last_scanned_at went back: %v -> %v", first.LastScannedAt, again.LastScannedAt)
	}

	a.mustDo(http.MethodPost, "/api/scan-queue", obj{"barcode": "0-306-40615-2", "reason": "not_found"}, nil, http.StatusCreated)
	a.mustDo(http.MethodPost, "/api/scan-queue", obj{"barcode": "036000291452", "reason": "not_found"}, nil, http.StatusCreated)
	if got, want := barcodes(a.queue()), "0036000291452,9780306406157,5901234123457"; got != want {
		t.Fatalf("queue (normalized, newest first) = %s, want %s", got, want)
	}

	a.mustDo(http.MethodDelete, entryPath(first.ID), nil, nil, http.StatusNoContent)
	a.mustDo(http.MethodDelete, entryPath(first.ID), nil, nil, http.StatusNotFound)
	a.mustDo(http.MethodDelete, "/api/scan-queue/abc", nil, nil, http.StatusNotFound)
	if got, want := barcodes(a.queue()), "0036000291452,9780306406157"; got != want {
		t.Fatalf("queue after delete = %s, want %s", got, want)
	}
}

func TestQueueRejectsBadInput(t *testing.T) {
	a := newAPI(t)
	tests := []struct {
		name  string
		body  any
		field string
	}{
		{"bad check digit", obj{"barcode": "5901234123458", "reason": "not_found"}, "barcode"},
		{"not a barcode", obj{"barcode": "abc", "reason": "not_found"}, "barcode"},
		{"unknown reason", obj{"barcode": "5901234123457", "reason": "bored"}, "reason"},
		{"missing reason", obj{"barcode": "5901234123457"}, "reason"},
		{"not an object", "nope", "body"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var e struct {
				Field string `json:"field"`
			}
			if got := a.do(http.MethodPost, "/api/scan-queue", tt.body, &e); got != http.StatusUnprocessableEntity || e.Field != tt.field {
				t.Fatalf("status %d field %q, want 422 on %q", got, e.Field, tt.field)
			}
		})
	}
	if got := a.queue(); len(got) != 0 {
		t.Fatalf("queue after rejects = %v", got)
	}
}

func TestQueueDetailIsCut(t *testing.T) {
	a := newAPI(t)
	var e scanqueue.Entry
	a.mustDo(http.MethodPost, "/api/scan-queue",
		obj{"barcode": "5901234123457", "reason": "lookup_failed", "detail": strings.Repeat("ż", 600)}, &e, http.StatusCreated)
	if n := utf8.RuneCountInString(e.Detail); n != 500 || !utf8.ValidString(e.Detail) {
		t.Fatalf("detail has %d runes, valid %v", n, utf8.ValidString(e.Detail))
	}
}

func TestEditionResolvesQueueEntry(t *testing.T) {
	a := newAPI(t)
	a.mustDo(http.MethodPost, "/api/scan-queue", obj{"barcode": "5901234123457", "reason": "not_found"}, nil, http.StatusCreated)
	a.mustDo(http.MethodPost, "/api/scan-queue", obj{"barcode": "9780306406157", "reason": "not_found"}, nil, http.StatusCreated)

	var movie, book collection.Title
	a.mustDo(http.MethodPost, "/api/titles", obj{"kind": "movie", "name": "Rejs"}, &movie, http.StatusCreated)
	a.mustDo(http.MethodPost, "/api/titles", obj{"kind": "book", "name": "Lalka"}, &book, http.StatusCreated)

	a.mustDo(http.MethodPost, "/api/editions",
		obj{"title_id": movie.ID, "barcode": "5901234123457", "format": "dvd"}, nil, http.StatusCreated)
	if got := barcodes(a.queue()); got != "9780306406157" {
		t.Fatalf("queue after creating an edition = %s", got)
	}

	var e collection.Edition
	a.mustDo(http.MethodPost, "/api/editions", obj{"title_id": book.ID}, &e, http.StatusCreated)
	a.mustDo(http.MethodPut, "/api/editions/"+strconv.FormatInt(e.ID, 10),
		obj{"title_id": book.ID, "barcode": "0306406152"}, nil, http.StatusOK)
	if got := a.queue(); len(got) != 0 {
		t.Fatalf("queue after setting an edition's barcode = %v", got)
	}

	var conflict struct {
		Field string `json:"field"`
	}
	if got := a.do(http.MethodPost, "/api/scan-queue", obj{"barcode": "5901234123457", "reason": "not_found"}, &conflict); got != http.StatusConflict || conflict.Field != "barcode" {
		t.Fatalf("queueing a barcode in the collection = %d %+v, want 409 on barcode", got, conflict)
	}
}

func TestStoreCount(t *testing.T) {
	pool := dbtest.Migrated(t)
	store := scanqueue.NewStore(pool)
	ctx := context.Background()
	for _, code := range []string{"5901234123457", "5901234123457", "9788375780635"} {
		if _, _, err := store.Add(ctx, code, "not_found", ""); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := store.Count(ctx); err != nil || n != 2 {
		t.Fatalf("count = %d, %v; want 2 distinct barcodes", n, err)
	}
}
