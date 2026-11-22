package music_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Automaat/ohara/backend-go/internal/discogs"
	"github.com/Automaat/ohara/backend-go/internal/lookup"
	"github.com/Automaat/ohara/backend-go/internal/lookup/movie"
	"github.com/Automaat/ohara/backend-go/internal/lookup/music"
)

// twoPressings has two Discogs releases, so the user picks the edition.
const twoPressings = "5099902987620"

// upcOnly is a UPC-A that Discogs stores with 12 digits.
const upcOnly = "0602537869336"

// mbOnly is unknown to Discogs and known to MusicBrainz.
const mbOnly = "5000000000012"

// discogsDown makes Discogs fail while MusicBrainz knows the release.
const discogsDown = "5900000000022"

// allDown makes every source fail.
const allDown = "5900000000039"

// unknown is known to no source.
const unknown = "5900000000046"

// shopOnly is known only to the shop catalog, which names the product.
const shopOnly = "5900000000053"

// shopDown makes the shop catalog fail for a barcode no source knows.
const shopDown = "5900000000060"

// tacoCD is what a Discogs search for the shop's product name finds: the
// standard pressing has no barcode on Discogs.
var tacoCD = []map[string]any{
	{
		"id": 28352776, "title": "Taco Hemingway - 1-800-OŚWIECENIE", "year": "2023", "country": "Poland",
		"format": []string{"CD", "Album", "Limited Edition"}, "barcode": []string{"602458447675"},
	},
	{
		"id": 28352929, "title": "Taco Hemingway - 1-800-OŚWIECENIE", "year": "2023", "country": "Poland",
		"format": []string{"CD", "Album"},
	},
}

// shopNames answers like movie.Allegro: shopOnly is a listed product,
// shopDown an outage, anything else no product.
type shopNames struct {
	mu    sync.Mutex
	names []string
	calls int
}

func (s *shopNames) Products(_ context.Context, code string) ([]movie.Product, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	switch code {
	case shopOnly:
		out := make([]movie.Product, 0, len(s.names))
		for _, n := range s.names {
			out = append(out, movie.Product{Source: "allegro", Name: n})
		}
		return out, nil
	case shopDown:
		return nil, errors.New("allegro: down")
	}
	return []movie.Product{}, nil
}

// discogsReleases follow the documented /database/search schema, trimmed
// to the fields the provider reads.
var discogsReleases = map[string][]map[string]any{
	twoPressings: {
		{
			"id": 1873013, "master_id": 5460, "title": "Pink Floyd - The Dark Side Of The Moon",
			"year": "2011", "country": "Europe", "format": []string{"CD", "Album", "Reissue", "Remastered"},
			"label": []string{"EMI", "Pink Floyd Records"}, "catno": "50999 028955 2 5",
			"barcode": []string{"5 099902 987620", "LC 0542"}, "cover_image": "https://i.discogs.com/dsotm-2011.jpeg",
		},
		{
			"id": 3122840, "title": "Pink Floyd - The Dark Side Of The Moon", "year": "2011",
			"country": "UK", "format": []string{"CD", "Album", "Reissue", "Remastered", "Digipak"},
			"label": []string{"EMI"}, "catno": "5099902987620", "barcode": []string{"5099902987620"},
			"cover_image": "https://st.discogs.com/images/spacer.gif",
		},
	},
	upcOnly[1:]: {
		{
			"id": 4123561, "title": "Nirvana (2) - Nevermind", "year": "2013", "country": "Europe",
			"format": []string{"Vinyl", "LP", "Album", "Reissue"}, "label": []string{"DGC"},
			"barcode": []string{"6 02537 86933 6"},
		},
	},
}

// mbReleases follow the documented /ws/2/release search schema.
var mbReleases = map[string][]music.MBRelease{
	mbOnly: {{
		ID: "b84ee12a-09ef-421b-82de-0441a926375b", Score: 100, Title: "Kind of Blue", Date: "1997-03-25",
		Country: "XE", Barcode: mbOnly,
		ArtistCredit: []music.MBCredit{{Name: "Miles Davis"}},
		LabelInfo:    []music.MBLabel{{CatalogNumber: "CK 64935"}},
		Media:        []music.MBMedium{{Format: "Enhanced CD"}},
	}},
	discogsDown: {{
		ID: "f5093c06-23e3-404f-aeaa-40f72885ee3a", Score: 100, Title: "Abbey Road", Date: "1969",
		Barcode:      discogsDown,
		ArtistCredit: []music.MBCredit{{Name: "The Beatles"}},
		Media:        []music.MBMedium{{Format: "12\" Vinyl"}},
	}},
}

type upstream struct {
	mu     sync.Mutex
	hits   map[string]int
	server *httptest.Server
}

func newUpstream(t *testing.T) *upstream {
	t.Helper()
	u := &upstream{hits: map[string]int{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /database/search", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("barcode")
		if q := r.URL.Query().Get("q"); q != "" {
			u.hit("discogs q=" + q + " format=" + r.URL.Query().Get("format"))
			if q == "down" {
				http.Error(w, "down", http.StatusInternalServerError)
				return
			}
			if q == "1-800-Oświecenie Taco Hemingway" || q == "Taco Hemingway 1-800-Oświecenie" {
				writeJSON(w, map[string]any{"results": tacoCD})
				return
			}
			writeJSON(w, map[string]any{"results": []any{}})
			return
		}
		u.hit("discogs " + code)
		if r.Header.Get("Authorization") != "Discogs token=secret" || !strings.HasPrefix(r.Header.Get("User-Agent"), "ohara/") {
			http.Error(w, `{"message":"You must authenticate"}`, http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("type") != "release" {
			http.Error(w, "type", http.StatusBadRequest)
			return
		}
		if code == discogsDown || code == allDown {
			http.Error(w, "down", http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"results": orEmpty(discogsReleases[code])})
	})
	mux.HandleFunc("GET /ws/2/release", func(w http.ResponseWriter, r *http.Request) {
		u.hit("musicbrainz")
		q := r.URL.Query().Get("query")
		if r.URL.Query().Get("fmt") != "json" || !strings.Contains(r.Header.Get("User-Agent"), "github.com/Automaat/ohara") {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if strings.Contains(q, allDown) {
			http.Error(w, `{"error":"busy"}`, http.StatusServiceUnavailable)
			return
		}
		code := strings.TrimPrefix(strings.Fields(q)[0], "barcode:")
		writeJSON(w, map[string]any{"releases": orEmpty(mbReleases[code])})
	})
	mux.HandleFunc("GET /release/{mbid}", func(w http.ResponseWriter, r *http.Request) {
		u.hit("coverart")
		if r.PathValue("mbid") != mbReleases[mbOnly][0].ID {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, map[string]any{"images": []map[string]any{
			{"front": false, "image": "http://coverartarchive.org/back.jpg"},
			{"front": true, "image": "http://coverartarchive.org/front.jpg", "thumbnails": map[string]string{"250": "http://coverartarchive.org/front-250.jpg"}},
		}})
	})
	u.server = httptest.NewServer(mux)
	t.Cleanup(u.server.Close)
	return u
}

func orEmpty[T any](v []T) []T {
	if v == nil {
		return []T{}
	}
	return v
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (u *upstream) hit(name string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.hits[name]++
}

func (u *upstream) count(name string) int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.hits[name]
}

func (u *upstream) provider(withDiscogs bool) *music.Provider {
	p := &music.Provider{
		MusicBrainz: music.MusicBrainz{Client: u.server.Client(), BaseURL: u.server.URL},
		Covers:      music.CoverArt{Client: u.server.Client(), BaseURL: u.server.URL},
	}
	if withDiscogs {
		p.Discogs = &discogs.Client{HTTP: u.server.Client(), BaseURL: u.server.URL, Token: "secret"}
	}
	return p
}

func lookupWith(t *testing.T, p lookup.Provider, code string) lookup.Result {
	t.Helper()
	r := &lookup.Registry{}
	if err := r.Register(p, 0); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.DiscardHandler)
	return lookup.NewService(r, nil, logger).Lookup(context.Background(), code)
}

func TestScanResolvesToDiscogsReleases(t *testing.T) {
	u := newUpstream(t)
	res := lookupWith(t, u.provider(true), twoPressings)
	if len(res.Errors) != 0 || len(res.Candidates) != 2 {
		t.Fatalf("result = %+v", res)
	}
	if !res.NeedsConfirmation {
		t.Fatal("two pressings of one barcode must ask the user to pick the edition")
	}
	c := res.Candidates[0]
	if c.Type != "music" || c.Title.Name != "The Dark Side Of The Moon" || len(c.Title.Authors) != 1 || c.Title.Authors[0] != "Pink Floyd" {
		t.Fatalf("title = %+v", c.Title)
	}
	e := c.Edition
	if e.Barcode != twoPressings || e.DiscogsReleaseID == nil || *e.DiscogsReleaseID != 1873013 ||
		e.Format == nil || *e.Format != "cd" || e.Publisher != "EMI" || e.Year == nil || *e.Year != 2011 {
		t.Fatalf("edition = %+v", e)
	}
	if e.Label != "Reissue, Remastered · Europe · 50999 028955 2 5" {
		t.Fatalf("label = %q", e.Label)
	}
	if c.CoverURL != "https://i.discogs.com/dsotm-2011.jpeg" || res.Candidates[1].CoverURL != "" {
		t.Fatalf("covers = %q, %q", c.CoverURL, res.Candidates[1].CoverURL)
	}
	if res.Candidates[1].Edition.Label != "Reissue, Remastered, Digipak · UK · 5099902987620" {
		t.Fatalf("second label = %q", res.Candidates[1].Edition.Label)
	}
	if u.count("musicbrainz") != 0 {
		t.Fatal("MusicBrainz asked although Discogs found the barcode")
	}
}

func TestDiscogsRetriesTheUPCSpelling(t *testing.T) {
	u := newUpstream(t)
	res := lookupWith(t, u.provider(true), upcOnly)
	if len(res.Candidates) != 1 || res.NeedsConfirmation {
		t.Fatalf("result = %+v", res)
	}
	c := res.Candidates[0]
	if c.Title.Authors[0] != "Nirvana" || c.Title.Name != "Nevermind" || *c.Edition.Format != "vinyl" ||
		c.Edition.Barcode != upcOnly || c.Confidence < lookup.ConfirmThreshold {
		t.Fatalf("candidate = %+v", c)
	}
	if u.count("discogs "+upcOnly) != 1 || u.count("discogs "+upcOnly[1:]) != 1 {
		t.Fatalf("discogs hits = %v", u.hits)
	}
}

func TestMusicBrainzAnswersWhatDiscogsLacks(t *testing.T) {
	u := newUpstream(t)
	for _, withDiscogs := range []bool{true, false} {
		res := lookupWith(t, u.provider(withDiscogs), mbOnly)
		if len(res.Errors) != 0 || len(res.Candidates) != 1 || res.NeedsConfirmation {
			t.Fatalf("discogs %v: result = %+v", withDiscogs, res)
		}
		c := res.Candidates[0]
		if c.Title.Name != "Kind of Blue" || c.Title.Authors[0] != "Miles Davis" || *c.Edition.Format != "cd" ||
			*c.Edition.Year != 1997 || c.Edition.Label != "XE · CK 64935" || c.Edition.DiscogsReleaseID != nil {
			t.Fatalf("discogs %v: candidate = %+v", withDiscogs, c)
		}
		if c.CoverURL != "https://coverartarchive.org/front-250.jpg" {
			t.Fatalf("cover = %q", c.CoverURL)
		}
	}
	if u.count("discogs "+mbOnly) != 1 {
		t.Fatalf("discogs hits = %v", u.hits)
	}
}

func TestFetchErrorPolicy(t *testing.T) {
	u := newUpstream(t)
	p := u.provider(true)

	raw, err := p.Fetch(context.Background(), discogsDown)
	var partial *lookup.PartialError
	if !errors.As(err, &partial) || raw != nil {
		t.Fatalf("Discogs down, MusicBrainz up: raw %s, err %v, want a partial error", raw, err)
	}
	res := lookupWith(t, p, discogsDown)
	if len(res.Candidates) != 1 || len(res.Errors) != 1 || res.Errors[0].Provider != "music" ||
		*res.Candidates[0].Edition.Format != "vinyl" {
		t.Fatalf("partial result = %+v", res)
	}

	if _, err := p.Fetch(context.Background(), allDown); err == nil {
		t.Fatal("every source down must fail the fetch, so the miss is not cached")
	}

	raw, err = p.Fetch(context.Background(), unknown)
	if err != nil {
		t.Fatalf("unknown barcode: %v", err)
	}
	cands, err := p.Parse(unknown, raw)
	if err != nil || len(cands) != 0 {
		t.Fatalf("unknown barcode parsed to %+v, %v", cands, err)
	}
}

func TestFallsBackForISBNsOnly(t *testing.T) {
	p := &music.Provider{}
	if !p.Fallback("9788375780635") || p.Fallback(twoPressings) {
		t.Fatal("music provider must wait for the book providers on ISBNs only")
	}
}

func TestDiscogsErrorsHideTheToken(t *testing.T) {
	c := &discogs.Client{BaseURL: "http://127.0.0.1:1", Token: "secret"}
	_, err := c.SearchBarcode(context.Background(), twoPressings)
	if err == nil || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), twoPressings) {
		t.Fatalf("err = %v", err)
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	if _, err := (&music.Provider{}).Parse(twoPressings, json.RawMessage(`{"discogs":1}`)); err == nil {
		t.Fatal("garbage parsed")
	}
}

func TestShopNameFindsDiscogsReleases(t *testing.T) {
	tests := []struct {
		name, query, format string
		names               []string
		found               int
	}{
		{
			name: "allegro listing", names: []string{"1-800-Oświecenie Taco Hemingway CD"},
			query: "1-800-Oświecenie Taco Hemingway", format: "CD", found: 2,
		},
		{
			name: "listing words dropped", names: []string{"Taco Hemingway 1-800-Oświecenie (Płyta CD) NOWA w folii"},
			query: "Taco Hemingway 1-800-Oświecenie", format: "CD", found: 2,
		},
		{name: "vinyl", names: []string{"Kind of Blue Winyl LP"}, query: "Kind of Blue", format: "Vinyl"},
		{
			name: "first usable name", names: []string{" CD ", "1-800-Oświecenie Taco Hemingway"},
			query: "1-800-Oświecenie Taco Hemingway", found: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := newUpstream(t)
			p := u.provider(true)
			p.Names = &shopNames{names: tt.names}
			res := lookupWith(t, p, shopOnly)
			if len(res.Errors) != 0 || len(res.Candidates) != tt.found {
				t.Fatalf("result = %+v", res)
			}
			if u.count("discogs q="+tt.query+" format="+tt.format) != 1 {
				t.Fatalf("discogs hits = %v", u.hits)
			}
			if tt.found == 0 {
				return
			}
			if !res.NeedsConfirmation {
				t.Fatal("a release found by name, not barcode, must ask the user")
			}
			c := res.Candidates[1]
			if c.Title.Name != "1-800-OŚWIECENIE" || c.Title.Authors[0] != "Taco Hemingway" ||
				c.Edition.Barcode != shopOnly || c.Edition.DiscogsReleaseID == nil || *c.Edition.DiscogsReleaseID != 28352929 ||
				c.Confidence >= lookup.ConfirmThreshold {
				t.Fatalf("candidate = %+v", c)
			}
		})
	}
}

func TestShopNameOnlyWhenNoSourceKnowsTheBarcode(t *testing.T) {
	u := newUpstream(t)
	names := &shopNames{names: []string{"Taco Hemingway 1-800-Oświecenie"}}
	p := u.provider(true)
	p.Names = names
	if res := lookupWith(t, p, twoPressings); len(res.Candidates) != 2 {
		t.Fatalf("result = %+v", res)
	}
	if res := lookupWith(t, p, mbOnly); len(res.Candidates) != 1 {
		t.Fatalf("result = %+v", res)
	}
	if names.calls != 0 {
		t.Fatalf("shop asked %d times for barcodes Discogs or MusicBrainz knew", names.calls)
	}

	noDiscogs := u.provider(false)
	noDiscogs.Names = names
	if res := lookupWith(t, noDiscogs, shopOnly); len(res.Candidates) != 0 || names.calls != 0 {
		t.Fatalf("without Discogs: result = %+v, shop calls %d", res, names.calls)
	}
}

func TestShopOutageFailsTheFetch(t *testing.T) {
	u := newUpstream(t)
	p := u.provider(true)
	p.Names = &shopNames{}
	if _, err := p.Fetch(context.Background(), shopDown); err == nil {
		t.Fatal("a shop outage on an unknown barcode must fail the fetch, so the miss is not cached")
	}
}

func TestTextSearch(t *testing.T) {
	u := newUpstream(t)
	s := music.TextSearch{Discogs: &discogs.Client{HTTP: u.server.Client(), BaseURL: u.server.URL, Token: "secret"}}
	got, err := s.SearchText(context.Background(), "Taco Hemingway 1-800-Oświecenie", "cd")
	if err != nil || len(got) != 2 || got[1].Edition.DiscogsReleaseID == nil || *got[1].Edition.DiscogsReleaseID != 28352929 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if u.count("discogs q=Taco Hemingway 1-800-Oświecenie format=CD") != 1 {
		t.Fatalf("discogs hits = %v", u.hits)
	}
}
