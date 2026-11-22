package collection_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Automaat/ohara/backend-go/internal/collection"
	"github.com/Automaat/ohara/backend-go/internal/dbtest"
	"github.com/Automaat/ohara/backend-go/internal/server"
)

type api struct {
	t   *testing.T
	url string
}

func newAPI(t *testing.T) *api {
	t.Helper()
	a, _ := newAPIWithPool(t)
	return a
}

// newAPIWithPool is newAPI that also hands out its database, for tests that
// hold a transaction open while a request runs.
func newAPIWithPool(t *testing.T) (*api, *pgxpool.Pool) {
	t.Helper()
	pool := dbtest.Migrated(t)
	logger := slog.New(slog.DiscardHandler)
	h := collection.NewHandler(collection.NewStore(pool), logger)
	srv := httptest.NewServer(server.New(server.Config{}, logger, server.Deps{DB: pool, API: []server.Mounter{h}}))
	t.Cleanup(srv.Close)
	return &api{t: t, url: srv.URL}, pool
}

// do sends body as JSON and decodes the response into out when out is not nil.
func (a *api) do(method, path string, body, out any) int {
	a.t.Helper()
	var r io.Reader = http.NoBody
	if binary, ok := body.([]byte); ok {
		r = bytes.NewReader(binary)
	} else if body != nil {
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
	if binary, ok := out.(*[]byte); ok {
		*binary = raw
	} else if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			a.t.Fatalf("%s %s: decode %q: %v", method, path, raw, err)
		}
	}
	return resp.StatusCode
}

// mustDo is do with an expected status.
func (a *api) mustDo(method, path string, body, out any, want int) {
	a.t.Helper()
	var raw json.RawMessage
	got := a.do(method, path, body, &raw)
	if got != want {
		a.t.Fatalf("%s %s = %d, want %d: %s", method, path, got, want, raw)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			a.t.Fatalf("decode %s: %v", raw, err)
		}
	}
}

// fails asserts an error status naming field.
func (a *api) fails(method, path string, body any, want int, field string) {
	a.t.Helper()
	var e struct {
		Error string `json:"error"`
		Field string `json:"field"`
	}
	got := a.do(method, path, body, &e)
	if got != want || e.Field != field || e.Error == "" {
		a.t.Fatalf("%s %s = %d %+v, want %d on field %q", method, path, got, e, want, field)
	}
}

type obj = map[string]any

func (a *api) title(kind, name string) int64 {
	a.t.Helper()
	var t collection.Title
	a.mustDo(http.MethodPost, "/api/titles", obj{"kind": kind, "name": name}, &t, http.StatusCreated)
	return t.ID
}

func (a *api) edition(body obj) int64 {
	a.t.Helper()
	var e collection.Edition
	a.mustDo(http.MethodPost, "/api/editions", body, &e, http.StatusCreated)
	return e.ID
}

func (a *api) copyOf(body obj) int64 {
	a.t.Helper()
	var c collection.Copy
	a.mustDo(http.MethodPost, "/api/copies", body, &c, http.StatusCreated)
	return c.ID
}

func TestEditionDetailReturnsArtworkForEachEdition(t *testing.T) {
	a := newAPI(t)
	const representative = "https://example.test/title.jpg"
	var title collection.Title
	a.mustDo(http.MethodPost, "/api/titles", obj{
		"kind": "movie", "name": "Seksmisja", "cover_url": representative,
	}, &title, http.StatusCreated)
	a.mustDo(http.MethodPut, fmt.Sprintf("/api/titles/%d", title.ID), obj{
		"kind": "movie", "name": "Seksmisja",
	}, nil, http.StatusOK)

	for _, tt := range []struct {
		name  string
		cover string
	}{
		{name: "First release", cover: "https://example.test/first.jpg"},
		{name: "Second release", cover: "https://example.test/second.jpg"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sub := &api{t: t, url: a.url}
			editionID := sub.edition(obj{
				"title_id": title.ID, "label": tt.name, "format": "dvd", "cover_url": tt.cover,
			})
			path := fmt.Sprintf("/api/editions/%d", editionID)
			sub.mustDo(http.MethodPut, path, obj{
				"title_id": title.ID, "label": tt.name, "format": "dvd",
			}, nil, http.StatusOK)

			var detail collection.EditionDetail
			sub.mustDo(http.MethodGet, path, nil, &detail, http.StatusOK)

			if detail.CoverURL == nil || *detail.CoverURL != tt.cover {
				t.Errorf("edition cover = %v, want %q", detail.CoverURL, tt.cover)
			}
			if detail.Title.CoverURL == nil || *detail.Title.CoverURL != representative {
				t.Errorf("title cover = %v, want %q", detail.Title.CoverURL, representative)
			}
		})
	}
}

func TestRejectsInvalidEditionArtworkURLs(t *testing.T) {
	a := newAPI(t)
	titleID := a.title("movie", "Seksmisja")
	for _, cover := range []string{"javascript:x", "//example.test/cover.jpg", "https://u:p@example.test/cover.jpg"} {
		a.fails(http.MethodPost, "/api/editions", obj{
			"title_id": titleID, "format": "dvd", "cover_url": cover,
		}, http.StatusUnprocessableEntity, "cover_url")
	}
}

func coverPNG(t *testing.T, width int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, width, 1))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestEditionCoverLifecycle(t *testing.T) {
	a := newAPI(t)
	titleID := a.title("movie", "Seksmisja")
	const provider = "https://example.test/first.jpg"
	const siblingCover = "https://example.test/second.jpg"
	edition := a.edition(obj{"title_id": titleID, "format": "dvd", "cover_url": provider})
	sibling := a.edition(obj{"title_id": titleID, "format": "dvd", "cover_url": siblingCover})
	a.copyOf(obj{"edition_id": edition, "status": "owned"})
	a.copyOf(obj{"edition_id": sibling, "status": "owned"})
	detailPath := fmt.Sprintf("/api/editions/%d", edition)
	coverPath := detailPath + "/cover"

	assertCovers := func(want string) {
		t.Helper()
		var view collection.CollectionView
		a.mustDo(http.MethodGet, "/api/collection", nil, &view, http.StatusOK)
		if len(view.Items) != 2 {
			t.Fatalf("got %d copies, want 2", len(view.Items))
		}
		covers := map[int64]string{edition: want, sibling: siblingCover}
		for _, item := range view.Items {
			if item.CoverURL == nil || *item.CoverURL != covers[item.EditionID] {
				t.Errorf("edition %d cover = %v, want %q", item.EditionID, item.CoverURL, covers[item.EditionID])
			}
		}
	}
	assertOverride := func(photo []byte) string {
		t.Helper()
		var detail collection.EditionDetail
		a.mustDo(http.MethodGet, detailPath, nil, &detail, http.StatusOK)
		if detail.CoverOverrideURL == nil || !strings.HasPrefix(*detail.CoverOverrideURL, coverPath+"?") {
			t.Fatalf("missing cover override: %+v", detail.CoverOverrideURL)
		}
		if detail.CoverURL == nil || *detail.CoverURL != provider {
			t.Fatalf("provider cover changed: %+v", detail.CoverURL)
		}
		var got []byte
		if status := a.do(http.MethodGet, *detail.CoverOverrideURL, nil, &got); status != http.StatusOK || !bytes.Equal(got, photo) {
			t.Fatalf("override image = %d, %d bytes, want %d bytes", status, len(got), len(photo))
		}
		assertCovers(*detail.CoverOverrideURL)
		return *detail.CoverOverrideURL
	}

	first, second := coverPNG(t, 1), coverPNG(t, 2)
	a.mustDo(http.MethodPut, coverPath, first, nil, http.StatusNoContent)
	firstURL := assertOverride(first)

	a.fails(http.MethodPut, coverPath, first[:40], http.StatusUnprocessableEntity, "cover_photo")
	a.fails(http.MethodPut, coverPath, make([]byte, (8<<20)+1), http.StatusRequestEntityTooLarge, "cover_photo")
	if assertOverride(first) != firstURL {
		t.Fatal("a failed replacement changed the cover URL")
	}

	a.mustDo(http.MethodPut, coverPath, second, nil, http.StatusNoContent)
	if assertOverride(second) == firstURL {
		t.Fatal("replacement reused the previous cover URL")
	}

	a.mustDo(http.MethodDelete, coverPath, nil, nil, http.StatusNoContent)
	a.mustDo(http.MethodGet, coverPath, nil, nil, http.StatusNotFound)
	var detail collection.EditionDetail
	a.mustDo(http.MethodGet, detailPath, nil, &detail, http.StatusOK)
	if detail.CoverOverrideURL != nil || detail.CoverURL == nil || *detail.CoverURL != provider {
		t.Fatal("removal did not restore the provider cover")
	}
	assertCovers(provider)
}

// get sends a GET to path and returns its status and headers.
func (a *api) get(path string) (int, http.Header) {
	a.t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, a.url+path, http.NoBody)
	if err != nil {
		a.t.Fatalf("request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		a.t.Fatalf("read: %v", err)
	}
	return resp.StatusCode, resp.Header
}

func TestEditionCoverCaching(t *testing.T) {
	a := newAPI(t)
	edition := a.edition(obj{"title_id": a.title("movie", "Seksmisja"), "format": "dvd"})
	detailPath := fmt.Sprintf("/api/editions/%d", edition)
	coverPath := detailPath + "/cover"
	coverURL := func() string {
		t.Helper()
		var detail collection.EditionDetail
		a.mustDo(http.MethodGet, detailPath, nil, &detail, http.StatusOK)
		if detail.CoverOverrideURL == nil {
			t.Fatal("missing cover override")
		}
		return *detail.CoverOverrideURL
	}
	const noStore = "no-store"
	const immutable = "private, max-age=31536000, immutable"

	a.mustDo(http.MethodPut, coverPath, coverPNG(t, 1), nil, http.StatusNoContent)
	first := coverURL()
	a.mustDo(http.MethodPut, coverPath, coverPNG(t, 2), nil, http.StatusNoContent)
	current := coverURL()

	tests := []struct {
		name   string
		path   string
		status int
		cache  string
	}{
		{name: "current version", path: current, status: http.StatusOK, cache: immutable},
		{name: "no version", path: coverPath, status: http.StatusOK, cache: noStore},
		{name: "empty version", path: coverPath + "?v=", status: http.StatusOK, cache: noStore},
		{name: "replaced version", path: first, status: http.StatusOK, cache: noStore},
		{name: "unknown version", path: coverPath + "?v=1", status: http.StatusOK, cache: noStore},
		{name: "current then replaced version", path: current + "&" + first[strings.Index(first, "?")+1:], status: http.StatusOK, cache: noStore},
		{name: "current version twice", path: current + "&" + current[strings.Index(current, "?")+1:], status: http.StatusOK, cache: noStore},
		{name: "malformed query before current version", path: coverPath + "?v=%ZZ&" + current[strings.Index(current, "?")+1:], status: http.StatusOK, cache: noStore},
		{name: "current version with another parameter", path: current + "&size=large", status: http.StatusOK, cache: immutable},
		{name: "missing edition", path: fmt.Sprintf("/api/editions/%d/cover?v=1", edition+1000), status: http.StatusNotFound, cache: noStore},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, h := a.get(tt.path)
			if status != tt.status {
				t.Fatalf("GET %s = %d, want %d", tt.path, status, tt.status)
			}
			if got := h.Get("Cache-Control"); got != tt.cache {
				t.Errorf("Cache-Control = %q, want %q", got, tt.cache)
			}
			if h.Get("X-Request-Id") == "" || h.Get("X-Frame-Options") != "DENY" || h.Get("X-Content-Type-Options") != "nosniff" {
				t.Errorf("missing request ID or security headers: %v", h)
			}
		})
	}

	a.mustDo(http.MethodDelete, coverPath, nil, nil, http.StatusNoContent)
	if status, h := a.get(current); status != http.StatusNotFound || h.Get("Cache-Control") != noStore {
		t.Errorf("removed photo = %d %q, want 404 %q", status, h.Get("Cache-Control"), noStore)
	}
}

func TestCRUDLifecycle(t *testing.T) {
	a := newAPI(t)

	var title collection.Title
	a.mustDo(http.MethodPost, "/api/titles", obj{
		"kind": "movie", "name": "Seksmisja", "polish_title": "Seksmisja", "original_title": "Seksmisja",
		"year": 1984, "tmdb_id": 11906,
	}, &title, http.StatusCreated)
	if title.ID == 0 || title.Kind != "movie" || *title.TMDBID != 11906 {
		t.Fatalf("created title = %+v", title)
	}

	editionID := a.edition(obj{
		"title_id": title.ID, "barcode": "590-1234-12345-7", "format": "bluray", "publisher": "Best Film",
		"pl_subtitles": "yes", "pl_voiceover": "yes", "packaging": []string{"steelbook"},
	})
	copyID := a.copyOf(obj{
		"edition_id": editionID, "status": "owned", "price_cents": 4999, "purchased_on": "2026-11-01",
		"shop": "OLX", "acquired_as": "used", "condition": "very_good",
	})

	var detail collection.TitleDetail
	a.mustDo(http.MethodGet, fmt.Sprintf("/api/titles/%d", title.ID), nil, &detail, http.StatusOK)
	if detail.EditionCount != 1 || detail.CopyCount != 1 || len(detail.Editions) != 1 || len(detail.Editions[0].Copies) != 1 {
		t.Fatalf("title detail = %+v", detail)
	}
	if got := *detail.Editions[0].Barcode; got != "5901234123457" {
		t.Fatalf("barcode stored as %q, want normalized", got)
	}

	var ed collection.EditionDetail
	a.mustDo(http.MethodGet, fmt.Sprintf("/api/editions/%d", editionID), nil, &ed, http.StatusOK)
	if ed.Title.ID != title.ID || len(ed.Copies) != 1 || ed.Packaging[0] != "steelbook" {
		t.Fatalf("edition detail = %+v", ed)
	}

	var updated collection.Copy
	a.mustDo(http.MethodPut, fmt.Sprintf("/api/copies/%d", copyID), obj{
		"edition_id": editionID, "status": "owned", "price_cents": 3999, "shop": "Allegro",
	}, &updated, http.StatusOK)
	if *updated.PriceCents != 3999 || updated.Shop != "Allegro" || updated.PurchasedOn != nil {
		t.Fatalf("updated copy = %+v", updated)
	}
	var cd collection.CopyDetail
	a.mustDo(http.MethodGet, fmt.Sprintf("/api/copies/%d", copyID), nil, &cd, http.StatusOK)
	if cd.Edition.ID != editionID || cd.Title.Name != "Seksmisja" {
		t.Fatalf("copy detail = %+v", cd)
	}

	a.mustDo(http.MethodPut, fmt.Sprintf("/api/titles/%d", title.ID), obj{"kind": "movie", "name": "Seksmisja (1984)"}, nil, http.StatusOK)
	a.mustDo(http.MethodPut, fmt.Sprintf("/api/editions/%d", editionID), obj{"title_id": title.ID, "format": "dvd"}, nil, http.StatusOK)

	a.fails(http.MethodDelete, fmt.Sprintf("/api/titles/%d", title.ID), nil, http.StatusConflict, "")
	a.fails(http.MethodDelete, fmt.Sprintf("/api/editions/%d", editionID), nil, http.StatusConflict, "")
	a.mustDo(http.MethodDelete, fmt.Sprintf("/api/copies/%d", copyID), nil, nil, http.StatusNoContent)
	a.mustDo(http.MethodDelete, fmt.Sprintf("/api/editions/%d", editionID), nil, nil, http.StatusNoContent)
	a.mustDo(http.MethodDelete, fmt.Sprintf("/api/titles/%d", title.ID), nil, nil, http.StatusNoContent)

	for _, path := range []string{"/api/titles/%d", "/api/editions/%d", "/api/copies/%d"} {
		for _, method := range []string{http.MethodGet, http.MethodDelete} {
			a.fails(method, fmt.Sprintf(path, 999999), nil, http.StatusNotFound, "")
		}
	}
	a.fails(http.MethodGet, "/api/titles/abc", nil, http.StatusNotFound, "")
	a.fails(http.MethodPut, "/api/titles/999999", obj{"kind": "movie", "name": "x"}, http.StatusNotFound, "")
	a.fails(http.MethodPut, "/api/editions/999999", obj{"title_id": a.title("book", "B"), "binding": "other"}, http.StatusNotFound, "")
	a.fails(http.MethodPut, "/api/copies/999999", obj{"edition_id": 1, "status": "owned"}, http.StatusNotFound, "")
}

func TestOneEditionPerBarcode(t *testing.T) {
	a := newAPI(t)
	book := a.title("book", "Programming Pearls")
	first := a.edition(obj{"title_id": book, "barcode": "9780306406157", "binding": "paperback"})

	a.fails(http.MethodPost, "/api/editions", obj{"title_id": book, "barcode": "978-0-306-40615-7"}, http.StatusConflict, "barcode")
	a.fails(http.MethodPost, "/api/editions", obj{"title_id": book, "barcode": "0306406152"}, http.StatusConflict, "barcode")

	other := a.title("book", "Other")
	a.fails(http.MethodPost, "/api/editions", obj{"title_id": other, "barcode": "9780306406157"}, http.StatusConflict, "barcode")

	a.edition(obj{"title_id": book})
	a.edition(obj{"title_id": book, "barcode": ""})
	second := a.edition(obj{"title_id": book, "barcode": "9780804429573"})
	a.fails(http.MethodPut, fmt.Sprintf("/api/editions/%d", second), obj{"title_id": book, "barcode": "9780306406157"}, http.StatusConflict, "barcode")
	a.mustDo(http.MethodPut, fmt.Sprintf("/api/editions/%d", first), obj{"title_id": book, "barcode": "9780306406157", "binding": "hardcover"}, nil, http.StatusOK)

	var found []collection.Edition
	a.mustDo(http.MethodGet, "/api/editions?barcode=0-306-40615-2", nil, &found, http.StatusOK)
	if len(found) != 1 || found[0].ID != first {
		t.Fatalf("editions by barcode = %+v", found)
	}
	a.fails(http.MethodGet, "/api/editions?barcode=123", nil, http.StatusUnprocessableEntity, "barcode")
	a.fails(http.MethodPost, "/api/editions", obj{"title_id": book, "barcode": "9780306406158"}, http.StatusUnprocessableEntity, "barcode")

	var byTitle []collection.Edition
	a.mustDo(http.MethodGet, fmt.Sprintf("/api/editions?title_id=%d", book), nil, &byTitle, http.StatusOK)
	if len(byTitle) != 4 {
		t.Fatalf("editions of title = %d, want 4", len(byTitle))
	}
	a.fails(http.MethodGet, "/api/editions?title_id=x", nil, http.StatusUnprocessableEntity, "title_id")
}

func TestCopyBelongsToEdition(t *testing.T) {
	a := newAPI(t)
	movie := a.title("movie", "Rejs")
	edition := a.edition(obj{"title_id": movie, "format": "dvd"})
	copyID := a.copyOf(obj{"edition_id": edition, "status": "wishlist"})

	a.fails(http.MethodPost, "/api/copies", obj{"status": "owned"}, http.StatusUnprocessableEntity, "edition_id")
	a.fails(http.MethodPost, "/api/copies", obj{"edition_id": 999999, "status": "owned"}, http.StatusUnprocessableEntity, "edition_id")
	a.fails(http.MethodPut, fmt.Sprintf("/api/copies/%d", copyID), obj{"edition_id": 999999, "status": "owned"}, http.StatusUnprocessableEntity, "edition_id")
	a.fails(http.MethodPost, "/api/copies", obj{"edition_id": edition, "status": "wishlist", "price_cents": 100}, http.StatusUnprocessableEntity, "price_cents")
	a.fails(http.MethodPost, "/api/copies", obj{"edition_id": edition, "status": "owned", "price_cents": int64(9223372036854775807)}, http.StatusUnprocessableEntity, "price_cents")

	other := a.edition(obj{"title_id": movie, "format": "uhd"})
	var moved collection.Copy
	a.mustDo(http.MethodPut, fmt.Sprintf("/api/copies/%d", copyID), obj{"edition_id": other, "status": "owned", "price_cents": 12000}, &moved, http.StatusOK)
	if moved.EditionID != other || moved.Status != "owned" {
		t.Fatalf("moved copy = %+v", moved)
	}
}

func TestWishlistMaxPrice(t *testing.T) {
	a := newAPI(t)
	movie := a.title("movie", "Rejs")
	edition := a.edition(obj{"title_id": movie, "format": "dvd"})

	var c collection.Copy
	a.mustDo(http.MethodPost, "/api/copies", obj{"edition_id": edition, "status": "wishlist", "max_price_cents": 2500}, &c, http.StatusCreated)
	if c.MaxPriceCents == nil || *c.MaxPriceCents != 2500 {
		t.Fatalf("created copy = %+v", c)
	}
	path := fmt.Sprintf("/api/copies/%d", c.ID)
	a.mustDo(http.MethodPut, path, obj{"edition_id": edition, "status": "wishlist", "max_price_cents": 3000}, &c, http.StatusOK)
	if *c.MaxPriceCents != 3000 {
		t.Fatalf("updated max price = %d", *c.MaxPriceCents)
	}
	a.fails(http.MethodPut, path, obj{"edition_id": edition, "status": "owned", "max_price_cents": 3000}, http.StatusUnprocessableEntity, "max_price_cents")
	a.mustDo(http.MethodPut, path, obj{"edition_id": edition, "status": "owned", "price_cents": 2000}, &c, http.StatusOK)
	if c.MaxPriceCents != nil {
		t.Fatalf("bought copy kept max price %d", *c.MaxPriceCents)
	}
}

func TestWishlistMarketPercent(t *testing.T) {
	a := newAPI(t)
	movie := a.title("movie", "Rejs")
	edition := a.edition(obj{"title_id": movie, "format": "dvd"})

	var c collection.Copy
	a.mustDo(http.MethodPost, "/api/copies", obj{"edition_id": edition, "status": "wishlist", "max_market_percent": 70}, &c, http.StatusCreated)
	if c.MaxMarketPercent == nil || *c.MaxMarketPercent != 70 || c.MaxPriceCents != nil {
		t.Fatalf("created copy = %+v", c)
	}
	path := fmt.Sprintf("/api/copies/%d", c.ID)
	a.mustDo(http.MethodPut, path, obj{"edition_id": edition, "status": "wishlist", "max_price_cents": 3000, "max_market_percent": 80}, &c, http.StatusOK)
	if *c.MaxMarketPercent != 80 || *c.MaxPriceCents != 3000 {
		t.Fatalf("updated copy = %+v", c)
	}
	for _, percent := range []int{0, 101} {
		a.fails(http.MethodPut, path, obj{"edition_id": edition, "status": "wishlist", "max_market_percent": percent}, http.StatusUnprocessableEntity, "max_market_percent")
	}
	a.fails(http.MethodPut, path, obj{"edition_id": edition, "status": "owned", "max_market_percent": 80}, http.StatusUnprocessableEntity, "max_market_percent")
	var kept collection.Copy
	a.mustDo(http.MethodGet, path, nil, &kept, http.StatusOK)
	if kept.MaxMarketPercent == nil || *kept.MaxMarketPercent != 80 {
		t.Fatalf("refused updates changed the copy to %+v", kept)
	}
	var bought collection.Copy
	a.mustDo(http.MethodPut, path, obj{"edition_id": edition, "status": "owned", "price_cents": 2000}, &bought, http.StatusOK)
	if bought.MaxMarketPercent != nil {
		t.Fatalf("bought copy kept market percent %d", *bought.MaxMarketPercent)
	}
}

func TestWishlistExactEdition(t *testing.T) {
	a := newAPI(t)
	movie := a.title("movie", "Blade Runner 2049")
	edition := a.edition(obj{"title_id": movie, "format": "bluray", "packaging": []string{"steelbook"}})

	var c collection.Copy
	a.mustDo(http.MethodPost, "/api/copies", obj{"edition_id": edition, "status": "wishlist"}, &c, http.StatusCreated)
	if c.ExactEdition {
		t.Fatalf("a new wish matches only its edition: %+v", c)
	}
	path := fmt.Sprintf("/api/copies/%d", c.ID)
	a.mustDo(http.MethodPut, path, obj{"edition_id": edition, "status": "wishlist", "exact_edition": true}, &c, http.StatusOK)
	if !c.ExactEdition {
		t.Fatalf("updated copy = %+v", c)
	}
	var kept collection.Copy
	a.mustDo(http.MethodGet, path, nil, &kept, http.StatusOK)
	if !kept.ExactEdition {
		t.Fatalf("stored copy = %+v", kept)
	}
	a.fails(http.MethodPut, path, obj{"edition_id": edition, "status": "owned", "exact_edition": true}, http.StatusUnprocessableEntity, "exact_edition")
	a.mustDo(http.MethodGet, path, nil, &kept, http.StatusOK)
	if !kept.ExactEdition || kept.Status != collection.StatusWishlist {
		t.Fatalf("a refused update changed the copy to %+v", kept)
	}
	var bought collection.Copy
	a.mustDo(http.MethodPut, path, obj{"edition_id": edition, "status": "owned"}, &bought, http.StatusOK)
	if bought.ExactEdition {
		t.Fatalf("bought copy kept matching only its edition: %+v", bought)
	}

	var created collection.Copy
	a.mustDo(http.MethodPost, "/api/copies", obj{"edition_id": edition, "status": "wishlist", "exact_edition": true}, &created, http.StatusCreated)
	if !created.ExactEdition {
		t.Fatalf("created copy = %+v", created)
	}
	a.fails(http.MethodPost, "/api/copies", obj{"edition_id": edition, "status": "owned", "exact_edition": true}, http.StatusUnprocessableEntity, "exact_edition")
}

func TestEditionKindRules(t *testing.T) {
	a := newAPI(t)
	movie := a.title("movie", "Miś")
	book := a.title("book", "Lalka")

	a.fails(http.MethodPost, "/api/editions", obj{"title_id": movie}, http.StatusUnprocessableEntity, "format")
	a.fails(http.MethodPost, "/api/editions", obj{"title_id": book, "format": "dvd"}, http.StatusUnprocessableEntity, "format")
	a.fails(http.MethodPost, "/api/editions", obj{"title_id": 999999, "format": "dvd"}, http.StatusUnprocessableEntity, "title_id")

	edition := a.edition(obj{"title_id": movie, "format": "dvd"})
	a.fails(http.MethodPut, fmt.Sprintf("/api/editions/%d", edition), obj{"title_id": book, "format": "dvd"}, http.StatusUnprocessableEntity, "format")
	a.copyOf(obj{"edition_id": edition, "status": "owned"})
	a.fails(http.MethodPut, fmt.Sprintf("/api/editions/%d", edition), obj{"title_id": book, "binding": "paperback"}, http.StatusUnprocessableEntity, "title_id")
	var view collection.CollectionView
	a.mustDo(http.MethodGet, "/api/collection?kind=book", nil, &view, http.StatusOK)
	if len(view.Items) != 0 {
		t.Fatalf("copy moved into books: %+v", view.Items)
	}
	a.fails(http.MethodPut, fmt.Sprintf("/api/titles/%d", movie), obj{"kind": "book", "name": "Miś"}, http.StatusConflict, "kind")
	otherMovie := a.title("movie", "Miś 2")
	var moved collection.Edition
	a.mustDo(http.MethodPut, fmt.Sprintf("/api/editions/%d", edition), obj{"title_id": otherMovie, "format": "dvd"}, &moved, http.StatusOK)
	if moved.TitleID != otherMovie {
		t.Fatalf("same-kind move = %+v", moved)
	}

	empty := a.title("movie", "Wrong kind")
	var changed collection.Title
	a.mustDo(http.MethodPut, fmt.Sprintf("/api/titles/%d", empty), obj{"kind": "book", "name": "Right kind", "authors": []string{"A"}}, &changed, http.StatusOK)
	if changed.Kind != "book" || changed.Authors[0] != "A" {
		t.Fatalf("changed title = %+v", changed)
	}
}

func TestMusicEdition(t *testing.T) {
	a := newAPI(t)
	album := a.title("music", "Kind of Blue")
	a.fails(http.MethodPost, "/api/editions", obj{"title_id": album}, http.StatusUnprocessableEntity, "format")
	a.fails(http.MethodPost, "/api/editions", obj{"title_id": album, "format": "dvd"}, http.StatusUnprocessableEntity, "format")
	var e collection.Edition
	a.mustDo(http.MethodPost, "/api/editions", obj{
		"title_id": album, "barcode": "5099902987620", "format": "cd", "discogs_release_id": 1873013,
	}, &e, http.StatusCreated)
	if e.DiscogsReleaseID == nil || *e.DiscogsReleaseID != 1873013 || *e.Format != "cd" {
		t.Fatalf("album edition = %+v", e)
	}
	a.mustDo(http.MethodPut, fmt.Sprintf("/api/editions/%d", e.ID), obj{"title_id": album, "format": "vinyl"}, &e, http.StatusOK)
	if e.DiscogsReleaseID != nil || *e.Format != "vinyl" {
		t.Fatalf("updated album edition = %+v", e)
	}
	a.copyOf(obj{"edition_id": e.ID, "status": "owned"})
	var view collection.CollectionView
	a.mustDo(http.MethodGet, "/api/collection?kind=music&format=vinyl", nil, &view, http.StatusOK)
	if len(view.Items) != 1 || view.Items[0].Kind != "music" {
		t.Fatalf("music view = %+v", view.Items)
	}
}

func TestMusicCopyGrades(t *testing.T) {
	a := newAPI(t)
	album := a.title("music", "Blue Train")
	vinyl := a.edition(obj{"title_id": album, "format": "vinyl"})
	film := a.edition(obj{"title_id": a.title("movie", "Alien"), "format": "dvd"})
	book := a.edition(obj{"title_id": a.title("book", "Lalka")})

	for _, tt := range []struct {
		name  string
		body  obj
		field string
	}{
		{"film media grade", obj{"edition_id": film, "status": "owned", "media_grade": "mint"}, "media_grade"},
		{"film sleeve grade", obj{"edition_id": film, "status": "owned", "sleeve_grade": "no_cover"}, "sleeve_grade"},
		{"book media grade", obj{"edition_id": book, "status": "wishlist", "media_grade": "good"}, "media_grade"},
		{"unknown media grade", obj{"edition_id": vinyl, "status": "owned", "media_grade": "VG+"}, "media_grade"},
		{"sleeve-only grade on media", obj{"edition_id": vinyl, "status": "owned", "media_grade": "generic"}, "media_grade"},
		{"unknown sleeve grade", obj{"edition_id": vinyl, "status": "owned", "sleeve_grade": "excellent"}, "sleeve_grade"},
		{"missing edition", obj{"edition_id": 999999, "status": "owned", "media_grade": "mint"}, "edition_id"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sub := &api{t: t, url: a.url}
			sub.fails(http.MethodPost, "/api/copies", tt.body, http.StatusUnprocessableEntity, tt.field)
		})
	}

	// A film copy without grades saves as before.
	a.copyOf(obj{"edition_id": film, "status": "owned", "media_grade": "", "sleeve_grade": nil})

	// An album copy keeps its condition when a later save adds grades.
	var c collection.Copy
	a.mustDo(http.MethodPost, "/api/copies", obj{"edition_id": vinyl, "status": "owned", "condition": "good"},
		&c, http.StatusCreated)
	if c.MediaGrade != nil || c.SleeveGrade != nil {
		t.Fatalf("new copy grades = %v, %v", c.MediaGrade, c.SleeveGrade)
	}
	a.mustDo(http.MethodPut, fmt.Sprintf("/api/copies/%d", c.ID), obj{
		"edition_id": vinyl, "status": "owned", "condition": "good",
		"media_grade": " very_good_plus ", "sleeve_grade": "good_plus",
	}, &c, http.StatusOK)
	if c.Condition == nil || *c.Condition != "good" {
		t.Fatalf("condition after grading = %v", c.Condition)
	}
	wantGrades := func(where string, media, sleeve *string) {
		t.Helper()
		if media == nil || *media != "very_good_plus" || sleeve == nil || *sleeve != "good_plus" {
			t.Fatalf("%s grades = %v, %v", where, media, sleeve)
		}
	}
	wantGrades("PUT answer", c.MediaGrade, c.SleeveGrade)

	var detail collection.CopyDetail
	a.mustDo(http.MethodGet, fmt.Sprintf("/api/copies/%d", c.ID), nil, &detail, http.StatusOK)
	wantGrades("GET /api/copies/{id}", detail.MediaGrade, detail.SleeveGrade)
	if detail.Condition == nil || *detail.Condition != "good" {
		t.Fatalf("stored condition = %v", detail.Condition)
	}

	var title collection.TitleDetail
	a.mustDo(http.MethodGet, fmt.Sprintf("/api/titles/%d", album), nil, &title, http.StatusOK)
	if len(title.Editions) != 1 || len(title.Editions[0].Copies) != 1 {
		t.Fatalf("title editions = %+v", title.Editions)
	}
	got := title.Editions[0].Copies[0]
	wantGrades("GET /api/titles/{id}", got.MediaGrade, got.SleeveGrade)

	var view collection.CollectionView
	a.mustDo(http.MethodGet, "/api/collection?kind=music", nil, &view, http.StatusOK)
	if len(view.Items) != 1 {
		t.Fatalf("music view = %+v", view.Items)
	}
	wantGrades("GET /api/collection", view.Items[0].MediaGrade, view.Items[0].SleeveGrade)

	// Every sleeve-only grade fits the sleeve; a PUT without grades clears them.
	for _, grade := range []string{"generic", "not_graded", "no_cover"} {
		var regraded collection.Copy
		a.mustDo(http.MethodPut, fmt.Sprintf("/api/copies/%d", c.ID), obj{
			"edition_id": vinyl, "status": "owned", "media_grade": "near_mint", "sleeve_grade": grade,
		}, &regraded, http.StatusOK)
		if regraded.SleeveGrade == nil || *regraded.SleeveGrade != grade {
			t.Fatalf("sleeve grade = %v, want %s", regraded.SleeveGrade, grade)
		}
	}
	var cleared collection.Copy
	a.mustDo(http.MethodPut, fmt.Sprintf("/api/copies/%d", c.ID), obj{"edition_id": vinyl, "status": "owned"},
		&cleared, http.StatusOK)
	if cleared.MediaGrade != nil || cleared.SleeveGrade != nil {
		t.Fatalf("cleared grades = %v, %v", cleared.MediaGrade, cleared.SleeveGrade)
	}

	// A PUT that leaves condition out keeps the stored one; a null clears it.
	a.mustDo(http.MethodPut, fmt.Sprintf("/api/copies/%d", c.ID), obj{"edition_id": vinyl, "status": "owned", "condition": "fair"},
		nil, http.StatusOK)
	var graded collection.Copy
	a.mustDo(http.MethodPut, fmt.Sprintf("/api/copies/%d", c.ID), obj{
		"edition_id": vinyl, "status": "owned", "media_grade": "good", "sleeve_grade": "fair",
	}, &graded, http.StatusOK)
	if graded.Condition == nil || *graded.Condition != "fair" || graded.MediaGrade == nil || *graded.MediaGrade != "good" {
		t.Fatalf("grade-only PUT = condition %v, media grade %v", graded.Condition, graded.MediaGrade)
	}
	var ungraded collection.Copy
	a.mustDo(http.MethodPut, fmt.Sprintf("/api/copies/%d", c.ID), obj{"edition_id": vinyl, "status": "owned", "condition": nil},
		&ungraded, http.StatusOK)
	if ungraded.Condition != nil {
		t.Fatalf("condition after a null = %v", ungraded.Condition)
	}

	// A wrongly typed grade or condition names its field.
	for _, field := range []string{"media_grade", "sleeve_grade", "condition"} {
		a.fails(http.MethodPost, "/api/copies", obj{"edition_id": vinyl, "status": "owned", field: []string{"mint"}},
			http.StatusUnprocessableEntity, field)
		a.fails(http.MethodPut, fmt.Sprintf("/api/copies/%d", c.ID), obj{"edition_id": vinyl, "status": "owned", field: 1},
			http.StatusUnprocessableEntity, field)
	}

	// Moving a graded copy to a film edition is refused.
	a.fails(http.MethodPut, fmt.Sprintf("/api/copies/%d", c.ID), obj{
		"edition_id": film, "status": "owned", "media_grade": "mint",
	}, http.StatusUnprocessableEntity, "media_grade")
}

func TestGradedCopyReplayOutlivesItsEdition(t *testing.T) {
	a := newAPI(t)
	album := a.title("music", "Giant Steps")
	first := a.edition(obj{"title_id": album, "format": "cd"})
	second := a.edition(obj{"title_id": album, "format": "vinyl"})
	add := obj{"edition_id": first, "status": "owned", "media_grade": "mint", "request_key": "grades-replay"}
	var c collection.Copy
	a.mustDo(http.MethodPost, "/api/copies", add, &c, http.StatusCreated)
	a.mustDo(http.MethodPut, fmt.Sprintf("/api/copies/%d", c.ID), obj{"edition_id": second, "status": "owned", "media_grade": "mint"},
		nil, http.StatusOK)
	a.mustDo(http.MethodDelete, fmt.Sprintf("/api/editions/%d", first), nil, nil, http.StatusNoContent)

	var replay collection.Copy
	a.mustDo(http.MethodPost, "/api/copies", add, &replay, http.StatusOK)
	if replay.ID != c.ID || replay.EditionID != second {
		t.Fatalf("replay = copy %d on edition %d, want copy %d on edition %d", replay.ID, replay.EditionID, c.ID, second)
	}
}

func TestDuplicateTMDBAndBadBodies(t *testing.T) {
	a := newAPI(t)
	a.mustDo(http.MethodPost, "/api/titles", obj{"kind": "movie", "name": "A", "tmdb_id": 5}, nil, http.StatusCreated)
	a.fails(http.MethodPost, "/api/titles", obj{"kind": "movie", "name": "B", "tmdb_id": 5}, http.StatusConflict, "tmdb_id")
	a.fails(http.MethodPost, "/api/titles", "not an object", http.StatusUnprocessableEntity, "body")
	a.fails(http.MethodPost, "/api/titles", obj{"kind": "movie", "name": "x", "year": "1984"}, http.StatusUnprocessableEntity, "body")
	a.fails(http.MethodPost, "/api/copies", obj{"edition_id": 1, "status": "owned", "purchased_on": "01.11.2026"}, http.StatusUnprocessableEntity, "body")
	a.fails(http.MethodPost, "/api/titles", obj{"kind": "movie", "name": strings.Repeat("x", 70000)}, http.StatusUnprocessableEntity, "body")
}

// ratingOf reads a title's rating from GET /api/titles/{id}.
func (a *api) ratingOf(id int64) *int {
	a.t.Helper()
	var got collection.TitleDetail
	a.mustDo(http.MethodGet, fmt.Sprintf("/api/titles/%d", id), nil, &got, http.StatusOK)
	return got.Rating
}

// wantRating asserts a title's stored rating; 0 means unrated.
func (a *api) wantRating(id int64, want int) {
	a.t.Helper()
	got := 0
	if r := a.ratingOf(id); r != nil {
		got = *r
	}
	if got != want {
		a.t.Fatalf("title %d rating = %d, want %d", id, got, want)
	}
}

func TestTitleRatingIsSetKeptAndCleared(t *testing.T) {
	a := newAPI(t)
	var created collection.Title
	a.mustDo(http.MethodPost, "/api/titles", obj{"kind": "book", "name": "Lalka", "rating": 8}, &created, http.StatusCreated)
	if created.Rating == nil || *created.Rating != 8 {
		t.Fatalf("created rating = %v", created.Rating)
	}
	a.wantRating(created.ID, 8)
	a.wantRating(a.title("movie", "Rejs"), 0)

	path := fmt.Sprintf("/api/titles/%d", created.ID)
	a.mustDo(http.MethodPut, path, obj{"kind": "book", "name": "Lalka", "notes": "Prus"}, nil, http.StatusOK)
	a.wantRating(created.ID, 8)

	for _, edge := range []int{1, 10} {
		a.mustDo(http.MethodPut, path, obj{"kind": "book", "name": "Lalka", "rating": edge}, nil, http.StatusOK)
		a.wantRating(created.ID, edge)
	}
	a.mustDo(http.MethodPut, path, obj{"kind": "book", "name": "Lalka", "rating": nil}, nil, http.StatusOK)
	a.wantRating(created.ID, 0)
}

func TestTitleRatingRefusesValuesOffTheScale(t *testing.T) {
	a := newAPI(t)
	id := a.title("book", "Lalka")
	path := fmt.Sprintf("/api/titles/%d", id)
	a.mustDo(http.MethodPut, path, obj{"kind": "book", "name": "Lalka", "rating": 7}, nil, http.StatusOK)

	for _, bad := range []any{0, 11, -1, 7.5, "8", true, []int{8}, obj{}, 1e10} {
		a.fails(http.MethodPost, "/api/titles", obj{"kind": "movie", "name": "Miś", "rating": bad}, http.StatusUnprocessableEntity, "rating")
		a.fails(http.MethodPut, path, obj{"kind": "book", "name": "Lalka", "rating": bad}, http.StatusUnprocessableEntity, "rating")
	}
	a.fails(http.MethodPut, path, []byte(`{"kind": "book", "name": "Lalka", "rating": 8.0}`), http.StatusUnprocessableEntity, "rating")
	a.wantRating(id, 7)

	var titles []collection.Title
	a.mustDo(http.MethodGet, "/api/titles", nil, &titles, http.StatusOK)
	if len(titles) != 1 {
		t.Fatalf("a refused POST stored a title: %+v", titles)
	}
}

func TestRateTitleChangesOnlyTheRating(t *testing.T) {
	a := newAPI(t)
	cover := "https://example.com/lalka.jpg"
	var created collection.Title
	a.mustDo(http.MethodPost, "/api/titles", obj{"kind": "book", "name": "Lalka", "notes": "Original", "cover_url": cover, "rating": 4}, &created, http.StatusCreated)
	path := fmt.Sprintf("/api/titles/%d", created.ID)
	a.mustDo(http.MethodPut, path, obj{"kind": "book", "name": "Lalka", "notes": "Corrected"}, nil, http.StatusOK)

	var rated collection.Title
	a.mustDo(http.MethodPut, path+"/rating", obj{"rating": 8}, &rated, http.StatusOK)
	if rated.Rating == nil || *rated.Rating != 8 || rated.Notes != "Corrected" {
		t.Fatalf("rated title = %+v", rated)
	}
	var detail collection.TitleDetail
	a.mustDo(http.MethodGet, path, nil, &detail, http.StatusOK)
	if detail.Notes != "Corrected" || detail.Name != "Lalka" || detail.CoverURL == nil || *detail.CoverURL != cover {
		t.Fatalf("title after rating = %+v", detail.Title)
	}
	a.wantRating(created.ID, 8)

	var cleared collection.Title
	a.mustDo(http.MethodPut, path+"/rating", obj{"rating": nil}, &cleared, http.StatusOK)
	if cleared.Rating != nil || cleared.Notes != "Corrected" {
		t.Fatalf("cleared title = %+v", cleared)
	}
	a.wantRating(created.ID, 0)
	a.mustDo(http.MethodPut, path+"/rating", obj{"rating": 10}, nil, http.StatusOK)

	for _, bad := range []any{0, 11, 7.5, "8", true, obj{}} {
		a.fails(http.MethodPut, path+"/rating", obj{"rating": bad}, http.StatusUnprocessableEntity, "rating")
	}
	a.fails(http.MethodPut, path+"/rating", obj{}, http.StatusUnprocessableEntity, "rating")
	a.fails(http.MethodPut, path+"/rating", []byte(`{"rating": 8.0}`), http.StatusUnprocessableEntity, "rating")
	a.fails(http.MethodPut, path+"/rating", "not an object", http.StatusUnprocessableEntity, "body")
	a.wantRating(created.ID, 10)

	a.fails(http.MethodPut, fmt.Sprintf("/api/titles/%d/rating", created.ID+1000), obj{"rating": 5}, http.StatusNotFound, "")
}

func TestTitleRatingAppliesToAWishedTitle(t *testing.T) {
	a := newAPI(t)
	wished := a.title("movie", "Miś")
	a.copyOf(obj{"edition_id": a.edition(obj{"title_id": wished, "format": "dvd"}), "status": "wishlist"})
	a.mustDo(http.MethodPut, fmt.Sprintf("/api/titles/%d", wished), obj{"kind": "movie", "name": "Miś", "rating": 10}, nil, http.StatusOK)
	a.wantRating(wished, 10)

	var titles []collection.Title
	a.mustDo(http.MethodGet, "/api/titles", nil, &titles, http.StatusOK)
	if len(titles) != 1 || titles[0].Rating == nil || *titles[0].Rating != 10 {
		t.Fatalf("listed titles = %+v", titles)
	}
}

func TestCollectionViewCarriesTitleRating(t *testing.T) {
	a := newAPI(t)
	rated := a.title("movie", "Seksmisja")
	a.mustDo(http.MethodPut, fmt.Sprintf("/api/titles/%d", rated), obj{"kind": "movie", "name": "Seksmisja", "rating": 9}, nil, http.StatusOK)
	a.copyOf(obj{"edition_id": a.edition(obj{"title_id": rated, "format": "dvd"}), "status": "owned"})
	a.copyOf(obj{"edition_id": a.edition(obj{"title_id": rated, "format": "bluray"}), "status": "wishlist"})
	a.copyOf(obj{"edition_id": a.edition(obj{"title_id": a.title("movie", "Rejs"), "format": "dvd"}), "status": "owned"})

	var raw struct {
		Items []map[string]any `json:"items"`
	}
	a.mustDo(http.MethodGet, "/api/collection", nil, &raw, http.StatusOK)
	if len(raw.Items) != 3 {
		t.Fatalf("items = %+v", raw.Items)
	}
	for _, it := range raw.Items {
		got, has := it["rating"]
		if it["name"] == "Seksmisja" && got != float64(9) {
			t.Fatalf("rated copy %v rating = %v", it["copy_id"], got)
		}
		if it["name"] == "Rejs" && has {
			t.Fatalf("unrated copy carries rating %v", got)
		}
	}
}

func TestCollectionViewFiltersAndTotals(t *testing.T) {
	a := newAPI(t)
	movie := a.title("movie", "Seksmisja")
	bluray := a.edition(obj{"title_id": movie, "barcode": "5901234123457", "format": "bluray"})
	dvd := a.edition(obj{"title_id": movie, "format": "dvd"})
	book := a.title("book", "Solaris")
	a.mustDo(http.MethodPut, fmt.Sprintf("/api/titles/%d", book), obj{"kind": "book", "name": "Solaris", "authors": []string{"Stanisław Lem"}}, nil, http.StatusOK)
	paper := a.edition(obj{"title_id": book, "barcode": "9788373012349", "binding": "paperback"})

	a.copyOf(obj{"edition_id": bluray, "status": "owned", "price_cents": 4999})
	a.copyOf(obj{"edition_id": dvd, "status": "owned"})
	a.copyOf(obj{"edition_id": dvd, "status": "wishlist"})
	bookCopy := a.copyOf(obj{"edition_id": paper, "status": "owned", "price_cents": 2550})

	tests := []struct {
		query string
		want  collection.Totals
	}{
		{"", collection.Totals{Copies: 4, Owned: 3, Wishlist: 1, Priced: 2, SpentCents: 7549}},
		{"?kind=movie", collection.Totals{Copies: 3, Owned: 2, Wishlist: 1, Priced: 1, SpentCents: 4999}},
		{"?kind=book", collection.Totals{Copies: 1, Owned: 1, Priced: 1, SpentCents: 2550}},
		{"?format=dvd", collection.Totals{Copies: 2, Owned: 1, Wishlist: 1}},
		{"?status=wishlist", collection.Totals{Copies: 1, Wishlist: 1}},
		{"?status=owned&kind=movie&format=bluray", collection.Totals{Copies: 1, Owned: 1, Priced: 1, SpentCents: 4999}},
		{"?q=lem", collection.Totals{Copies: 1, Owned: 1, Priced: 1, SpentCents: 2550}},
		{"?q=590-1234-12345-7", collection.Totals{Copies: 1, Owned: 1, Priced: 1, SpentCents: 4999}},
		{"?q=nothing", collection.Totals{}},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			var view collection.CollectionView
			a.mustDo(http.MethodGet, "/api/collection"+tt.query, nil, &view, http.StatusOK)
			if view.Totals != tt.want || len(view.Items) != tt.want.Copies {
				t.Fatalf("totals = %+v (%d items), want %+v", view.Totals, len(view.Items), tt.want)
			}
		})
	}

	var view collection.CollectionView
	a.mustDo(http.MethodGet, "/api/collection?kind=book", nil, &view, http.StatusOK)
	if it := view.Items[0]; it.Name != "Solaris" || it.Kind != "book" || *it.Binding != "paperback" || it.TitleID != book {
		t.Fatalf("item = %+v", it)
	}
	var copyDetail collection.CopyDetail
	a.mustDo(http.MethodGet, fmt.Sprintf("/api/copies/%d", bookCopy), nil, &copyDetail, http.StatusOK)
	if got, want := view.Items[0].CreatedAtUS, copyDetail.CreatedAt.UnixMicro(); got != want {
		t.Fatalf("created_at_us = %d, want %d", got, want)
	}

	for _, q := range []string{"?kind=vinyl", "?format=vhs", "?status=lent"} {
		field, _, _ := strings.Cut(strings.TrimPrefix(q, "?"), "=")
		a.fails(http.MethodGet, "/api/collection"+q, nil, http.StatusUnprocessableEntity, field)
	}
}

func TestCollectionViewLoans(t *testing.T) {
	a := newAPI(t)
	dvd := a.edition(obj{"title_id": a.title("movie", "Seksmisja"), "format": "dvd"})
	paper := a.edition(obj{"title_id": a.title("book", "Solaris")})
	lentDue := a.copyOf(obj{"edition_id": dvd, "status": "owned", "price_cents": 2000})
	lentOpen := a.copyOf(obj{"edition_id": paper, "status": "owned"})
	back := a.copyOf(obj{"edition_id": paper, "status": "owned", "price_cents": 1500})
	a.copyOf(obj{"edition_id": dvd, "status": "owned"})
	a.copyOf(obj{"edition_id": dvd, "status": "wishlist"})

	lend := func(copyID int64, body obj) collection.Loan {
		t.Helper()
		var l collection.Loan
		a.mustDo(http.MethodPost, fmt.Sprintf("/api/copies/%d/loans", copyID), body, &l, http.StatusCreated)
		return l
	}
	lend(lentDue, obj{"borrower": "Ania", "lent_on": "2026-05-01", "due_on": "2026-06-01"})
	lend(lentOpen, obj{"borrower": "Bartek"})
	returned := lend(back, obj{"borrower": "Celina", "lent_on": "2026-04-01"})
	a.mustDo(http.MethodPut, fmt.Sprintf("/api/loans/%d", returned.ID), obj{"returned_on": "2026-04-20"}, nil, http.StatusOK)

	var view collection.CollectionView
	a.mustDo(http.MethodGet, "/api/collection", nil, &view, http.StatusOK)
	want := map[int64][2]string{lentDue: {"Ania", "2026-06-01"}, lentOpen: {"Bartek", ""}}
	for _, it := range view.Items {
		borrower := ""
		if it.LoanBorrower != nil {
			borrower = *it.LoanBorrower
		}
		if got := [2]string{borrower, day(it.LoanDueOn)}; got != want[it.CopyID] {
			t.Errorf("copy %d loan = %q, want %q", it.CopyID, got, want[it.CopyID])
		}
	}
	if want := (collection.Totals{Copies: 5, Owned: 4, Wishlist: 1, Priced: 2, SpentCents: 3500}); view.Totals != want {
		t.Fatalf("totals = %+v, want %+v: a lent copy stays owned", view.Totals, want)
	}

	tests := []struct {
		query string
		want  []int64
	}{
		{"?loaned=1", []int64{lentDue, lentOpen}},
		{"?loaned=1&kind=book", []int64{lentOpen}},
		{"?loaned=1&status=wishlist", nil},
		{"?loaned=1&q=ania", nil},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			var view collection.CollectionView
			a.mustDo(http.MethodGet, "/api/collection"+tt.query, nil, &view, http.StatusOK)
			got := make([]int64, 0, len(view.Items))
			for _, it := range view.Items {
				got = append(got, it.CopyID)
			}
			if fmt.Sprint(got) != fmt.Sprint(append([]int64{}, tt.want...)) {
				t.Fatalf("copies = %v, want %v", got, tt.want)
			}
		})
	}
	for _, q := range []string{"?loaned=0", "?loaned=true"} {
		a.fails(http.MethodGet, "/api/collection"+q, nil, http.StatusUnprocessableEntity, "loaned")
	}
}

func TestSoldCopy(t *testing.T) {
	a := newAPI(t)
	movie := a.title("movie", "Rejs")
	edition := a.edition(obj{"title_id": movie, "format": "dvd"})
	owned := obj{
		"edition_id": edition, "status": "owned", "price_cents": 4999, "purchased_on": "2024-03-01",
		"shop": "Empik", "acquired_as": "used", "condition": "good",
	}
	id := a.copyOf(owned)
	path := fmt.Sprintf("/api/copies/%d", id)
	with := func(base obj, fields obj) obj {
		out := maps.Clone(base)
		maps.Copy(out, fields)
		return out
	}
	sold := with(owned, obj{"status": "sold", "sold_on": "2026-05-10", "sale_price_cents": 3500})

	a.mustDo(http.MethodPut, path, sold, nil, http.StatusOK)
	var got collection.CopyDetail
	a.mustDo(http.MethodGet, path, nil, &got, http.StatusOK)
	c := got.Copy
	if c.Status != "sold" || c.SoldOn == nil || c.SoldOn.Format("2006-01-02") != "2026-05-10" ||
		c.SalePriceCents == nil || *c.SalePriceCents != 3500 {
		t.Fatalf("sold copy = %+v", c)
	}
	if c.PriceCents == nil || *c.PriceCents != 4999 || c.PurchasedOn == nil ||
		c.PurchasedOn.Format("2006-01-02") != "2024-03-01" || c.Shop != "Empik" ||
		c.AcquiredAs == nil || *c.AcquiredAs != "used" || c.Condition == nil || *c.Condition != "good" {
		t.Fatalf("sold copy lost its purchase: %+v", c)
	}

	refused := []struct {
		name  string
		body  obj
		field string
	}{
		{"sold without a day", with(owned, obj{"status": "sold", "sale_price_cents": 3500}), "sold_on"},
		{"owned with a sale day", with(owned, obj{"sold_on": "2026-05-10"}), "sold_on"},
		{"owned with a sale price", with(owned, obj{"sale_price_cents": 3500}), "sale_price_cents"},
		{"wish with a sale day", obj{"edition_id": edition, "status": "wishlist", "sold_on": "2026-05-10"}, "sold_on"},
		{"wish with a sale price", obj{"edition_id": edition, "status": "wishlist", "sale_price_cents": 3500}, "sale_price_cents"},
		{"negative sale price", with(sold, obj{"sale_price_cents": -1}), "sale_price_cents"},
		{"sale price over the cap", with(sold, obj{"sale_price_cents": collection.MaxPriceCents + 1}), "sale_price_cents"},
		{"sale price past int64", with(sold, obj{"sale_price_cents": json.Number("9223372036854775808")}), "sale_price_cents"},
		{"sale price not a number", with(sold, obj{"sale_price_cents": "35"}), "sale_price_cents"},
		{"sold with a wish limit", with(sold, obj{"max_price_cents": 3000}), "max_price_cents"},
		{"sold with a market percent", with(sold, obj{"max_market_percent": 70}), "max_market_percent"},
	}
	for _, tt := range refused {
		t.Run(tt.name, func(t *testing.T) {
			a.fails(http.MethodPut, path, tt.body, http.StatusUnprocessableEntity, tt.field)
		})
	}
	var kept collection.Copy
	a.mustDo(http.MethodGet, path, nil, &kept, http.StatusOK)
	if kept.Status != "sold" || *kept.SalePriceCents != 3500 {
		t.Fatalf("refused updates changed the copy to %+v", kept)
	}

	var unpriced collection.Copy
	a.mustDo(http.MethodPut, path, with(sold, obj{"sale_price_cents": nil}), &unpriced, http.StatusOK)
	if unpriced.Status != "sold" || unpriced.SoldOn == nil || unpriced.SalePriceCents != nil {
		t.Fatalf("sold copy without a sale price = %+v", unpriced)
	}

	var back collection.Copy
	a.mustDo(http.MethodPut, path, sold, nil, http.StatusOK)
	a.mustDo(http.MethodPut, path, owned, &back, http.StatusOK)
	if back.Status != "owned" || back.SoldOn != nil || back.SalePriceCents != nil || *back.PriceCents != 4999 {
		t.Fatalf("copy back on the shelf = %+v", back)
	}
	var raw map[string]any
	a.mustDo(http.MethodGet, path, nil, &raw, http.StatusOK)
	if _, ok := raw["sold_on"]; ok {
		t.Fatalf("owned copy answers a sale day: %v", raw)
	}

	var created collection.Copy
	a.mustDo(http.MethodPost, "/api/copies", obj{"edition_id": edition, "status": "sold", "sold_on": "2026-01-02"}, &created, http.StatusCreated)
	if created.Status != "sold" || created.SoldOn == nil || created.PriceCents != nil {
		t.Fatalf("created sold copy = %+v", created)
	}
	a.fails(http.MethodPost, "/api/copies", obj{"edition_id": edition, "status": "sold"}, http.StatusUnprocessableEntity, "sold_on")
}

// TestConcurrentCopyUpdatesAnswerTheirOwnRow overlaps updates that sell a
// copy and put it back: each answer shows the copy as its own update left it.
func TestConcurrentCopyUpdatesAnswerTheirOwnRow(t *testing.T) {
	a := newAPI(t)
	movie := a.title("movie", "Rejs")
	edition := a.edition(obj{"title_id": movie, "format": "dvd"})
	id := a.copyOf(obj{"edition_id": edition, "status": "owned"})
	path := fmt.Sprintf("%s/api/copies/%d", a.url, id)

	put := func(body obj) (collection.Copy, error) {
		raw, err := json.Marshal(body)
		if err != nil {
			return collection.Copy{}, err
		}
		req, err := http.NewRequestWithContext(context.Background(), http.MethodPut, path, bytes.NewReader(raw))
		if err != nil {
			return collection.Copy{}, err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return collection.Copy{}, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return collection.Copy{}, fmt.Errorf("status %d", resp.StatusCode)
		}
		var c collection.Copy
		return c, json.NewDecoder(resp.Body).Decode(&c)
	}

	const rounds = 100
	errs := make(chan error, 2*rounds)
	// Sixty-four in flight overlap the updates while staying within the test
	// server's listen backlog.
	inFlight := make(chan struct{}, 64)
	var wg sync.WaitGroup
	for i := range rounds {
		day := fmt.Sprintf("2026-05-%02d", i%28+1)
		bodies := []obj{
			{"edition_id": edition, "status": "sold", "sold_on": day, "sale_price_cents": 3500 + i},
			{"edition_id": edition, "status": "owned"},
		}
		for _, body := range bodies {
			wg.Go(func() {
				inFlight <- struct{}{}
				got, err := put(body)
				<-inFlight
				switch {
				case err != nil:
					errs <- err
				case got.Status != body["status"]:
					errs <- fmt.Errorf("sent %v, answer %+v", body, got)
				case got.Status == "sold" && (got.SoldOn == nil || got.SoldOn.Format("2006-01-02") != day):
					errs <- fmt.Errorf("sent %v, answer %+v", body, got)
				}
			})
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestCollectionViewKeepsSoldCopiesApart(t *testing.T) {
	a := newAPI(t)
	movie := a.title("movie", "Seksmisja")
	dvd := a.edition(obj{"title_id": movie, "format": "dvd"})
	a.copyOf(obj{"edition_id": dvd, "status": "owned", "price_cents": 4999})
	a.copyOf(obj{"edition_id": dvd, "status": "wishlist"})
	sold := a.copyOf(obj{
		"edition_id": dvd, "status": "sold", "price_cents": 2000, "sold_on": "2026-05-10",
		"sale_price_cents": 3500,
	})

	tests := []struct {
		query string
		want  collection.Totals
		ids   int
	}{
		{"", collection.Totals{Copies: 3, Owned: 1, Wishlist: 1, Priced: 1, SpentCents: 4999}, 3},
		{"?status=sold", collection.Totals{Copies: 1}, 1},
		{"?status=owned", collection.Totals{Copies: 1, Owned: 1, Priced: 1, SpentCents: 4999}, 1},
		{"?status=wishlist", collection.Totals{Copies: 1, Wishlist: 1}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			var view collection.CollectionView
			a.mustDo(http.MethodGet, "/api/collection"+tt.query, nil, &view, http.StatusOK)
			if view.Totals != tt.want || len(view.Items) != tt.ids {
				t.Fatalf("totals = %+v (%d items), want %+v", view.Totals, len(view.Items), tt.want)
			}
			for _, it := range view.Items {
				if (it.CopyID == sold) != (it.Status == "sold") {
					t.Fatalf("item %+v", it)
				}
				if tt.query == "?status=owned" && it.CopyID == sold {
					t.Fatalf("owned view lists the sold copy")
				}
			}
		})
	}

	var view collection.CollectionView
	a.mustDo(http.MethodGet, "/api/collection?status=sold", nil, &view, http.StatusOK)
	it := view.Items[0]
	if it.CopyID != sold || it.SoldOn == nil || it.SoldOn.Format("2006-01-02") != "2026-05-10" ||
		it.SalePriceCents == nil || *it.SalePriceCents != 3500 || *it.PriceCents != 2000 {
		t.Fatalf("sold item = %+v", it)
	}
}

func TestOrderedCopy(t *testing.T) {
	a := newAPI(t)
	movie := a.title("movie", "Diuna")
	edition := a.edition(obj{"title_id": movie, "format": "uhd"})
	with := func(base obj, fields obj) obj {
		out := maps.Clone(base)
		maps.Copy(out, fields)
		return out
	}
	ordered := obj{
		"edition_id": edition, "status": "ordered", "price_cents": 12999, "purchased_on": "2026-11-02",
		"shop": "Allegro", "acquired_as": "new", "expected_on": "2026-12-15",
	}
	var created collection.Copy
	a.mustDo(http.MethodPost, "/api/copies", ordered, &created, http.StatusCreated)
	if created.Status != "ordered" || created.ExpectedOn == nil || created.ExpectedOn.Format("2006-01-02") != "2026-12-15" ||
		created.PriceCents == nil || *created.PriceCents != 12999 || created.PurchasedOn == nil ||
		created.PurchasedOn.Format("2006-01-02") != "2026-11-02" || created.Shop != "Allegro" {
		t.Fatalf("created ordered copy = %+v", created)
	}
	path := fmt.Sprintf("/api/copies/%d", created.ID)

	var undated collection.Copy
	a.mustDo(http.MethodPut, path, with(ordered, obj{"expected_on": nil}), &undated, http.StatusOK)
	if undated.Status != "ordered" || undated.ExpectedOn != nil || *undated.PriceCents != 12999 {
		t.Fatalf("ordered copy without a delivery day = %+v", undated)
	}
	a.mustDo(http.MethodPut, path, ordered, nil, http.StatusOK)

	owned := obj{"edition_id": edition, "status": "owned", "price_cents": 12999}
	refused := []struct {
		name   string
		method string
		path   string
		body   obj
		field  string
	}{
		{"owned with a delivery day", http.MethodPut, path, with(owned, obj{"expected_on": "2026-12-15"}), "expected_on"},
		{"wish with a delivery day", http.MethodPut, path, obj{"edition_id": edition, "status": "wishlist", "expected_on": "2026-12-15"}, "expected_on"},
		{"sold with a delivery day", http.MethodPut, path, with(owned, obj{"status": "sold", "sold_on": "2026-12-20", "expected_on": "2026-12-15"}), "expected_on"},
		{"created owned with a delivery day", http.MethodPost, "/api/copies", with(owned, obj{"expected_on": "2026-12-15"}), "expected_on"},
		{"ordered with a wish limit", http.MethodPut, path, with(ordered, obj{"max_price_cents": 10000}), "max_price_cents"},
		{"created ordered with a wish limit", http.MethodPost, "/api/copies", with(ordered, obj{"max_price_cents": 10000}), "max_price_cents"},
		{"ordered with a market percent", http.MethodPut, path, with(ordered, obj{"max_market_percent": 70}), "max_market_percent"},
		{"ordered with a location", http.MethodPut, path, with(ordered, obj{"location": "Regał 2"}), "location"},
		{"ordered with a sale day", http.MethodPut, path, with(ordered, obj{"sold_on": "2026-12-20"}), "sold_on"},
		{"delivery day not a date", http.MethodPut, path, with(ordered, obj{"expected_on": "15.12.2026"}), "body"},
	}
	for _, tt := range refused {
		t.Run(tt.name, func(t *testing.T) {
			a.fails(tt.method, tt.path, tt.body, http.StatusUnprocessableEntity, tt.field)
		})
	}
	var kept collection.Copy
	a.mustDo(http.MethodGet, path, nil, &kept, http.StatusOK)
	if kept.Status != "ordered" || kept.ExpectedOn == nil || kept.ExpectedOn.Format("2006-01-02") != "2026-12-15" {
		t.Fatalf("refused updates changed the copy to %+v", kept)
	}

	// Receiving: the form sends the copy back as owned, without its delivery day.
	received := maps.Clone(ordered)
	delete(received, "expected_on")
	received["status"] = "owned"
	var got collection.Copy
	a.mustDo(http.MethodPut, path, received, &got, http.StatusOK)
	if got.Status != "owned" || got.ExpectedOn != nil || got.PriceCents == nil || *got.PriceCents != 12999 ||
		got.PurchasedOn == nil || got.PurchasedOn.Format("2006-01-02") != "2026-11-02" || got.Shop != "Allegro" ||
		got.AcquiredAs == nil || *got.AcquiredAs != "new" {
		t.Fatalf("received copy = %+v", got)
	}
	var raw map[string]any
	a.mustDo(http.MethodGet, path, nil, &raw, http.StatusOK)
	if _, ok := raw["expected_on"]; ok {
		t.Fatalf("owned copy answers a delivery day: %v", raw)
	}
}

// TestReceiveOrderedCopy receives a copy whose price and shop changed after
// the receiving page read it: the receipt keeps the stored values, so a stale
// page cannot put the old ones back.
func TestReceiveOrderedCopy(t *testing.T) {
	a := newAPI(t)
	movie := a.title("movie", "Diuna")
	edition := a.edition(obj{"title_id": movie, "format": "uhd"})
	ordered := obj{
		"edition_id": edition, "status": "ordered", "price_cents": 12999, "purchased_on": "2026-11-02",
		"shop": "Allegro", "condition": "mint", "expected_on": "2026-12-15",
	}
	id := a.copyOf(ordered)
	path := fmt.Sprintf("/api/copies/%d", id)
	edited := maps.Clone(ordered)
	edited["price_cents"] = 9999
	edited["shop"] = "Empik"
	a.mustDo(http.MethodPut, path, edited, nil, http.StatusOK)

	var got collection.Copy
	a.mustDo(http.MethodPost, path+"/receipt", nil, &got, http.StatusOK)
	if got.Status != "owned" || got.ExpectedOn != nil || got.PriceCents == nil || *got.PriceCents != 9999 ||
		got.Shop != "Empik" || got.PurchasedOn == nil || got.PurchasedOn.Format("2006-01-02") != "2026-11-02" ||
		got.Condition == nil || *got.Condition != "mint" {
		t.Fatalf("received copy = %+v", got)
	}
	var stored collection.Copy
	a.mustDo(http.MethodGet, path, nil, &stored, http.StatusOK)
	if stored.Status != "owned" || stored.ExpectedOn != nil || *stored.PriceCents != 9999 || stored.Shop != "Empik" {
		t.Fatalf("stored copy = %+v", stored)
	}

	a.fails(http.MethodPost, path+"/receipt", nil, http.StatusConflict, "status")
	wish := a.copyOf(obj{"edition_id": edition, "status": "wishlist"})
	a.fails(http.MethodPost, fmt.Sprintf("/api/copies/%d/receipt", wish), nil, http.StatusConflict, "status")
	a.fails(http.MethodPost, "/api/copies/999999/receipt", nil, http.StatusNotFound, "")
}

func TestCollectionViewCountsOrderedCopiesApart(t *testing.T) {
	a := newAPI(t)
	movie := a.title("movie", "Diuna")
	uhd := a.edition(obj{"title_id": movie, "format": "uhd"})
	a.copyOf(obj{"edition_id": uhd, "status": "owned", "price_cents": 4999})
	a.copyOf(obj{"edition_id": uhd, "status": "owned"})
	a.copyOf(obj{"edition_id": uhd, "status": "wishlist"})
	a.copyOf(obj{"edition_id": uhd, "status": "sold", "price_cents": 2000, "sold_on": "2026-05-10"})
	ordered := a.copyOf(obj{
		"edition_id": uhd, "status": "ordered", "price_cents": 12999, "purchased_on": "2026-11-02",
		"expected_on": "2026-12-15",
	})
	a.copyOf(obj{"edition_id": uhd, "status": "ordered"})

	tests := []struct {
		query string
		want  collection.Totals
	}{
		{"", collection.Totals{Copies: 6, Owned: 2, Ordered: 2, Wishlist: 1, Priced: 1, SpentCents: 17998}},
		{"?status=ordered", collection.Totals{Copies: 2, Ordered: 2, SpentCents: 12999}},
		{"?status=owned", collection.Totals{Copies: 2, Owned: 2, Priced: 1, SpentCents: 4999}},
		{"?status=wishlist", collection.Totals{Copies: 1, Wishlist: 1}},
		{"?status=sold", collection.Totals{Copies: 1}},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			var view collection.CollectionView
			a.mustDo(http.MethodGet, "/api/collection"+tt.query, nil, &view, http.StatusOK)
			if view.Totals != tt.want || len(view.Items) != tt.want.Copies {
				t.Fatalf("totals = %+v (%d items), want %+v", view.Totals, len(view.Items), tt.want)
			}
			for _, it := range view.Items {
				if tt.query == "?status=ordered" && it.Status != "ordered" {
					t.Fatalf("ordered view lists %+v", it)
				}
			}
		})
	}

	var view collection.CollectionView
	a.mustDo(http.MethodGet, "/api/collection?status=ordered", nil, &view, http.StatusOK)
	var found bool
	for _, it := range view.Items {
		if it.CopyID != ordered {
			continue
		}
		found = true
		if it.ExpectedOn == nil || it.ExpectedOn.Format("2006-01-02") != "2026-12-15" || *it.PriceCents != 12999 {
			t.Fatalf("ordered item = %+v", it)
		}
	}
	if !found {
		t.Fatalf("ordered view misses copy %d: %+v", ordered, view.Items)
	}
}

func TestCollectionViewReturnsArtworkAndCreators(t *testing.T) {
	a := newAPI(t)
	const titleCover = "https://example.test/title.jpg"
	const editionCover = "https://example.test/edition.jpg"
	var title collection.Title
	a.mustDo(http.MethodPost, "/api/titles", obj{
		"kind": "book", "name": "Solaris", "authors": []string{"Stanisław Lem"}, "cover_url": titleCover,
	}, &title, http.StatusCreated)

	withCover := a.edition(obj{"title_id": title.ID, "cover_url": editionCover})
	withoutCover := a.edition(obj{"title_id": title.ID})
	withoutArtwork := a.edition(obj{"title_id": a.title("book", "Lalka")})
	wantByCopy := map[int64]string{
		a.copyOf(obj{"edition_id": withCover, "status": "owned"}):      editionCover,
		a.copyOf(obj{"edition_id": withoutCover, "status": "owned"}):   titleCover,
		a.copyOf(obj{"edition_id": withoutArtwork, "status": "owned"}): "",
	}

	var view collection.CollectionView
	a.mustDo(http.MethodGet, "/api/collection", nil, &view, http.StatusOK)
	if len(view.Items) != len(wantByCopy) {
		t.Fatalf("collection contains %d copies, want %d", len(view.Items), len(wantByCopy))
	}
	for _, item := range view.Items {
		want, found := wantByCopy[item.CopyID]
		if !found || (item.CoverURL == nil && want != "") || (item.CoverURL != nil && *item.CoverURL != want) {
			t.Errorf("copy %d cover = %v, want %q", item.CopyID, item.CoverURL, want)
		}
		if item.TitleID == title.ID && (len(item.Authors) != 1 || item.Authors[0] != "Stanisław Lem") {
			t.Errorf("copy %d authors = %v, want Stanisław Lem", item.CopyID, item.Authors)
		}
	}
}

func TestCollectionViewReturnsWishPriceLimits(t *testing.T) {
	a := newAPI(t)
	edition := a.edition(obj{"title_id": a.title("movie", "Ida"), "format": "dvd"})
	wantByCopy := map[int64]*int64{
		a.copyOf(obj{"edition_id": edition, "status": "wishlist", "max_price_cents": 8000}): new(int64(8000)),
		a.copyOf(obj{"edition_id": edition, "status": "wishlist"}):                          nil,
		a.copyOf(obj{"edition_id": edition, "status": "owned", "price_cents": 2500}):        nil,
	}

	var view collection.CollectionView
	a.mustDo(http.MethodGet, "/api/collection", nil, &view, http.StatusOK)
	if len(view.Items) != len(wantByCopy) {
		t.Fatalf("collection contains %d copies, want %d", len(view.Items), len(wantByCopy))
	}
	for _, item := range view.Items {
		want, found := wantByCopy[item.CopyID]
		if !found || (want == nil) != (item.MaxPriceCents == nil) ||
			(want != nil && *item.MaxPriceCents != *want) {
			t.Errorf("copy %d max price = %v, want %v", item.CopyID, item.MaxPriceCents, want)
		}
	}
}

func TestListTitles(t *testing.T) {
	a := newAPI(t)
	a.title("movie", "Rejs")
	a.title("movie", "Miś")
	a.title("book", "Lalka")
	a.mustDo(http.MethodPost, "/api/titles", obj{"kind": "movie", "name": "Seksmisja", "tmdb_id": 11202}, nil, http.StatusCreated)

	tests := []struct {
		query string
		want  []string
	}{
		{"", []string{"Lalka", "Miś", "Rejs", "Seksmisja"}},
		{"?kind=movie", []string{"Miś", "Rejs", "Seksmisja"}},
		{"?q=ALK", []string{"Lalka"}},
		{"?tmdb_id=11202", []string{"Seksmisja"}},
		{"?tmdb_id=1", []string{}},
	}
	for _, tt := range tests {
		var titles []collection.Title
		a.mustDo(http.MethodGet, "/api/titles"+tt.query, nil, &titles, http.StatusOK)
		names := make([]string, 0, len(titles))
		for i := range titles {
			names = append(names, titles[i].Name)
		}
		if strings.Join(names, ",") != strings.Join(tt.want, ",") {
			t.Errorf("titles%s = %v, want %v", tt.query, names, tt.want)
		}
	}
	a.fails(http.MethodGet, "/api/titles?kind=vinyl", nil, http.StatusUnprocessableEntity, "kind")
	a.fails(http.MethodGet, "/api/titles?tmdb_id=x", nil, http.StatusUnprocessableEntity, "tmdb_id")
	a.fails(http.MethodGet, "/api/titles?tmdb_id=0", nil, http.StatusUnprocessableEntity, "tmdb_id")
}

func titleTags(a *api, id int64) []string {
	a.t.Helper()
	var detail collection.TitleDetail
	a.mustDo(http.MethodGet, fmt.Sprintf("/api/titles/%d", id), nil, &detail, http.StatusOK)
	return detail.Tags
}

func TestTitleTags(t *testing.T) {
	a := newAPI(t)
	var created collection.Title
	a.mustDo(http.MethodPost, "/api/titles", obj{
		"kind": "movie", "name": "Obcy", "tags": []string{" Horror ", "", "sci-fi", "HORROR", "  "},
	}, &created, http.StatusCreated)
	if got := strings.Join(created.Tags, ","); got != "Horror,sci-fi" {
		t.Fatalf("created tags = %q, want Horror,sci-fi", got)
	}
	path := fmt.Sprintf("/api/titles/%d", created.ID)
	if got := strings.Join(titleTags(a, created.ID), ","); got != "Horror,sci-fi" {
		t.Fatalf("detail tags = %q, want Horror,sci-fi", got)
	}

	// A body without tags keeps them, as it keeps cover_url; null does too.
	var updated collection.Title
	a.mustDo(http.MethodPut, path, obj{"kind": "movie", "name": "Obcy – 8. pasażer Nostromo"}, &updated, http.StatusOK)
	if got := strings.Join(updated.Tags, ","); got != "Horror,sci-fi" {
		t.Fatalf("tags after a PUT without them = %q", got)
	}
	a.mustDo(http.MethodPut, path, obj{"kind": "movie", "name": "Obcy", "tags": nil}, &updated, http.StatusOK)
	if got := strings.Join(updated.Tags, ","); got != "Horror,sci-fi" {
		t.Fatalf("tags after a PUT with null = %q", got)
	}

	// A tag keeps the spelling it was first saved with on every title.
	other := a.title("movie", "Coś")
	a.mustDo(http.MethodPut, fmt.Sprintf("/api/titles/%d", other), obj{
		"kind": "movie", "name": "Coś", "tags": []string{"horror", "Kult"},
	}, &updated, http.StatusOK)
	if got := strings.Join(updated.Tags, ","); got != "Horror,Kult" {
		t.Fatalf("other title's tags = %q, want Horror,Kult", got)
	}

	a.mustDo(http.MethodPut, path, obj{"kind": "movie", "name": "Obcy", "tags": []string{"Kult", "SCI-FI"}}, &updated, http.StatusOK)
	if got := strings.Join(updated.Tags, ","); got != "Kult,SCI-FI" {
		t.Fatalf("reordered tags = %q, want Kult,SCI-FI (sci-fi is no title's tag once replaced)", got)
	}
	a.mustDo(http.MethodPut, path, obj{"kind": "movie", "name": "Obcy", "tags": []string{}}, &updated, http.StatusOK)
	if updated.Tags == nil || len(updated.Tags) != 0 {
		t.Fatalf("tags after [] = %#v, want an empty list", updated.Tags)
	}
	if got := titleTags(a, created.ID); got == nil || len(got) != 0 {
		t.Fatalf("detail tags after [] = %#v, want an empty list", got)
	}

	var plain collection.Title
	a.mustDo(http.MethodPost, "/api/titles", obj{"kind": "book", "name": "Lalka"}, &plain, http.StatusCreated)
	if plain.Tags == nil || len(plain.Tags) != 0 {
		t.Fatalf("untagged title tags = %#v, want an empty list", plain.Tags)
	}

	tooMany := make([]string, 21)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("tag %d", i)
	}
	twenty := slices.Clone(tooMany[:20])
	twenty = append(twenty, "TAG 0", " ")
	for _, body := range []obj{
		{"kind": "movie", "name": "Obcy", "tags": tooMany},
		{"kind": "movie", "name": "Obcy", "tags": []string{strings.Repeat("ą", 251)}},
		{"kind": "movie", "name": "Obcy", "tags": []string{"a\x00b"}},
	} {
		a.fails(http.MethodPost, "/api/titles", body, http.StatusUnprocessableEntity, "tags")
		a.fails(http.MethodPut, path, body, http.StatusUnprocessableEntity, "tags")
	}
	for _, tags := range []any{[]any{nil}, []any{"Kult", nil}, []any{"Kult", 1}, "Kult", 7, obj{"name": "Kult"}} {
		a.fails(http.MethodPost, "/api/titles", obj{"kind": "movie", "name": "Obcy", "tags": tags}, http.StatusUnprocessableEntity, "tags")
		a.mustDo(http.MethodPut, path, obj{"kind": "movie", "name": "Obcy", "tags": []string{"Horror"}}, nil, http.StatusOK)
		a.fails(http.MethodPut, path, obj{"kind": "movie", "name": "Obcy", "tags": tags}, http.StatusUnprocessableEntity, "tags")
		if got := titleTags(a, created.ID); strings.Join(got, ",") != "Horror" {
			t.Fatalf("tags after a refused PUT with %v = %q, want Horror kept", tags, got)
		}
	}
	a.mustDo(http.MethodPut, path, obj{"kind": "movie", "name": "Obcy", "tags": twenty}, &updated, http.StatusOK)
	if len(updated.Tags) != 20 {
		t.Fatalf("20 tags after collapsing a duplicate and a blank = %d", len(updated.Tags))
	}
	a.mustDo(http.MethodPut, path, obj{"kind": "movie", "name": "Obcy", "tags": []string{strings.Repeat("ą", 250)}}, &updated, http.StatusOK)
}

func TestListTagsCountsTitles(t *testing.T) {
	a := newAPI(t)
	tag := func(id int64, kind, name string, tags ...string) {
		if tags == nil {
			tags = []string{} // clears them; null would keep them
		}
		a.mustDo(http.MethodPut, fmt.Sprintf("/api/titles/%d", id), obj{"kind": kind, "name": name, "tags": tags}, nil, http.StatusOK)
	}
	alien := a.title("movie", "Obcy")
	thing := a.title("movie", "Coś")
	lalka := a.title("book", "Lalka")
	tag(alien, "movie", "Obcy", "Horror", "kult")
	tag(thing, "movie", "Coś", "horror")
	tag(lalka, "book", "Lalka", "Do sprzedania")

	list := func() string {
		var tags []collection.TagCount
		a.mustDo(http.MethodGet, "/api/tags", nil, &tags, http.StatusOK)
		parts := make([]string, 0, len(tags))
		for _, c := range tags {
			parts = append(parts, fmt.Sprintf("%s=%d", c.Name, c.Titles))
		}
		return strings.Join(parts, ",")
	}
	if got := list(); got != "Do sprzedania=1,Horror=2,kult=1" {
		t.Fatalf("tags = %q", got)
	}

	// A tag no title carries is gone from the list, whether the title dropped it or went away.
	tag(alien, "movie", "Obcy")
	a.mustDo(http.MethodDelete, fmt.Sprintf("/api/titles/%d", lalka), nil, nil, http.StatusNoContent)
	if got := list(); got != "Horror=1" {
		t.Fatalf("tags after removals = %q, want Horror=1", got)
	}
	// One no title carries takes the spelling saved next.
	tag(alien, "movie", "Obcy", "Kult")
	if got := list(); got != "Horror=1,Kult=1" {
		t.Fatalf("tags after a new spelling = %q, want Horror=1,Kult=1", got)
	}
	tag(thing, "movie", "Coś")
	tag(alien, "movie", "Obcy")
	if got := list(); got != "" {
		t.Fatalf("tags with no tagged title = %q, want none", got)
	}
}

func TestTagsMatchByGoCaseFold(t *testing.T) {
	a := newAPI(t)
	cases := []struct{ saved, filter string }{
		{"ΟΣ", "ος"},
		{"Ᲊ", "ᲊ"},
		{"İSTANBUL", "istanbul"},
	}
	for _, tc := range cases {
		name := "Tytuł " + tc.saved
		var created collection.Title
		a.mustDo(http.MethodPost, "/api/titles", obj{"kind": "movie", "name": name, "tags": []string{tc.saved, tc.filter}}, &created, http.StatusCreated)
		if got := strings.Join(created.Tags, ","); got != tc.saved {
			t.Errorf("tags %q and %q saved as %q, want one tag %q", tc.saved, tc.filter, got, tc.saved)
		}
		a.copyOf(obj{"edition_id": a.edition(obj{"title_id": created.ID, "format": "dvd"}), "status": "owned"})
		var titles []collection.Title
		a.mustDo(http.MethodGet, "/api/titles?tag="+url.QueryEscape(tc.filter), nil, &titles, http.StatusOK)
		var view collection.CollectionView
		a.mustDo(http.MethodGet, "/api/collection?tag="+url.QueryEscape(tc.filter), nil, &view, http.StatusOK)
		if len(titles) != 1 || len(view.Items) != 1 {
			t.Errorf("?tag=%s found %d titles and %d copies, want the one tagged %s", tc.filter, len(titles), len(view.Items), tc.saved)
		}
	}
}

func TestTagKeepsFirstSpellingWhileAnotherSaveWaits(t *testing.T) {
	a, pool := newAPIWithPool(t)
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var titleID, tagID int64
	if err := tx.QueryRow(ctx, `INSERT INTO title (kind, name) VALUES ('movie', 'Pierwszy') RETURNING id`).Scan(&titleID); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO tag (name, key) VALUES ('First-Use', 'first-use') RETURNING id`).Scan(&tagID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO title_tag (title_id, tag_id, position) VALUES ($1, $2, 0)`, titleID, tagID); err != nil {
		t.Fatal(err)
	}

	type answer struct {
		status int
		title  collection.Title
	}
	done := make(chan answer, 1)
	go func() {
		var second collection.Title
		status := a.do(http.MethodPost, "/api/titles", obj{"kind": "movie", "name": "Drugi", "tags": []string{"first-use"}}, &second)
		done <- answer{status, second}
	}()
	waitForLockWait(t, pool)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	got := <-done
	if got.status != http.StatusCreated || strings.Join(got.title.Tags, ",") != "First-Use" {
		t.Fatalf("second save = %d %q, want 201 with the first spelling First-Use", got.status, got.title.Tags)
	}
	if tags := titleTags(a, titleID); strings.Join(tags, ",") != "First-Use" {
		t.Fatalf("first title's tags = %q, want First-Use", tags)
	}
}

// waitForLockWait returns once a session of the test's database waits for
// a lock, the request the test started.
func waitForLockWait(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var waiting bool
		err := pool.QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock')`).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no request waited for the held tag")
}

func TestConcurrentSavesOfTagsInAnotherOrderAllSucceed(t *testing.T) {
	a := newAPI(t)
	for round := range 30 {
		first, second := fmt.Sprintf("Horror %d", round), fmt.Sprintf("Kult %d", round)
		statuses := make(chan int, 2)
		for i, tags := range [][]string{{first, second}, {second, first}} {
			go func() {
				var created collection.Title
				statuses <- a.do(http.MethodPost, "/api/titles", obj{
					"kind": "movie", "name": fmt.Sprintf("Film %d-%d", round, i), "tags": tags,
				}, &created)
			}()
		}
		for range 2 {
			if status := <-statuses; status != http.StatusCreated {
				t.Fatalf("round %d: a save of the same tags in another order answered %d, want 201", round, status)
			}
		}
	}
}

func TestCollectionAndTitlesFilterByTag(t *testing.T) {
	a := newAPI(t)
	alien := a.title("movie", "Obcy")
	a.mustDo(http.MethodPut, fmt.Sprintf("/api/titles/%d", alien), obj{"kind": "movie", "name": "Obcy", "tags": []string{"Horror"}}, nil, http.StatusOK)
	thing := a.title("movie", "Coś")
	a.mustDo(http.MethodPut, fmt.Sprintf("/api/titles/%d", thing), obj{"kind": "movie", "name": "Coś", "tags": []string{"horror", "Kult"}}, nil, http.StatusOK)
	uncopied := a.title("movie", "Egzorcysta")
	a.mustDo(http.MethodPut, fmt.Sprintf("/api/titles/%d", uncopied), obj{"kind": "movie", "name": "Egzorcysta", "tags": []string{"HORROR"}}, nil, http.StatusOK)
	a.title("book", "Lalka")

	alienDVD := a.edition(obj{"title_id": alien, "format": "dvd"})
	alienBluray := a.edition(obj{"title_id": alien, "format": "bluray"})
	thingDVD := a.edition(obj{"title_id": thing, "format": "dvd"})
	a.copyOf(obj{"edition_id": alienDVD, "status": "owned"})
	a.copyOf(obj{"edition_id": alienBluray, "status": "wishlist"})
	a.copyOf(obj{"edition_id": thingDVD, "status": "owned"})

	copies := []struct {
		query string
		want  int
	}{
		{"?tag=horror", 3},
		{"?tag=HORROR&format=dvd", 2},
		{"?tag=+Kult+", 1},
		{"?tag=kult&status=wishlist", 0},
		{"?tag=", 3},
		{"?tag=hor", 0},
	}
	for _, tt := range copies {
		var view collection.CollectionView
		a.mustDo(http.MethodGet, "/api/collection"+tt.query, nil, &view, http.StatusOK)
		if len(view.Items) != tt.want || view.Totals.Copies != tt.want {
			t.Errorf("collection%s = %d copies, want %d", tt.query, len(view.Items), tt.want)
		}
	}

	var view collection.CollectionView
	a.mustDo(http.MethodGet, "/api/collection?tag=kult", nil, &view, http.StatusOK)
	if got := strings.Join(view.Items[0].Tags, ","); got != "Horror,Kult" {
		t.Fatalf("item tags = %q, want Horror,Kult", got)
	}

	titles := []struct {
		query string
		want  []string
	}{
		{"?tag=Horror", []string{"Coś", "Egzorcysta", "Obcy"}},
		{"?tag=kult", []string{"Coś"}},
		{"?tag=horror&q=egz", []string{"Egzorcysta"}},
		{"?tag=horror&kind=book", []string{}},
		{"?tag=", []string{"Coś", "Egzorcysta", "Lalka", "Obcy"}},
	}
	for _, tt := range titles {
		var got []collection.Title
		a.mustDo(http.MethodGet, "/api/titles"+tt.query, nil, &got, http.StatusOK)
		names := make([]string, 0, len(got))
		for i := range got {
			names = append(names, got[i].Name)
		}
		if strings.Join(names, ",") != strings.Join(tt.want, ",") {
			t.Errorf("titles%s = %v, want %v", tt.query, names, tt.want)
		}
	}
}

func TestBarcodeSpellingsShareOneEdition(t *testing.T) {
	a := newAPI(t)
	movie := a.title("movie", "UPC disc")
	var e collection.Edition
	a.mustDo(http.MethodPost, "/api/editions", obj{"title_id": movie, "barcode": "036000291452", "format": "dvd"}, &e, http.StatusCreated)
	if *e.Barcode != "0036000291452" {
		t.Fatalf("stored barcode = %q, want EAN-13 form", *e.Barcode)
	}
	for _, spelling := range []string{"0036000291452", "00036000291452"} {
		a.fails(http.MethodPost, "/api/editions", obj{"title_id": movie, "barcode": spelling, "format": "dvd"}, http.StatusConflict, "barcode")
		var found []collection.Edition
		a.mustDo(http.MethodGet, "/api/editions?barcode="+spelling, nil, &found, http.StatusOK)
		if len(found) != 1 || found[0].ID != e.ID {
			t.Fatalf("editions?barcode=%s = %+v", spelling, found)
		}
	}
}

// TestKindChangeWaitsForConcurrentEdition holds an edition insert open on the
// title's share lock, starts a kind change that must wait for it, then
// commits the insert: the kind change has to see the new edition.
func TestKindChangeWaitsForConcurrentEdition(t *testing.T) {
	pool := dbtest.Migrated(t)
	store := collection.NewStore(pool)
	ctx := context.Background()
	in := collection.TitleInput{Kind: "movie", Name: "Race"}
	if fe := in.Normalize(); fe != nil {
		t.Fatalf("normalize: %v", fe)
	}
	title, _, err := store.CreateTitle(ctx, &in)
	if err != nil {
		t.Fatalf("create title: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT kind FROM title WHERE id = $1 FOR SHARE`, title.ID); err != nil {
		t.Fatalf("share lock: %v", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO edition (title_id, format) VALUES ($1, 'dvd')`, title.ID); err != nil {
		t.Fatalf("insert edition: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		book := collection.TitleInput{Kind: "book", Name: "Race", Authors: []string{}}
		_, err := store.UpdateTitle(ctx, title.ID, &book)
		done <- err
	}()

	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting bool
		err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity
			WHERE wait_event_type = 'Lock' AND query LIKE '%FOR UPDATE%')`).Scan(&waiting)
		if err != nil {
			t.Fatalf("poll locks: %v", err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("kind change never waited on the title lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit edition: %v", err)
	}

	if err := <-done; !errors.Is(err, collection.ErrKindLocked) {
		t.Fatalf("kind change err = %v, want ErrKindLocked", err)
	}
	got, err := store.GetTitle(ctx, title.ID)
	if err != nil || got.Kind != "movie" {
		t.Fatalf("title after race = %+v, %v; want movie", got, err)
	}
}

func TestCopyRequestKeyMakesCreateRetrySafe(t *testing.T) {
	a := newAPI(t)
	movie := a.title("movie", "Rejs")
	edition := a.edition(obj{"title_id": movie, "format": "dvd"})
	body := obj{"edition_id": edition, "status": "owned", "price_cents": 4999, "request_key": " scan-1 "}

	var first, retry collection.Copy
	a.mustDo(http.MethodPost, "/api/copies", body, &first, http.StatusCreated)
	a.mustDo(http.MethodPost, "/api/copies", body, &retry, http.StatusOK)
	if retry.ID != first.ID || retry.PriceCents == nil || *retry.PriceCents != 4999 {
		t.Fatalf("retry = %+v, first %+v", retry, first)
	}

	a.mustDo(http.MethodPost, "/api/copies", obj{"edition_id": edition, "status": "owned", "request_key": "scan-2"}, nil, http.StatusCreated)
	a.mustDo(http.MethodPost, "/api/copies", obj{"edition_id": edition, "status": "owned"}, nil, http.StatusCreated)
	a.mustDo(http.MethodPost, "/api/copies", obj{"edition_id": edition, "status": "owned", "request_key": "  "}, nil, http.StatusCreated)
	var view collection.CollectionView
	a.mustDo(http.MethodGet, "/api/collection", nil, &view, http.StatusOK)
	if view.Totals.Copies != 4 {
		t.Fatalf("copies = %d, want 4 (one per distinct or absent key)", view.Totals.Copies)
	}

	a.fails(http.MethodPost, "/api/copies", obj{"edition_id": edition, "status": "owned", "request_key": strings.Repeat("k", 101)}, http.StatusUnprocessableEntity, "request_key")
}

func TestTitleRequestKeyMakesCreateRetrySafe(t *testing.T) {
	a := newAPI(t)
	body := obj{"kind": "movie", "name": "Rejs", "tmdb_id": 41187, "request_key": " scan-1 "}

	var first, retry collection.Title
	a.mustDo(http.MethodPost, "/api/titles", body, &first, http.StatusCreated)
	a.mustDo(http.MethodPost, "/api/titles", body, &retry, http.StatusOK)
	if retry.ID != first.ID || retry.RequestKey != nil {
		t.Fatalf("retry = %+v, first %+v", retry, first)
	}

	a.fails(http.MethodPost, "/api/titles", obj{"kind": "movie", "name": "Rejs", "tmdb_id": 41187, "request_key": "scan-2"},
		http.StatusConflict, "tmdb_id")
	a.mustDo(http.MethodPost, "/api/titles", obj{"kind": "book", "name": "Lalka", "request_key": "scan-3"}, nil, http.StatusCreated)
	a.mustDo(http.MethodPost, "/api/titles", obj{"kind": "book", "name": "Lalka", "request_key": "  "}, nil, http.StatusCreated)
	var titles []collection.Title
	a.mustDo(http.MethodGet, "/api/titles", nil, &titles, http.StatusOK)
	if len(titles) != 3 {
		t.Fatalf("titles = %d, want 3 (one per distinct or absent key)", len(titles))
	}

	a.fails(http.MethodPost, "/api/titles", obj{"kind": "book", "name": "Lalka", "request_key": strings.Repeat("k", 101)},
		http.StatusUnprocessableEntity, "request_key")
}

func TestEditionRequestKeyMakesCreateRetrySafe(t *testing.T) {
	a := newAPI(t)
	movie := a.title("movie", "Rejs")
	body := obj{"title_id": movie, "barcode": "5901234123457", "format": "dvd", "request_key": "scan-1"}

	var first, retry collection.Edition
	a.mustDo(http.MethodPost, "/api/editions", body, &first, http.StatusCreated)
	a.mustDo(http.MethodPost, "/api/editions", body, &retry, http.StatusOK)
	if retry.ID != first.ID || retry.RequestKey != nil {
		t.Fatalf("retry = %+v, first %+v", retry, first)
	}

	a.fails(http.MethodPost, "/api/editions", obj{"title_id": movie, "barcode": "5901234123457", "format": "dvd", "request_key": "scan-2"},
		http.StatusConflict, "barcode")
	a.mustDo(http.MethodPost, "/api/editions", obj{"title_id": movie, "format": "bluray", "request_key": "scan-3"}, nil, http.StatusCreated)
	a.mustDo(http.MethodPost, "/api/editions", obj{"title_id": movie, "format": "uhd"}, nil, http.StatusCreated)
	var detail collection.TitleDetail
	a.mustDo(http.MethodGet, fmt.Sprintf("/api/titles/%d", movie), nil, &detail, http.StatusOK)
	if len(detail.Editions) != 3 {
		t.Fatalf("editions = %d, want 3 (one per distinct or absent key)", len(detail.Editions))
	}

	a.fails(http.MethodPost, "/api/editions", obj{"title_id": movie, "format": "dvd", "request_key": strings.Repeat("k", 101)},
		http.StatusUnprocessableEntity, "request_key")
}

func TestDiscardRequestRemovesWhatAFailedAddMade(t *testing.T) {
	a := newAPI(t)
	exists := func(path string) bool {
		t.Helper()
		switch code := a.do(http.MethodGet, path, nil, nil); code {
		case http.StatusOK:
			return true
		case http.StatusNotFound:
			return false
		default:
			t.Fatalf("GET %s = %d", path, code)
			return false
		}
	}
	add := func(key string, edition obj) (int64, int64, int64) {
		t.Helper()
		var tt collection.Title
		a.mustDo(http.MethodPost, "/api/titles", obj{"kind": "movie", "name": "Diuna", "request_key": key}, &tt, http.StatusCreated)
		edition["title_id"], edition["format"], edition["request_key"] = tt.ID, "uhd", key
		ed := a.edition(edition)
		return tt.ID, ed, a.copyOf(obj{"edition_id": ed, "status": "owned", "request_key": key})
	}
	path := func(kind string, id int64) string { return fmt.Sprintf("/api/%s/%d", kind, id) }

	title, ed, cp := add("all", obj{})
	a.mustDo(http.MethodDelete, "/api/requests/all", nil, nil, http.StatusNoContent)
	if exists(path("copies", cp)) || exists(path("editions", ed)) || exists(path("titles", title)) {
		t.Fatal("a failed add left rows behind")
	}
	a.mustDo(http.MethodDelete, "/api/requests/all", nil, nil, http.StatusNoContent)
	a.mustDo(http.MethodDelete, "/api/requests/unknown", nil, nil, http.StatusNoContent)

	title, ed, cp = add("shared", obj{})
	other := a.copyOf(obj{"edition_id": ed, "status": "owned"})
	a.mustDo(http.MethodDelete, "/api/requests/shared", nil, nil, http.StatusNoContent)
	if exists(path("copies", cp)) || !exists(path("copies", other)) || !exists(path("editions", ed)) || !exists(path("titles", title)) {
		t.Fatal("an edition another copy uses must stay, with its title, and only the add's copy go")
	}

	title, ed, _ = add("edited", obj{})
	a.mustDo(http.MethodPut, path("editions", ed), obj{"title_id": title, "format": "uhd", "notes": "z innej sesji"}, nil, http.StatusOK)
	a.mustDo(http.MethodDelete, "/api/requests/edited", nil, nil, http.StatusNoContent)
	if !exists(path("editions", ed)) || !exists(path("titles", title)) {
		t.Fatal("an edition a person edited must stay")
	}

	title, ed, _ = add("photo", obj{})
	a.mustDo(http.MethodPut, path("editions", ed)+"/cover", coverPNG(t, 1), nil, http.StatusNoContent)
	a.mustDo(http.MethodDelete, "/api/requests/photo", nil, nil, http.StatusNoContent)
	if !exists(path("editions", ed)) || !exists(path("titles", title)) {
		t.Fatal("an edition with a cover photo must stay")
	}

	title, ed, _ = add("barcode", obj{"barcode": "5901234123457"})
	a.mustDo(http.MethodDelete, "/api/requests/barcode", nil, nil, http.StatusNoContent)
	if !exists(path("editions", ed)) || !exists(path("titles", title)) {
		t.Fatal("an edition with a barcode must stay for the scanner's undo")
	}

	reused := a.title("movie", "Rejs")
	ed = a.edition(obj{"title_id": reused, "format": "dvd", "request_key": "reused"})
	a.mustDo(http.MethodDelete, "/api/requests/reused", nil, nil, http.StatusNoContent)
	if exists(path("editions", ed)) || !exists(path("titles", reused)) {
		t.Fatal("a title the add did not make must stay")
	}

	var renamed collection.Title
	a.mustDo(http.MethodPost, "/api/titles", obj{"kind": "book", "name": "Lalka", "request_key": "renamed"}, &renamed, http.StatusCreated)
	a.mustDo(http.MethodPut, path("titles", renamed.ID), obj{"kind": "book", "name": "Lalka (wyd. 2)"}, nil, http.StatusOK)
	a.mustDo(http.MethodDelete, "/api/requests/renamed", nil, nil, http.StatusNoContent)
	if !exists(path("titles", renamed.ID)) {
		t.Fatal("a title a person edited must stay")
	}

	title, ed, cp = add("copy-edited", obj{})
	a.mustDo(http.MethodPut, path("copies", cp), obj{"edition_id": ed, "status": "owned", "shop": "z innej sesji"}, nil, http.StatusOK)
	a.mustDo(http.MethodDelete, "/api/requests/copy-edited", nil, nil, http.StatusNoContent)
	if !exists(path("copies", cp)) || !exists(path("editions", ed)) || !exists(path("titles", title)) {
		t.Fatal("a copy a person edited must stay, with its edition and title")
	}

	title, ed, cp = add("a%2Fb", obj{})
	other, otherEd, otherCp := add("a/b", obj{})
	a.mustDo(http.MethodDelete, "/api/requests/"+url.PathEscape("a%2Fb"), nil, nil, http.StatusNoContent)
	if exists(path("copies", cp)) || exists(path("editions", ed)) || exists(path("titles", title)) {
		t.Fatal("a key holding an escape sequence must reach its own rows")
	}
	if !exists(path("copies", otherCp)) || !exists(path("editions", otherEd)) || !exists(path("titles", other)) {
		t.Fatal("decoding a key twice reached another key's rows")
	}

	title, ed, cp = add("a/b c", obj{})
	a.mustDo(http.MethodDelete, "/api/requests/"+url.PathEscape("a/b c"), nil, nil, http.StatusNoContent)
	if exists(path("copies", cp)) || exists(path("editions", ed)) || exists(path("titles", title)) {
		t.Fatal("an escaped key must reach the rows it made")
	}

	a.mustDo(http.MethodDelete, "/api/requests/late", nil, nil, http.StatusNoContent)
	a.fails(http.MethodPost, "/api/titles", obj{"kind": "movie", "name": "Spóźniony", "request_key": "late"}, http.StatusConflict, "request_key")
	a.fails(http.MethodPost, "/api/editions", obj{"title_id": reused, "format": "dvd", "request_key": "late"}, http.StatusConflict, "request_key")
	kept := a.edition(obj{"title_id": reused, "format": "bluray"})
	a.fails(http.MethodPost, "/api/copies", obj{"edition_id": kept, "status": "owned", "request_key": "late"}, http.StatusConflict, "request_key")

	a.fails(http.MethodDelete, "/api/requests/"+strings.Repeat("k", 101), nil, http.StatusNotFound, "")

	// A replay after the discard removed the edition's title is refused for its key, not its title.
	title, _, _ = add("gone", obj{})
	a.mustDo(http.MethodDelete, "/api/requests/gone", nil, nil, http.StatusNoContent)
	if exists(path("titles", title)) {
		t.Fatal("the discard left the title")
	}
	a.fails(http.MethodPost, "/api/editions", obj{"title_id": title, "format": "uhd", "request_key": "gone"}, http.StatusConflict, "request_key")
	a.fails(http.MethodPost, "/api/copies", obj{"edition_id": ed, "status": "owned", "request_key": "gone"}, http.StatusConflict, "request_key")

	a.fails(http.MethodPost, "/api/titles", obj{"kind": "movie", "name": "Zero", "request_key": "a\x00b"}, http.StatusUnprocessableEntity, "request_key")
	a.fails(http.MethodPost, "/api/editions", obj{"title_id": reused, "format": "dvd", "request_key": "a\x00b"}, http.StatusUnprocessableEntity, "request_key")
	a.fails(http.MethodPost, "/api/copies", obj{"edition_id": kept, "status": "owned", "request_key": "a\x00b"}, http.StatusUnprocessableEntity, "request_key")
	a.fails(http.MethodDelete, "/api/requests/a%00b", nil, http.StatusNotFound, "")
	a.fails(http.MethodDelete, "/api/requests/a%FFb", nil, http.StatusNotFound, "")
}

// TestCreatesRacingADiscardAnswerTheirRowOrAConflict runs an add's creates
// against a discard of its key: each create answers what it made or 409 on
// request_key, never a missing row or a server error.
func TestCreatesRacingADiscardAnswerTheirRowOrAConflict(t *testing.T) {
	a := newAPI(t)
	// send is do without t.Fatal, for goroutines.
	send := func(method, path string, body any) (int, int64, error) {
		var r io.Reader = http.NoBody
		if body != nil {
			b, err := json.Marshal(body)
			if err != nil {
				return 0, 0, err
			}
			r = bytes.NewReader(b)
		}
		req, err := http.NewRequestWithContext(context.Background(), method, a.url+path, r)
		if err != nil {
			return 0, 0, err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0, 0, err
		}
		defer resp.Body.Close()
		var out struct {
			ID    int64  `json:"id"`
			Field string `json:"field"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil && resp.StatusCode != http.StatusNoContent {
			return resp.StatusCode, 0, fmt.Errorf("%s %s: decode: %w", method, path, err)
		}
		if resp.StatusCode == http.StatusConflict && out.Field != "request_key" {
			return resp.StatusCode, 0, fmt.Errorf("%s %s: 409 on %q", method, path, out.Field)
		}
		return resp.StatusCode, out.ID, nil
	}
	add := func(key string) error {
		code, title, err := send(http.MethodPost, "/api/titles", obj{"kind": "movie", "name": "Diuna", "request_key": key})
		if err != nil || code == http.StatusConflict {
			return err
		}
		if code != http.StatusCreated {
			return fmt.Errorf("title: %d", code)
		}
		// The replay of a create the discard may have removed.
		if code, _, err := send(http.MethodPost, "/api/titles", obj{"kind": "movie", "name": "Diuna", "request_key": key}); err != nil || (code != http.StatusOK && code != http.StatusConflict) {
			return fmt.Errorf("title replay: %d %w", code, err)
		}
		code, edition, err := send(http.MethodPost, "/api/editions", obj{"title_id": title, "format": "uhd", "request_key": key})
		if err != nil || code == http.StatusConflict {
			return err
		}
		if code != http.StatusCreated {
			return fmt.Errorf("edition: %d", code)
		}
		code, _, err = send(http.MethodPost, "/api/copies", obj{"edition_id": edition, "status": "owned", "request_key": key})
		if err != nil || code == http.StatusConflict {
			return err
		}
		if code != http.StatusCreated {
			return fmt.Errorf("copy: %d", code)
		}
		return nil
	}
	for i := range 60 {
		key := fmt.Sprintf("race-%d", i)
		errs := make(chan error, 2)
		go func() { errs <- add(key) }()
		go func() {
			time.Sleep(time.Duration(i%6) * time.Millisecond)
			code, _, err := send(http.MethodDelete, "/api/requests/"+key, nil)
			if err == nil && code != http.StatusNoContent {
				err = fmt.Errorf("discard: %d", code)
			}
			errs <- err
		}()
		for range 2 {
			if err := <-errs; err != nil {
				t.Fatalf("%s: %v", key, err)
			}
		}
	}
}

func TestCopyLocation(t *testing.T) {
	a := newAPI(t)
	edition := a.edition(obj{"title_id": a.title("movie", "Rejs"), "format": "dvd"})

	var created collection.Copy
	a.mustDo(http.MethodPost, "/api/copies", obj{"edition_id": edition, "status": "owned", "location": "  Regał 2 "}, &created, http.StatusCreated)
	if created.Location == nil || *created.Location != "Regał 2" {
		t.Fatalf("created copy = %+v", created)
	}
	path := fmt.Sprintf("/api/copies/%d", created.ID)
	var detail collection.CopyDetail
	a.mustDo(http.MethodGet, path, nil, &detail, http.StatusOK)
	if detail.Location == nil || *detail.Location != "Regał 2" {
		t.Fatalf("GET copy location = %v", detail.Location)
	}
	var view collection.CollectionView
	a.mustDo(http.MethodGet, "/api/collection", nil, &view, http.StatusOK)
	if len(view.Items) != 1 || view.Items[0].Location == nil || *view.Items[0].Location != "Regał 2" {
		t.Fatalf("collection items = %+v", view.Items)
	}

	put := func(status string, location any) *string {
		t.Helper()
		var c collection.Copy
		a.mustDo(http.MethodPut, path, obj{"edition_id": edition, "status": status, "location": location}, &c, http.StatusOK)
		return c.Location
	}
	longest := strings.Repeat("ł", 250)
	if got := put("owned", longest); got == nil || *got != longest {
		t.Fatalf("500-byte location = %v", got)
	}
	a.fails(http.MethodPut, path, obj{"edition_id": edition, "status": "owned", "location": longest + "x"}, http.StatusUnprocessableEntity, "location")
	for _, control := range []string{"Regał\u00002", "Półka\nPółeczka", "Regał\t2", "Regał\u00852"} {
		a.fails(http.MethodPut, path, obj{"edition_id": edition, "status": "owned", "location": control}, http.StatusUnprocessableEntity, "location")
	}
	if got := put("owned", "   "); got != nil {
		t.Fatalf("blank location stored as %q", *got)
	}

	a.fails(http.MethodPost, "/api/copies", obj{"edition_id": edition, "status": "wishlist", "location": "Regał 2"}, http.StatusUnprocessableEntity, "location")
	put("owned", "Pudło")
	a.fails(http.MethodPut, path, obj{"edition_id": edition, "status": "wishlist", "location": "Pudło"}, http.StatusUnprocessableEntity, "location")
	a.fails(http.MethodPut, path, obj{"edition_id": edition, "status": "sold", "sold_on": "2026-05-10", "location": "Pudło"}, http.StatusUnprocessableEntity, "location")
	if got := put("wishlist", nil); got != nil {
		t.Fatalf("wished copy kept location %q", *got)
	}
}

func TestCopyLocationsInUse(t *testing.T) {
	a := newAPI(t)
	var locations []string
	a.mustDo(http.MethodGet, "/api/copies/locations", nil, &locations, http.StatusOK)
	if locations == nil || len(locations) != 0 {
		t.Fatalf("locations without any = %#v, want []", locations)
	}

	edition := a.edition(obj{"title_id": a.title("movie", "Rejs"), "format": "dvd"})
	for _, location := range []string{"Pudło", "regał 2", "Regał 2", "Regał 2", "Strych", "Strych", "Szafa"} {
		a.copyOf(obj{"edition_id": edition, "status": "owned", "location": location})
	}
	a.copyOf(obj{"edition_id": edition, "status": "owned"})
	a.copyOf(obj{"edition_id": edition, "status": "wishlist"})

	a.mustDo(http.MethodGet, "/api/copies/locations", nil, &locations, http.StatusOK)
	want := []string{"Regał 2", "Strych", "Pudło", "Szafa"}
	if fmt.Sprint(locations) != fmt.Sprint(want) {
		t.Fatalf("locations = %q, want %q", locations, want)
	}
}

func TestCollectionViewFiltersByLocation(t *testing.T) {
	a := newAPI(t)
	movie := a.title("movie", "Seksmisja")
	dvd := a.edition(obj{"title_id": movie, "format": "dvd"})
	book := a.title("book", "Solaris")
	paper := a.edition(obj{"title_id": book, "binding": "paperback"})

	shelf := a.copyOf(obj{"edition_id": dvd, "status": "owned", "location": "Regał 2"})
	box := a.copyOf(obj{"edition_id": dvd, "status": "owned", "location": "Regał 2 dolny"})
	a.copyOf(obj{"edition_id": dvd, "status": "owned"})
	a.copyOf(obj{"edition_id": dvd, "status": "wishlist"})
	shelfBook := a.copyOf(obj{"edition_id": paper, "status": "owned", "location": "regał 2"})

	tests := []struct {
		query string
		want  []int64
	}{
		{"?location=" + url.QueryEscape("Regał 2"), []int64{shelf, shelfBook}},
		{"?location=" + url.QueryEscape(" REGAŁ 2 "), []int64{shelf, shelfBook}},
		{"?location=" + url.QueryEscape("Regał"), []int64{}},
		{"?location=" + url.QueryEscape("regał 2") + "&kind=book", []int64{shelfBook}},
		{"?location=" + url.QueryEscape("Regał 2") + "&status=wishlist", []int64{}},
		{"?q=dolny", []int64{box}},
		{"?q=" + url.QueryEscape("REGAŁ"), []int64{shelf, box, shelfBook}},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			var view collection.CollectionView
			a.mustDo(http.MethodGet, "/api/collection"+tt.query, nil, &view, http.StatusOK)
			got := []int64{}
			for _, it := range view.Items {
				got = append(got, it.CopyID)
			}
			if fmt.Sprint(got) != fmt.Sprint(tt.want) {
				t.Fatalf("copies = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLocationMatchesAsTheBrowserDoes(t *testing.T) {
	a := newAPI(t)
	edition := a.edition(obj{"title_id": a.title("movie", "Rejs"), "format": "dvd"})
	boxed := a.copyOf(obj{"edition_id": edition, "status": "owned", "location": "Box 012345678905"})
	marked := a.copyOf(obj{"edition_id": edition, "status": "owned", "location": "\ufeffRegał"})
	upper := a.copyOf(obj{"edition_id": edition, "status": "owned", "location": "\U00010D50"})
	lower := a.copyOf(obj{"edition_id": edition, "status": "owned", "location": "\U00010D70"})

	var c collection.Copy
	a.mustDo(http.MethodGet, fmt.Sprintf("/api/copies/%d", marked), nil, &c, http.StatusOK)
	if c.Location == nil || *c.Location != "\ufeffRegał" {
		t.Fatalf("location with a byte order mark = %q", deref(c.Location))
	}

	tests := []struct {
		query string
		want  []int64
	}{
		{"?q=012345678905", []int64{boxed}},
		{"?location=" + url.QueryEscape("\ufeffregał"), []int64{marked}},
		{"?location=" + url.QueryEscape("Regał"), []int64{}},
		{"?location=" + url.QueryEscape("\U00010D50"), []int64{upper, lower}},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			var view collection.CollectionView
			a.mustDo(http.MethodGet, "/api/collection"+tt.query, nil, &view, http.StatusOK)
			got := []int64{}
			for _, it := range view.Items {
				got = append(got, it.CopyID)
			}
			if fmt.Sprint(got) != fmt.Sprint(tt.want) {
				t.Fatalf("copies = %v, want %v", got, tt.want)
			}
		})
	}

	var locations []string
	a.mustDo(http.MethodGet, "/api/copies/locations", nil, &locations, http.StatusOK)
	want := []string{"\U00010D50", "Box 012345678905", "\ufeffRegał"}
	if fmt.Sprint(locations) != fmt.Sprint(want) {
		t.Fatalf("locations = %q, want %q", locations, want)
	}
}

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

func TestLocationFinalSigmaIsOnePlace(t *testing.T) {
	a := newAPI(t)
	edition := a.edition(obj{"title_id": a.title("movie", "Rejs"), "format": "dvd"})
	upper := a.copyOf(obj{"edition_id": edition, "status": "owned", "location": "ΟΣ"})
	lower := a.copyOf(obj{"edition_id": edition, "status": "owned", "location": "ος"})

	var locations []string
	a.mustDo(http.MethodGet, "/api/copies/locations", nil, &locations, http.StatusOK)
	if fmt.Sprint(locations) != fmt.Sprint([]string{"ΟΣ"}) {
		t.Fatalf("locations = %q, want one place", locations)
	}
	for _, place := range []string{"ΟΣ", "ος", "οσ"} {
		var view collection.CollectionView
		a.mustDo(http.MethodGet, "/api/collection?location="+url.QueryEscape(place), nil, &view, http.StatusOK)
		got := []int64{}
		for _, it := range view.Items {
			got = append(got, it.CopyID)
		}
		if fmt.Sprint(got) != fmt.Sprint([]int64{upper, lower}) {
			t.Fatalf("location %q lists %v, want %v", place, got, []int64{upper, lower})
		}
	}
}
