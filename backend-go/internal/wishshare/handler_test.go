package wishshare_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Automaat/ohara/backend-go/internal/dbtest"
	"github.com/Automaat/ohara/backend-go/internal/server"
	"github.com/Automaat/ohara/backend-go/internal/wishshare"
)

type answer struct {
	code int
	ct   string
	csp  string
	body string
}

func do(t *testing.T, method, url string, header http.Header) answer {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, url, http.NoBody)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	maps.Copy(req.Header, header)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return answer{resp.StatusCode, resp.Header.Get("Content-Type"), resp.Header.Get("Content-Security-Policy"), string(raw)}
}

// owner is the header Traefik copies from Authelia's answer.
var owner = http.Header{"Remote-User": {"owner"}}

func newServer(t *testing.T, pool *pgxpool.Pool) *httptest.Server {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	h := wishshare.NewHandler(wishshare.NewStore(pool), logger)
	srv := httptest.NewServer(server.New(server.Config{RequireRemoteUser: true}, logger,
		server.Deps{DB: pool, API: []server.Mounter{h}, Share: h.Public()}))
	t.Cleanup(srv.Close)
	return srv
}

func createLink(t *testing.T, base string) string {
	t.Helper()
	a := do(t, http.MethodPost, base+"/api/wishlist/share", owner)
	if a.code != http.StatusCreated {
		t.Fatalf("POST share = %d %s", a.code, a.body)
	}
	var link wishshare.ShareLink
	if err := json.Unmarshal([]byte(a.body), &link); err != nil {
		t.Fatalf("decode %q: %v", a.body, err)
	}
	if link.CreatedAt.IsZero() {
		t.Errorf("link %+v has no creation time", link)
	}
	token, ok := strings.CutPrefix(link.URL, "/share/")
	if !ok {
		t.Fatalf("link URL %q is not under /share/", link.URL)
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw)*8 < 128 {
		t.Fatalf("token %q decodes to %d bits (%v), want at least 128", token, len(raw)*8, err)
	}
	return link.URL
}

func TestShareLinkLifecycle(t *testing.T) {
	srv := newServer(t, dbtest.Migrated(t))
	api := srv.URL + "/api/wishlist/share"

	if a := do(t, http.MethodGet, api, owner); a.code != http.StatusNotFound {
		t.Fatalf("GET share before any = %d %s", a.code, a.body)
	}
	first := createLink(t, srv.URL)
	for _, p := range []string{first, first + "/wishes"} {
		if a := do(t, http.MethodGet, srv.URL+p, nil); a.code != http.StatusOK {
			t.Fatalf("GET %s = %d %s", p, a.code, a.body)
		}
	}
	var shown wishshare.ShareLink
	if a := do(t, http.MethodGet, api, owner); a.code != http.StatusOK ||
		json.Unmarshal([]byte(a.body), &shown) != nil || shown.URL != first {
		t.Fatalf("GET share = %d %s, want %s", a.code, a.body, first)
	}

	second := createLink(t, srv.URL)
	if second == first {
		t.Fatal("creating again kept the token")
	}
	if a := do(t, http.MethodGet, srv.URL+first, nil); a.code != http.StatusNotFound {
		t.Errorf("replaced link = %d, want 404", a.code)
	}
	if a := do(t, http.MethodGet, srv.URL+second+"/wishes", nil); a.code != http.StatusOK {
		t.Errorf("new link = %d %s", a.code, a.body)
	}

	if a := do(t, http.MethodDelete, api, owner); a.code != http.StatusNoContent {
		t.Fatalf("DELETE share = %d %s", a.code, a.body)
	}
	for _, p := range []string{second, second + "/wishes"} {
		if a := do(t, http.MethodGet, srv.URL+p, nil); a.code != http.StatusNotFound {
			t.Errorf("revoked %s = %d, want 404", p, a.code)
		}
	}
	if a := do(t, http.MethodGet, api, owner); a.code != http.StatusNotFound {
		t.Errorf("GET share after DELETE = %d", a.code)
	}
	if a := do(t, http.MethodDelete, api, owner); a.code != http.StatusNoContent {
		t.Errorf("second DELETE = %d", a.code)
	}
}

func insert(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(context.Background(), sql+" RETURNING id", args...).Scan(&id); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return id
}

// seed adds a book, a film, a TV season and an owned film: every wish has
// prices, notes and other data the share must leave out.
func seed(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	book := insert(t, pool, `INSERT INTO title (kind, name, authors, year, cover_url, notes)
		VALUES ('book', 'Solaris', '{Stanisław Lem}', 1961, 'https://covers.example/solaris.jpg', 'tajne')`)
	bookEd := insert(t, pool, `INSERT INTO edition (title_id, binding, notes, barcode)
		VALUES ($1, 'hardcover', 'tajne', '9788308049386')`, book)
	insert(t, pool, `INSERT INTO copy (edition_id, status, notes, max_price_cents)
		VALUES ($1, 'wishlist', 'tajne', 4500)`, bookEd)

	film := insert(t, pool, `INSERT INTO title (kind, name, year) VALUES ('movie', 'Alien <script>', 1979)`)
	filmEd := insert(t, pool, `INSERT INTO edition (title_id, format, cover_photo)
		VALUES ($1, 'bluray', '\xffd8ff')`, film)
	insert(t, pool, `INSERT INTO copy (edition_id, status) VALUES ($1, 'wishlist')`, filmEd)

	show := insert(t, pool, `INSERT INTO title (kind, name, season) VALUES ('tv', 'Dark', 2)`)
	showEd := insert(t, pool, `INSERT INTO edition (title_id, format, cover_url)
		VALUES ($1, 'dvd', 'https://covers.example/dark.jpg')`, show)
	insert(t, pool, `INSERT INTO copy (edition_id, status) VALUES ($1, 'wishlist')`, showEd)

	owned := insert(t, pool, `INSERT INTO title (kind, name) VALUES ('movie', 'Na półce')`)
	ownedEd := insert(t, pool, `INSERT INTO edition (title_id, format) VALUES ($1, 'dvd')`, owned)
	insert(t, pool, `INSERT INTO copy (edition_id, status, price_cents) VALUES ($1, 'owned', 2000)`, ownedEd)
}

func canonical(t *testing.T, s string) string {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("decode %q: %v", s, err)
	}
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return string(out)
}

func TestSharedWishesCarryOnlyGiftFields(t *testing.T) {
	pool := dbtest.Migrated(t)
	seed(t, pool)
	srv := newServer(t, pool)
	link := createLink(t, srv.URL)

	a := do(t, http.MethodGet, srv.URL+link+"/wishes", nil)
	if a.code != http.StatusOK || a.ct != "application/json" {
		t.Fatalf("GET wishes = %d %s %s", a.code, a.ct, a.body)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(a.body), &top); err != nil {
		t.Fatalf("decode %q: %v", a.body, err)
	}
	if keys := slices.Sorted(maps.Keys(top)); !slices.Equal(keys, []string{"wishes"}) {
		t.Fatalf("top-level keys = %v", keys)
	}
	var items []map[string]any
	if err := json.Unmarshal(top["wishes"], &items); err != nil {
		t.Fatalf("decode wishes: %v", err)
	}
	wantKeys := []string{"binding", "cover_url", "creators", "format", "kind", "title", "year"}
	for _, it := range items {
		if keys := slices.Sorted(maps.Keys(it)); !slices.Equal(keys, wantKeys) {
			t.Errorf("wish keys = %v, want %v", keys, wantKeys)
		}
	}
	want := `{"wishes":[` +
		`{"title":"Alien <script>","creators":[],"year":1979,"kind":"movie","format":"bluray","binding":null,"cover_url":null},` +
		`{"title":"Dark, sezon 2","creators":[],"year":null,"kind":"tv","format":"dvd","binding":null,"cover_url":"https://covers.example/dark.jpg"},` +
		`{"title":"Solaris","creators":["Stanisław Lem"],"year":1961,"kind":"book","format":null,"binding":"hardcover","cover_url":"https://covers.example/solaris.jpg"}]}`
	if got, exp := canonical(t, a.body), canonical(t, want); got != exp {
		t.Errorf("wishes =\n%s\nwant\n%s", got, exp)
	}
	for _, leak := range []string{"tajne", "4500", "Na półce", "/api/", "9788308049386"} {
		if strings.Contains(a.body, leak) {
			t.Errorf("wishes carry %q: %s", leak, a.body)
		}
	}

	page := do(t, http.MethodGet, srv.URL+link, nil)
	if page.code != http.StatusOK || page.ct != "text/html; charset=utf-8" {
		t.Fatalf("GET page = %d %s", page.code, page.ct)
	}
	if !strings.Contains(page.csp, "default-src 'none'") || strings.Contains(page.csp, "script-src") {
		t.Errorf("page CSP = %q", page.csp)
	}
	for _, s := range []string{
		"<title>Lista życzeń</title>", "Alien &lt;script&gt;", "Dark, sezon 2",
		"Stanisław Lem", "Książka · 1961 · Twarda oprawa", "Film · 1979 · Blu-ray", "https://covers.example/dark.jpg",
	} {
		if !strings.Contains(page.body, s) {
			t.Errorf("page lacks %q", s)
		}
	}
	for _, leak := range []string{"tajne", "4500", "45 zł", "45,00", "Na półce", "/api/", "<script>", "Lista życzeń jest pusta"} {
		if strings.Contains(page.body, leak) {
			t.Errorf("page carries %q", leak)
		}
	}
}

func TestSharedPageSaysWhenEmpty(t *testing.T) {
	srv := newServer(t, dbtest.Migrated(t))
	link := createLink(t, srv.URL)
	page := do(t, http.MethodGet, srv.URL+link, nil)
	if page.code != http.StatusOK || !strings.Contains(page.body, "Lista życzeń jest pusta.") ||
		strings.Contains(page.body, "<ul") {
		t.Errorf("empty page = %d %s", page.code, page.body)
	}
	if a := do(t, http.MethodGet, srv.URL+link+"/wishes", nil); a.body != `{"wishes":[]}`+"\n" {
		t.Errorf("empty wishes = %q", a.body)
	}
}

func TestUnknownTokensAnswerTheAPIsNotFound(t *testing.T) {
	srv := newServer(t, dbtest.Migrated(t))
	link := createLink(t, srv.URL)
	token := strings.TrimPrefix(link, "/share/")
	random := strings.Repeat("A", len(token))

	ref := do(t, http.MethodGet, srv.URL+"/api/no-such-path-q8v", owner)
	if ref.code != http.StatusNotFound {
		t.Fatalf("unknown API path = %d", ref.code)
	}
	for _, p := range []string{
		"/share/" + random,
		"/share/" + random + "/wishes",
		"/share/" + token[:len(token)-1],
		"/share/" + token + "x",
		"/share/" + strings.ToLower(token),
		"/share/",
		"/share/" + token + "/wishes/1",
		"/share/" + token + "/other",
	} {
		a := do(t, http.MethodGet, srv.URL+p, nil)
		if a.code != ref.code || a.body != ref.body || a.ct != ref.ct {
			t.Errorf("GET %s = %d %s %q, want the API's %d %s %q", p, a.code, a.ct, a.body, ref.code, ref.ct, ref.body)
		}
	}
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		a := do(t, m, srv.URL+link, owner)
		if a.code != ref.code || a.body != ref.body {
			t.Errorf("%s %s with Remote-User = %d %q, want the 404", m, link, a.code, a.body)
		}
	}
}

func TestOnlyShareReadsSkipRemoteUser(t *testing.T) {
	pool := dbtest.Migrated(t)
	seed(t, pool)
	srv := newServer(t, pool)
	link := createLink(t, srv.URL)

	for _, tt := range []struct {
		method, path string
		code         int
	}{
		{http.MethodGet, link, http.StatusOK},
		{http.MethodGet, link + "/wishes", http.StatusOK},
		{http.MethodGet, "/api/wishlist/share", http.StatusUnauthorized},
		{http.MethodPost, "/api/wishlist/share", http.StatusUnauthorized},
		{http.MethodDelete, "/api/wishlist/share", http.StatusUnauthorized},
		{http.MethodPost, link, http.StatusUnauthorized},
		{http.MethodHead, link, http.StatusUnauthorized},
		{http.MethodGet, "/share", http.StatusUnauthorized},
		{http.MethodGet, "/%73hare/" + strings.TrimPrefix(link, "/share/"), http.StatusUnauthorized},
		{http.MethodGet, "/api/collection", http.StatusUnauthorized},
		{http.MethodGet, "/api/editions/1/cover", http.StatusUnauthorized},
	} {
		if a := do(t, tt.method, srv.URL+tt.path, nil); a.code != tt.code {
			t.Errorf("%s %s without Remote-User = %d, want %d", tt.method, tt.path, a.code, tt.code)
		}
	}
}

func TestSharedWishesLeaveOutCoversOnTheAppsHost(t *testing.T) {
	pool := dbtest.Migrated(t)
	srv := newServer(t, pool)
	host := strings.TrimPrefix(srv.URL, "http://")
	hostname := host[:strings.LastIndex(host, ":")]
	for i, cover := range []string{
		srv.URL + "/api/editions/1/cover",
		"https://" + strings.ToUpper(hostname) + "/pwa-192x192.png",
		"https://covers.example/kept.jpg",
	} {
		title := insert(t, pool, `INSERT INTO title (kind, name, cover_url) VALUES ('movie', $1, $2)`,
			"Film "+string(rune('A'+i)), cover)
		ed := insert(t, pool, `INSERT INTO edition (title_id, format) VALUES ($1, 'dvd')`, title)
		insert(t, pool, `INSERT INTO copy (edition_id, status) VALUES ($1, 'wishlist')`, ed)
	}
	link := createLink(t, srv.URL)

	var got wishshare.SharedWishlist
	a := do(t, http.MethodGet, srv.URL+link+"/wishes", nil)
	if err := json.Unmarshal([]byte(a.body), &got); err != nil || len(got.Wishes) != 3 {
		t.Fatalf("GET wishes = %d %s (%v)", a.code, a.body, err)
	}
	for i, want := range []string{"", "", "https://covers.example/kept.jpg"} {
		c := got.Wishes[i].CoverURL
		if (want == "") != (c == nil) || (c != nil && *c != want) {
			t.Errorf("wish %d cover = %v, want %q", i, c, want)
		}
	}
	page := do(t, http.MethodGet, srv.URL+link, nil)
	if strings.Contains(page.body, hostname) || strings.Contains(strings.ToLower(page.body), strings.ToLower(hostname)+"/") {
		t.Errorf("page points at the app's own host:\n%s", page.body)
	}
}
