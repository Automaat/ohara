package export_test

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Automaat/ohara/backend-go/internal/collection"
	"github.com/Automaat/ohara/backend-go/internal/dbtest"
	"github.com/Automaat/ohara/backend-go/internal/export"
	"github.com/Automaat/ohara/backend-go/internal/server"
	"github.com/Automaat/ohara/backend-go/internal/valuation"
)

const trickyNote = "półka; \"górna\"\ndruga linia"

func newServer(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	pool := dbtest.Migrated(t)
	logger := slog.New(slog.DiscardHandler)
	h := export.NewHandler(collection.NewStore(pool), valuation.NewStore(pool), 90*24*time.Hour, logger)
	srv := httptest.NewServer(server.New(server.Config{}, logger, server.Deps{DB: pool, API: []server.Mounter{h}}))
	t.Cleanup(srv.Close)
	return pool, srv.URL
}

func exec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

// seed adds a film named like a formula with an owned DVD and a wished
// Blu-ray, a book owned with buy-back and used prices, a sold album and a
// title without copies.
func seed(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	exec(t, pool, `INSERT INTO title (id, kind, name, original_title, year) OVERRIDING SYSTEM VALUE
		VALUES (1, 'movie', '=HYPERLINK("x")', 'Dune', 2021)`)
	exec(t, pool, `INSERT INTO title (id, kind, name, authors) OVERRIDING SYSTEM VALUE
		VALUES (2, 'book', 'Katedra', '{"Jacek Dukaj"}'), (3, 'music', 'Album', '{"A","B"}'),
		       (4, 'movie', 'Solaris', '{}')`)
	exec(t, pool, `INSERT INTO edition (id, title_id, barcode, format, label, publisher) OVERRIDING SYSTEM VALUE
		VALUES (10, 1, '5902600069706', 'dvd', 'Wydanie kolekcjonerskie', 'Galapagos'),
		       (11, 1, NULL, 'bluray', '', ''), (30, 3, NULL, 'vinyl', '', '')`)
	exec(t, pool, `INSERT INTO edition (id, title_id, barcode, binding) OVERRIDING SYSTEM VALUE
		VALUES (20, 2, '9788308073209', 'paperback')`)
	exec(t, pool, `INSERT INTO copy (id, edition_id, status, price_cents, purchased_on, shop, acquired_as,
			condition, notes, location) OVERRIDING SYSTEM VALUE
		VALUES (100, 10, 'owned', 4999, '2026-01-02', 'Empik', 'used', 'very_good', $1, 'Regał 2')`, trickyNote)
	exec(t, pool, `INSERT INTO copy (id, edition_id, status, max_price_cents, max_market_percent)
		OVERRIDING SYSTEM VALUE VALUES (101, 11, 'wishlist', 3000, 70)`)
	exec(t, pool, `INSERT INTO copy (id, edition_id, status, price_cents) OVERRIDING SYSTEM VALUE
		VALUES (200, 20, 'owned', 3000)`)
	exec(t, pool, `INSERT INTO copy (id, edition_id, status, price_cents, sold_on, sale_price_cents)
		OVERRIDING SYSTEM VALUE VALUES (300, 30, 'sold', 5000, '2026-03-04', 7550)`)
	exec(t, pool, `INSERT INTO price_observation
			(source, external_id, price_cents, edition_id, acquired_as, observed_at, last_seen_at)
		VALUES ('skupszop', '9788308073209', 537, 20, NULL, now(), now()),
		       ('olx', 'k1', 2000, 20, 'used', now(), now()),
		       ('olx', 'k2', 2600, 20, 'used', now(), now()),
		       ('olx', 'k3', 3000, 20, 'used', now(), now())`)
}

func get(t *testing.T, url string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, http.NoBody)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return resp, body
}

var fileName = regexp.MustCompile(`^attachment; filename="ohara-\d{4}-\d{2}-\d{2}\.(csv|json)"$`)

// readCSV checks the file's form and returns its rows, header first.
func readCSV(t *testing.T, url string) [][]string {
	t.Helper()
	resp, body := get(t, url+"/api/export?format=csv")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/csv; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := resp.Header.Get("Content-Disposition"); !fileName.MatchString(got) || got[len(got)-5:] != `.csv"` {
		t.Errorf("Content-Disposition = %q", got)
	}
	rest, ok := bytes.CutPrefix(body, []byte("\xEF\xBB\xBF"))
	if !ok {
		t.Fatalf("body does not start with a UTF-8 BOM: %q", body[:min(len(body), 10)])
	}
	r := csv.NewReader(bytes.NewReader(rest))
	r.Comma = ';'
	rows, err := r.ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	return rows
}

func TestExportCSV(t *testing.T) {
	pool, url := newServer(t)
	seed(t, pool)
	rows := readCSV(t, url)
	header := rows[0]
	col := func(name string) int {
		i := slices.Index(header, name)
		if i < 0 {
			t.Fatalf("no column %q in %q", name, header)
		}
		return i
	}
	if len(rows) != 6 {
		t.Fatalf("rows = %d, want a header, 4 copies and 1 title without copies: %q", len(rows), rows)
	}
	byTitle := map[string][][]string{}
	for _, row := range rows[1:] {
		if len(row) != len(header) {
			t.Fatalf("row has %d cells, header %d: %q", len(row), len(header), row)
		}
		byTitle[row[col("Tytuł")]] = append(byTitle[row[col("Tytuł")]], row)
	}

	film := byTitle[`'=HYPERLINK("x")`]
	if len(film) != 2 {
		t.Fatalf("film rows = %q, want its name with a leading ' twice", film)
	}
	owned, wished := film[0], film[1]
	if owned[col("Status")] != "Na półce" {
		owned, wished = wished, owned
	}
	for name, want := range map[string]string{
		"Rodzaj": "Film", "Tytuł oryginalny": "Dune", "Rok": "2021", "Nośnik lub oprawa": "DVD",
		"Wydanie": "Wydanie kolekcjonerskie", "Wydawca": "Galapagos", "Kod kreskowy": "5902600069706",
		"Status": "Na półce", "Cena zakupu (zł)": "49,99", "Data zakupu": "2026-01-02", "Sklep": "Empik",
		"Nowy czy używany": "Używany", "Stan": "Bardzo dobry", "Miejsce": "Regał 2", "Notatki": trickyNote,
		"Cena maksymalna (zł)": "", "Wartość: skup (zł)": "", "Wartość: używany (zł)": "",
	} {
		if got := owned[col(name)]; got != want {
			t.Errorf("owned film %s = %q, want %q", name, got, want)
		}
	}
	for name, want := range map[string]string{
		"Status": "Lista życzeń", "Nośnik lub oprawa": "Blu-ray", "Cena maksymalna (zł)": "30,00",
		"Maks. % ceny rynkowej": "70", "Cena zakupu (zł)": "", "Data zakupu": "", "Kod kreskowy": "",
	} {
		if got := wished[col(name)]; got != want {
			t.Errorf("wished film %s = %q, want %q", name, got, want)
		}
	}

	book := byTitle["Katedra"]
	if len(book) != 1 {
		t.Fatalf("book rows = %q", book)
	}
	for name, want := range map[string]string{
		"Rodzaj": "Książka", "Twórcy": "Jacek Dukaj", "Nośnik lub oprawa": "Miękka oprawa",
		"Cena zakupu (zł)": "30,00", "Wartość: skup (zł)": "5,37", "Źródło: skup": "SkupSzop",
		"Wartość: używany (zł)": "26,00", "Źródło: używany": "OLX", "Wartość: nowy (zł)": "", "Źródło: nowy": "",
	} {
		if got := book[0][col(name)]; got != want {
			t.Errorf("book %s = %q, want %q", name, got, want)
		}
	}

	album := byTitle["Album"]
	if len(album) != 1 {
		t.Fatalf("album rows = %q", album)
	}
	for name, want := range map[string]string{
		"Rodzaj": "Muzyka", "Twórcy": "A, B", "Nośnik lub oprawa": "Winyl", "Status": "Sprzedany",
		"Data sprzedaży": "2026-03-04", "Cena sprzedaży (zł)": "75,50", "Wartość: używany (zł)": "",
	} {
		if got := album[0][col(name)]; got != want {
			t.Errorf("album %s = %q, want %q", name, got, want)
		}
	}

	bare := byTitle["Solaris"]
	if len(bare) != 1 {
		t.Fatalf("title without copies rows = %q", bare)
	}
	for i, cell := range bare[0] {
		if i > col("Rok") && cell != "" {
			t.Errorf("title without copies has %s = %q, want blank", header[i], cell)
		}
	}
}

func TestExportJSON(t *testing.T) {
	pool, url := newServer(t)
	seed(t, pool)
	resp, body := get(t, url+"/api/export?format=json")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Content-Disposition"); !fileName.MatchString(got) || got[len(got)-6:] != `.json"` {
		t.Errorf("Content-Disposition = %q", got)
	}
	var titles []export.Title
	if err := json.Unmarshal(body, &titles); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var editions, copies int
	var film *export.Copy
	var book *export.Copy
	for i := range titles {
		editions += len(titles[i].Editions)
		for j := range titles[i].Editions {
			for k := range titles[i].Editions[j].Copies {
				c := &titles[i].Editions[j].Copies[k]
				copies++
				switch c.ID {
				case 100:
					film = c
				case 200:
					book = c
				}
			}
		}
	}
	if len(titles) != 4 || editions != 4 || copies != 4 {
		t.Fatalf("titles, editions, copies = %d, %d, %d, want 4, 4, 4", len(titles), editions, copies)
	}
	if film == nil || book == nil {
		t.Fatalf("copies 100 and 200 missing from %s", body)
	}
	if film.EditionID != 10 || film.Status != "owned" || film.PriceCents == nil || *film.PriceCents != 4999 ||
		film.PurchasedOn == nil || film.PurchasedOn.Format("2006-01-02") != "2026-01-02" ||
		film.Shop != "Empik" || film.Notes != trickyNote || film.Location == nil || *film.Location != "Regał 2" ||
		film.Floor != nil || film.Mid != nil || film.Ceiling != nil {
		t.Errorf("film copy = %+v", film)
	}
	if book.Floor == nil || book.Floor.Cents != 537 || book.Floor.Source != "skupszop" ||
		book.Mid == nil || book.Mid.Cents != 2600 || book.Mid.Source != "olx" || book.Ceiling != nil {
		t.Errorf("book copy values = %+v, %+v, %+v", book.Floor, book.Mid, book.Ceiling)
	}
	var raw []map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("decode raw: %v", err)
	}
	names := []any{}
	for _, title := range raw {
		names = append(names, title["name"])
	}
	if !slices.Contains(names, any(`=HYPERLINK("x")`)) {
		t.Errorf("JSON names = %v, want the formula-like one as it is", names)
	}
}

func TestExportEmpty(t *testing.T) {
	_, url := newServer(t)
	rows := readCSV(t, url)
	if len(rows) != 1 || rows[0][0] != "Rodzaj" {
		t.Fatalf("empty CSV = %q, want only the header", rows)
	}
	resp, body := get(t, url+"/api/export?format=json")
	if resp.StatusCode != http.StatusOK || string(bytes.TrimSpace(body)) != "[]" {
		t.Fatalf("empty JSON = %d %s, want 200 []", resp.StatusCode, body)
	}
}

func TestExportFormat(t *testing.T) {
	_, url := newServer(t)
	for _, query := range []string{"", "?format=", "?format=xlsx", "?format=CSV"} {
		resp, body := get(t, url+"/api/export"+query)
		var got struct {
			Field string `json:"field"`
		}
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("decode %s: %v", body, err)
		}
		if resp.StatusCode != http.StatusUnprocessableEntity || got.Field != "format" {
			t.Errorf("%q: %d %s, want 422 on format", query, resp.StatusCode, body)
		}
	}
}
