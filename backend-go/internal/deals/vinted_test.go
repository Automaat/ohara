package deals

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Automaat/ohara/backend-go/internal/dbtest"
)

func TestParseVinted(t *testing.T) {
	listings, err := parseVinted(fixture(t, "vinted.html"), "https://www.vinted.pl")
	if err != nil {
		t.Fatalf("parseVinted: %v", err)
	}
	got := byID(listings)
	if len(got) != 10 {
		t.Fatalf("got %d listings, want the 10 priced in PLN", len(got))
	}
	l := got["10464769656"]
	if l.Source != "vinted" || l.Title != "Łowca Androidów Blu-Ray PL Edycja kolekcjonerska 30 rocznica" ||
		l.PriceCents != 25000 || l.URL != "https://www.vinted.pl/items/10464769656-lowca-androidow-blu-ray-pl-edycja-kolekcjonerska-30-rocznica" ||
		l.AcquiredAs == nil || *l.AcquiredAs != "used" {
		t.Fatalf("listing = %+v", l)
	}
	if !strings.Contains(string(l.Raw), `"condition":"Bardzo dobry"`) || strings.Contains(string(l.Raw), "photos") {
		t.Fatalf("raw = %s", l.Raw)
	}
	for id, state := range map[string]string{"10789493495": "new", "10367543329": "new", "10675025109": "used"} {
		if a := got[id].AcquiredAs; a == nil || *a != state {
			t.Errorf("listing %s state = %v, want %s", id, a, state)
		}
	}
	for _, missing := range []string{"0", "1", "2"} {
		if _, ok := got[missing]; ok {
			t.Errorf("listing %s without an id or a PLN price was kept", missing)
		}
	}
}

func TestParseVintedRejectsOtherShapes(t *testing.T) {
	for name, body := range map[string]string{
		"html without payload":  `<html><body>Just a moment...</body></html>`,
		"payload without items": `<script>self.__next_f.push([1,"0:{\"catalog\":{}}\n"])</script>`,
		"items by reference":    `<script>self.__next_f.push([1,"\"items\":{\"items\":\"$5a\"}"])</script>`,
		"broken items":          `<script>self.__next_f.push([1,"\"items\":{\"items\":[{\"id\":"])</script>`,
	} {
		_, err := parseVinted([]byte(body), "")
		if _, ok := errors.AsType[parseError](err); !ok {
			t.Errorf("%s: err = %v, want a parse error", name, err)
		}
	}
	listings, err := parseVinted([]byte(`<script>self.__next_f.push([1,"\"items\":{\"items\":[],\"pagination\":{}}"])</script>`), "")
	if err != nil || len(listings) != 0 {
		t.Fatalf("empty results = %d listings, %v", len(listings), err)
	}
}

func TestVintedCondition(t *testing.T) {
	tests := []struct {
		line string
		want string
	}{
		{"Nowy z metką", "new"},
		{"Nowy bez metki", "new"},
		{"Bardzo dobry", "used"},
		{"Dobry", "used"},
		{"Zadowalający", "used"},
		{"Disney · Bardzo dobry", "used"},
		{"M · Nike · Nowy z metką", "new"},
		{"", ""},
		{"Warner", ""},
	}
	for _, tt := range tests {
		got := ""
		if s := vintedCondition(tt.line); s != nil {
			got = *s
		}
		if got != tt.want {
			t.Errorf("vintedCondition(%q) = %q, want %q", tt.line, got, tt.want)
		}
	}
}

func TestVintedSearchOverHTTP(t *testing.T) {
	var gotPath, gotQuery, gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery, gotUA = r.URL.Path, r.URL.RawQuery, r.UserAgent()
		if r.URL.Path != "/catalog" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write(fixture(t, "vinted.html"))
	}))
	t.Cleanup(srv.Close)
	ctx := context.Background()

	listings, err := Vinted{Client: srv.Client(), BaseURL: srv.URL + "/catalog"}.Search(ctx, "łowca androidów")
	if err != nil || len(listings) != 10 {
		t.Fatalf("vinted = %d listings, %v", len(listings), err)
	}
	if gotPath != "/catalog" || gotQuery != "order=newest_first&search_text=%C5%82owca+android%C3%B3w" ||
		!strings.HasPrefix(gotUA, "Mozilla/5.0") {
		t.Fatalf("request %s?%s, user agent %q", gotPath, gotQuery, gotUA)
	}
	if !strings.HasPrefix(listings[0].URL, srv.URL+"/items/") {
		t.Fatalf("url = %q", listings[0].URL)
	}
	_, err = Vinted{Client: srv.Client(), BaseURL: srv.URL + "/blocked"}.Search(ctx, "x")
	if se, ok := errors.AsType[statusError](err); !ok || se.code != http.StatusForbidden {
		t.Fatalf("blocked vinted err = %v", err)
	}
}

// TestRunAlertsOnVintedListing is the issue's acceptance check: a Vinted
// listing of a wishlist copy is stored as a price_observation, alerts like
// an OLX one and keeps the Vinted canary up.
func TestRunAlertsOnVintedListing(t *testing.T) {
	pool := dbtest.Migrated(t)
	wishlist(t, pool, wantBluray(new(int64(26000))))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(fixture(t, "vinted.html"))
	}))
	t.Cleanup(srv.Close)
	notifier := &fakeNotifier{}
	h := NewHunter(NewStore(pool), []Source{Vinted{Client: srv.Client(), BaseURL: srv.URL + "/catalog"}},
		notifier, Config{CanaryQuery: "dvd"}, slog.New(slog.DiscardHandler))
	h.sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }

	res := mustRun(t, h)
	if len(res.Errors) != 0 || res.Alerts != 1 || res.Sent != 1 {
		t.Fatalf("run = %+v", res)
	}
	msgs := notifier.messages()
	if len(msgs) != 1 {
		t.Fatalf("sent %d messages, want 1", len(msgs))
	}
	for _, part := range []string{
		"🎯 Okazja: Blade Runner · Blu-ray", "250,00 zł (limit 260,00 zł) · Vinted · używany",
		"Łowca Androidów Blu-Ray PL Edycja kolekcjonerska 30 rocznica", srv.URL + "/items/10464769656-",
	} {
		if !strings.Contains(msgs[0], part) {
			t.Fatalf("message %q lacks %q", msgs[0], part)
		}
	}
	if n := count(t, pool, `SELECT count(*) FROM price_observation
		WHERE source = 'vinted' AND external_id = '10464769656' AND acquired_as = 'used'`); n != 1 {
		t.Fatalf("stored %d observations of the Blu-ray listing", n)
	}
	if v := mustMetric(t, `ohara_source_canary_ok{source="vinted"}`); v != 1 {
		t.Fatalf("vinted canary = %v", v)
	}
	if v := mustMetric(t, `ohara_source_results{source="vinted"}`); v != float64(10*(1+res.Queries)) {
		t.Fatalf("vinted results = %v", v)
	}
	if res := mustRun(t, h); res.New != 0 || res.Sent != 0 {
		t.Fatalf("repeat run = %+v, want nothing new", res)
	}
}
