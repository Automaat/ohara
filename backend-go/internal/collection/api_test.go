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
	a.copyOf(obj{"edition_id": paper, "status": "owned", "price_cents": 2550})

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

	for _, q := range []string{"?kind=vinyl", "?format=vhs", "?status=lent"} {
		field, _, _ := strings.Cut(strings.TrimPrefix(q, "?"), "=")
		a.fails(http.MethodGet, "/api/collection"+q, nil, http.StatusUnprocessableEntity, field)
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
	title, err := store.CreateTitle(ctx, &in)
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
