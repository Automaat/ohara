package movie

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func serve(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	return s
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCeneo(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   []byte
		want   []string
		err    bool
	}{
		{
			name: "recorded search page keeps film results", status: 200, body: fixture(t, "ceneo-search-incepcja.html"),
			want: []string{"Incepcja [Blu-Ray]", "Incepcja [Blu-Ray 4K]+[Blu-Ray]", "Incepcja (2xBlu-ray)"},
		},
		{name: "recorded no-match page", status: 200, body: fixture(t, "ceneo-no-match.html"), want: []string{}},
		{name: "unknown page is a parse error", status: 200, body: []byte("<html>captcha</html>"), err: true},
		{name: "blocked", status: 403, body: []byte("denied"), err: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var path string
			s := serve(t, func(w http.ResponseWriter, r *http.Request) {
				path = r.URL.EscapedPath()
				if r.Header.Get("User-Agent") != browserUA {
					t.Errorf("user agent %q", r.Header.Get("User-Agent"))
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write(tc.body)
			})
			got, err := Ceneo{Client: s.Client(), BaseURL: s.URL}.Products(context.Background(), "5906190326195")
			if path != "/;szukaj-5906190326195" {
				t.Errorf("path = %q", path)
			}
			if tc.err {
				if err == nil {
					t.Fatalf("want error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			names := []string{}
			for _, p := range got {
				names = append(names, p.Name)
				if p.Source != "ceneo" || !strings.HasPrefix(p.Category, "/Filmy") {
					t.Errorf("product = %+v", p)
				}
			}
			if strings.Join(names, "|") != strings.Join(tc.want, "|") {
				t.Fatalf("names = %q, want %q", names, tc.want)
			}
		})
	}
}

func TestCeneoSkipsOtherCategoriesAndCaps(t *testing.T) {
	var page strings.Builder
	page.WriteString(`<div class="js_search-results">`)
	page.WriteString(`<div class="cat-prod-row" data-productName="Odtwarzacz DVD" data-GACategoryName="/Sprzęt RTV/DVD">`)
	for _, n := range []string{"A &amp; B [DVD]", "A &amp; B [DVD]", "C", "D", "E"} {
		page.WriteString(`<div class="cat-prod-row x" data-productName="` + n + `" data-GACategoryName="/Filmy/Filmy DVD">`)
	}
	got, err := parseCeneo(page.String())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != ceneoMaxResults || got[0].Name != "A & B [DVD]" || got[1].Name != "C" {
		t.Fatalf("products = %+v", got)
	}
}

func TestUPCitemdb(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   []byte
		want   int
		err    error
		anyErr bool
	}{
		{name: "recorded match", status: 200, body: fixture(t, "upcitemdb-5902600069706.json"), want: 1},
		{name: "recorded empty", status: 200, body: fixture(t, "upcitemdb-empty.json"), want: 0},
		{name: "invalid code", status: 400, body: []byte(`{"code":"INVALID_UPC","message":"x"}`), want: 0},
		{name: "quota", status: 429, body: []byte(`{"code":"TOO_FAST"}`), err: ErrQuotaExceeded},
		{name: "other 400", status: 400, body: []byte(`{"code":"EXCEED_LIMIT"}`), anyErr: true},
		{name: "garbage", status: 200, body: []byte(`<html>`), anyErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := serve(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/prod/trial/lookup" || r.URL.Query().Get("upc") != "5902600069706" {
					t.Errorf("url = %s", r.URL)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write(tc.body)
			})
			got, err := UPCitemdb{Client: s.Client(), BaseURL: s.URL}.Products(context.Background(), "5902600069706")
			switch {
			case tc.err != nil:
				if !errors.Is(err, tc.err) {
					t.Fatalf("err = %v, want %v", err, tc.err)
				}
			case tc.anyErr:
				if err == nil {
					t.Fatal("want error")
				}
			case err != nil:
				t.Fatal(err)
			case len(got) != tc.want:
				t.Fatalf("products = %+v", got)
			}
		})
	}
}

func TestTMDBAuth(t *testing.T) {
	for _, key := range []string{"0123456789abcdef", "eyJhbGciOiJIUzI1NiJ9.e30.sig"} {
		var auth, apiKey, year string
		s := serve(t, func(w http.ResponseWriter, r *http.Request) {
			auth, apiKey, year = r.Header.Get("Authorization"), r.URL.Query().Get("api_key"), r.URL.Query().Get("year")
			_, _ = w.Write([]byte(`{"results":[{"id":603,"title":"Matrix","original_title":"The Matrix","release_date":"1999-03-31"}]}`))
		})
		res, err := TMDB{Client: s.Client(), BaseURL: s.URL, Key: key}.Search(context.Background(), "Matrix", new(1999))
		if err != nil || len(res) != 1 || !eqPtr(res[0].Year(), new(1999)) || year != "1999" {
			t.Fatalf("res = %+v err %v year %q", res, err, year)
		}
		bearer := strings.HasPrefix(key, "eyJ")
		if bearer != (auth == "Bearer "+key) || bearer == (apiKey == key) {
			t.Fatalf("key %q sent as auth %q api_key %q", key, auth, apiKey)
		}
	}
}

func TestTMDBErrorsHideTheKey(t *testing.T) {
	s := serve(t, func(w http.ResponseWriter, _ *http.Request) {})
	s.Close()
	_, err := TMDB{Client: s.Client(), BaseURL: s.URL, Key: "secret-key"}.Search(context.Background(), "x", nil)
	if err == nil || strings.Contains(err.Error(), "secret-key") {
		t.Fatalf("err = %v", err)
	}
}

func TestTMDBRejectsBodyWithoutResults(t *testing.T) {
	s := serve(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"status_code":34}`)) })
	if _, err := (TMDB{Client: s.Client(), BaseURL: s.URL, Key: "k"}).Search(context.Background(), "x", nil); err == nil {
		t.Fatal("want error")
	}
}

func TestTMDBYearWithoutDate(t *testing.T) {
	if (TMDBMovie{}).Year() != nil || (TMDBMovie{ReleaseDate: "abcd-01-01"}).Year() != nil {
		t.Fatal("year from a missing date")
	}
}

type memStore struct {
	mu    sync.Mutex
	token *Token
	saves int
}

func (m *memStore) Load(context.Context, string) (Token, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.token == nil {
		return Token{}, false, nil
	}
	return *m.token, true, nil
}

func (m *memStore) Save(_ context.Context, _ string, t Token) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.token = &t
	m.saves++
	return nil
}

// allegroServer accepts access token "access-<n>" for the latest n and
// rotates the refresh token on each refresh.
type allegroServer struct {
	mu        sync.Mutex
	gen       int
	refreshes int
	catalog   int
	forbid    bool
	device    []string
}

func (a *allegroServer) handler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		q := r.URL.Query()
		id, secret, ok := r.BasicAuth()
		switch r.URL.Path {
		case "/auth/oauth/token":
			if !ok || id != "id" || secret != "secret" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
				return
			}
			switch q.Get("grant_type") {
			case "refresh_token":
				if q.Get("refresh_token") != refreshFor(a.gen) {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
					return
				}
			case "urn:ietf:params:oauth:grant-type:device_code":
				if q.Get("device_code") != "dev-1" {
					t.Errorf("device code %q", q.Get("device_code"))
				}
				if len(a.device) > 0 {
					step := a.device[0]
					a.device = a.device[1:]
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"error":"` + step + `"}`))
					return
				}
			default:
				t.Errorf("grant %q", q.Get("grant_type"))
			}
			a.gen++
			a.refreshes++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": accessFor(a.gen), "refresh_token": refreshFor(a.gen), "expires_in": 43199,
			})
		case "/auth/oauth/device":
			if q.Get("client_id") != "id" || !ok {
				t.Errorf("device request %s", r.URL)
			}
			_, _ = w.Write([]byte(`{"device_code":"dev-1","user_code":"ABC123","verification_uri":"https://allegro.pl/skojarz-aplikacje","verification_uri_complete":"https://allegro.pl/skojarz-aplikacje?code=ABC123","expires_in":60,"interval":0}`))
		case "/sale/products":
			a.catalog++
			if r.Header.Get("Authorization") != "Bearer "+accessFor(a.gen) {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if a.forbid {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			if q.Get("phrase") != "5906190326195" || q.Get("mode") != "GTIN" || r.Header.Get("Accept") != "application/vnd.allegro.public.v1+json" {
				t.Errorf("catalog request %s", r.URL)
			}
			_, _ = w.Write([]byte(`{"products":[{"id":"1","name":"Kursk [Blu-ray]","category":{"id":"89100"}},{"id":"2","name":" "}]}`))
		default:
			t.Errorf("unexpected %s", r.URL)
		}
	}
}

func accessFor(gen int) string  { return "access-" + string(rune('0'+gen)) }
func refreshFor(gen int) string { return "refresh-" + string(rune('0'+gen)) }

func newAllegro(t *testing.T, srv *allegroServer, store *memStore, now time.Time) *Allegro {
	t.Helper()
	s := serve(t, srv.handler(t))
	return &Allegro{
		ClientID: "id", ClientSecret: "secret", Store: store, Client: s.Client(), APIBase: s.URL, AuthBase: s.URL,
		Now: func() time.Time { return now },
	}
}

func TestAllegroRefreshesAnExpiredToken(t *testing.T) {
	now := time.Now()
	store := &memStore{token: &Token{Access: accessFor(0), Refresh: refreshFor(0), Expiry: now.Add(30 * time.Second)}}
	srv := &allegroServer{}
	a := newAllegro(t, srv, store, now)
	got, err := a.Products(context.Background(), "5906190326195")
	if err != nil || len(got) != 1 || got[0].Name != "Kursk [Blu-ray]" || got[0].Category != "89100" {
		t.Fatalf("products = %+v err %v", got, err)
	}
	if srv.refreshes != 1 || store.saves != 1 || store.token.Refresh != refreshFor(1) {
		t.Fatalf("refreshes %d saves %d token %+v", srv.refreshes, store.saves, store.token)
	}
	if _, err := a.Products(context.Background(), "5906190326195"); err != nil || srv.refreshes != 1 {
		t.Fatalf("second lookup refreshed again: %d, %v", srv.refreshes, err)
	}
}

func TestAllegroRetriesOnceOnRejectedToken(t *testing.T) {
	now := time.Now()
	store := &memStore{token: &Token{Access: "revoked", Refresh: refreshFor(0), Expiry: now.Add(time.Hour)}}
	srv := &allegroServer{}
	a := newAllegro(t, srv, store, now)
	if _, err := a.Products(context.Background(), "5906190326195"); err != nil {
		t.Fatal(err)
	}
	if srv.catalog != 2 || srv.refreshes != 1 {
		t.Fatalf("catalog %d refreshes %d", srv.catalog, srv.refreshes)
	}
}

func TestAllegroErrors(t *testing.T) {
	now := time.Now()
	t.Run("no stored token", func(t *testing.T) {
		a := newAllegro(t, &allegroServer{}, &memStore{}, now)
		if _, err := a.Products(context.Background(), "5906190326195"); !errors.Is(err, ErrAllegroNotAuthorized) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("refresh token revoked", func(t *testing.T) {
		store := &memStore{token: &Token{Access: "a", Refresh: "stale", Expiry: now}}
		a := newAllegro(t, &allegroServer{}, store, now)
		if _, err := a.Products(context.Background(), "5906190326195"); !errors.Is(err, ErrAllegroNotAuthorized) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("catalog refused", func(t *testing.T) {
		store := &memStore{token: &Token{Access: accessFor(0), Refresh: refreshFor(0), Expiry: now.Add(time.Hour)}}
		a := newAllegro(t, &allegroServer{forbid: true}, store, now)
		if _, err := a.Products(context.Background(), "5906190326195"); !errors.Is(err, ErrAllegroRefused) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("token rejected twice", func(t *testing.T) {
		store := &memStore{token: &Token{Access: accessFor(0), Refresh: refreshFor(0), Expiry: now.Add(time.Hour)}}
		srv := &allegroServer{}
		a := newAllegro(t, srv, store, now)
		a.APIBase = serve(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }).URL
		if _, err := a.Products(context.Background(), "5906190326195"); !isStatus(err, http.StatusUnauthorized) {
			t.Fatalf("err = %v", err)
		}
		if srv.refreshes != 1 {
			t.Fatalf("refreshes = %d", srv.refreshes)
		}
	})
}

func TestAllegroLogin(t *testing.T) {
	store := &memStore{}
	srv := &allegroServer{device: []string{"authorization_pending", "authorization_pending"}}
	a := newAllegro(t, srv, store, time.Now())
	var out bytes.Buffer
	if err := a.Login(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "https://allegro.pl/skojarz-aplikacje?code=ABC123") || !strings.Contains(out.String(), "ABC123") {
		t.Fatalf("output = %q", out.String())
	}
	if store.token == nil || store.token.Refresh != refreshFor(1) {
		t.Fatalf("stored token = %+v", store.token)
	}
	if _, err := a.Products(context.Background(), "5906190326195"); err != nil || srv.refreshes != 1 {
		t.Fatalf("lookup after login: %v, refreshes %d", err, srv.refreshes)
	}
}

func TestAllegroLoginDenied(t *testing.T) {
	store := &memStore{}
	a := newAllegro(t, &allegroServer{device: []string{"access_denied"}}, store, time.Now())
	var out bytes.Buffer
	var oe *oauthError
	if err := a.Login(context.Background(), &out); !errors.As(err, &oe) || oe.Code != "access_denied" {
		t.Fatalf("err = %v", err)
	}
	if store.token != nil {
		t.Fatal("token stored after denial")
	}
}

type fakeSource struct {
	name     string
	products []Product
	err      error
}

func (f fakeSource) Name() string { return f.name }

func (f fakeSource) Products(context.Context, string) ([]Product, error) { return f.products, f.err }

type fakeSearcher struct {
	mu      sync.Mutex
	calls   []string
	results map[string][]TMDBMovie
	err     error
}

func (f *fakeSearcher) Search(_ context.Context, q string, year *int) ([]TMDBMovie, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := q
	if year != nil {
		key += "@" + string(rune('0'+*year%10))
	}
	f.calls = append(f.calls, key)
	return f.results[key], f.err
}

func TestFetchErrorPolicy(t *testing.T) {
	down := errors.New("down")
	kursk := []Product{{Source: "a", Name: "Kursk [Blu-ray]"}}
	tests := []struct {
		name    string
		sources []NameSource
		tmdbErr error
		wantErr bool
		names   int
	}{
		{name: "one source down, another found it", sources: []NameSource{fakeSource{name: "a", err: down}, fakeSource{name: "b", products: kursk}}, names: 1},
		{name: "nothing found and a source down", sources: []NameSource{fakeSource{name: "a", err: down}, fakeSource{name: "b"}}, wantErr: true},
		{name: "nothing found anywhere is a cacheable no match", sources: []NameSource{fakeSource{name: "a"}, fakeSource{name: "b"}}},
		{name: "tmdb down", sources: []NameSource{fakeSource{name: "b", products: kursk}}, tmdbErr: down, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := &Provider{Sources: tc.sources, TMDB: &fakeSearcher{err: tc.tmdbErr}}
			raw, err := p.Fetch(context.Background(), "5906190326195")
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, raw %s", raw)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var r Raw
			if err := json.Unmarshal(raw, &r); err != nil || len(r.Products) != tc.names {
				t.Fatalf("raw = %s, %v", raw, err)
			}
		})
	}
}

func TestFetchSearchFallbacks(t *testing.T) {
	s := &fakeSearcher{results: map[string][]TMDBMovie{
		"Władca Pierścieni@1": {{ID: 120, Title: "Władca Pierścieni: Drużyna Pierścienia", OriginalTitle: "The Lord of the Rings: The Fellowship of the Ring", ReleaseDate: "2001-12-18"}},
	}}
	p := &Provider{Sources: []NameSource{fakeSource{name: "a", products: []Product{
		{Name: "Władca Pierścieni: Drużyna Pierścienia (2001) [DVD]"},
		{Name: "Władca pierścieni: drużyna pierścienia DVD"},
		{Name: "The Lord of the Rings (The Fellowship of the Ring) DVD"},
	}}}, TMDB: s}
	raw, err := p.Fetch(context.Background(), "5900000000001")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(s.calls, "|")
	if !strings.HasPrefix(got, "Władca Pierścieni: Drużyna Pierścienia@1|Władca Pierścieni: Drużyna Pierścienia|Władca Pierścieni@1|The Lord of the Rings") {
		t.Fatalf("searches = %s", got)
	}
	cands, err := p.Parse("5900000000001", raw)
	if err != nil || len(cands) != 1 || *cands[0].Title.TMDBID != 120 {
		t.Fatalf("cands = %+v, %v", cands, err)
	}
	if cands[0].Confidence < 0.8 {
		t.Fatalf("full-title match on a later product scored %v", cands[0].Confidence)
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	if _, err := (&Provider{}).Parse("1", json.RawMessage(`[`)); err == nil {
		t.Fatal("want error")
	}
}

func TestScore(t *testing.T) {
	titles := []weighted{{"Kursk", 1}}
	kursk2018 := TMDBMovie{ID: 1, Title: "Kursk", ReleaseDate: "2018-12-14"}
	kursk2012 := TMDBMovie{ID: 2, Title: "Kursk", ReleaseDate: "2012-07-31"}
	both := Search{Results: []TMDBMovie{kursk2018, kursk2012}}
	if c := score(kursk2018, 0, both, titles); c >= 0.8 {
		t.Errorf("ambiguous score %v", c)
	}
	withYear := Search{Year: new(2018), Results: both.Results}
	if c := score(kursk2018, 0, withYear, titles); c < 0.8 {
		t.Errorf("year-confirmed score %v", c)
	}
	if c := score(kursk2012, 1, withYear, titles); c >= 0.8 {
		t.Errorf("year-contradicted score %v", c)
	}
	if c := score(kursk2018, 0, Search{Results: []TMDBMovie{kursk2018}}, []weighted{{"Kursk", shortWeight}}); c >= 0.8 {
		t.Errorf("main-title-only score %v", c)
	}
}
