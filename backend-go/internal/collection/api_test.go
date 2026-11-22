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
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

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
	pool := dbtest.Migrated(t)
	logger := slog.New(slog.DiscardHandler)
	h := collection.NewHandler(collection.NewStore(pool), logger)
	srv := httptest.NewServer(server.New(server.Config{}, logger, server.Deps{DB: pool, API: []server.Mounter{h}}))
	t.Cleanup(srv.Close)
	return &api{t: t, url: srv.URL}
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

func TestDuplicateTMDBAndBadBodies(t *testing.T) {
	a := newAPI(t)
	a.mustDo(http.MethodPost, "/api/titles", obj{"kind": "movie", "name": "A", "tmdb_id": 5}, nil, http.StatusCreated)
	a.fails(http.MethodPost, "/api/titles", obj{"kind": "movie", "name": "B", "tmdb_id": 5}, http.StatusConflict, "tmdb_id")
	a.fails(http.MethodPost, "/api/titles", "not an object", http.StatusUnprocessableEntity, "body")
	a.fails(http.MethodPost, "/api/titles", obj{"kind": "movie", "name": "x", "year": "1984"}, http.StatusUnprocessableEntity, "body")
	a.fails(http.MethodPost, "/api/copies", obj{"edition_id": 1, "status": "owned", "purchased_on": "01.11.2026"}, http.StatusUnprocessableEntity, "body")
	a.fails(http.MethodPost, "/api/titles", obj{"kind": "movie", "name": strings.Repeat("x", 70000)}, http.StatusUnprocessableEntity, "body")
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
	a.fails(http.MethodPut, path, obj{"edition_id": edition, "status": "owned", "location": "Regał\u00002"}, http.StatusUnprocessableEntity, "location")
	if got := put("owned", "   "); got != nil {
		t.Fatalf("blank location stored as %q", *got)
	}

	a.fails(http.MethodPost, "/api/copies", obj{"edition_id": edition, "status": "wishlist", "location": "Regał 2"}, http.StatusUnprocessableEntity, "location")
	put("owned", "Pudło")
	a.fails(http.MethodPut, path, obj{"edition_id": edition, "status": "wishlist", "location": "Pudło"}, http.StatusUnprocessableEntity, "location")
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
