package deals

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Automaat/ohara/backend-go/internal/dbtest"
	"github.com/Automaat/ohara/backend-go/internal/server"
)

type blockingSource struct {
	started chan struct{}
	release chan struct{}
}

func (blockingSource) Name() string   { return "olx" }
func (blockingSource) PerQuery() bool { return false }

func (b blockingSource) Search(ctx context.Context, _ string) ([]Listing, error) {
	b.started <- struct{}{}
	select {
	case <-b.release:
	case <-ctx.Done():
	}
	return nil, nil
}

func call(t *testing.T, method, url string, body, out any) int {
	t.Helper()
	var r io.Reader = http.NoBody
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, url, r)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("decode %q: %v", raw, err)
		}
	}
	return resp.StatusCode
}

func TestKeywordsAPI(t *testing.T) {
	pool := dbtest.Migrated(t)
	logger := slog.New(slog.DiscardHandler)
	store := NewStore(pool)
	h := NewHandler(store, NewHunter(store, nil, nil, Config{}, logger), logger)
	srv := httptest.NewServer(server.New(server.Config{}, logger, server.Deps{DB: pool, API: []server.Mounter{h}}))
	t.Cleanup(srv.Close)
	base := srv.URL + "/api/deals/keywords"

	var seeded []Keyword
	if code := call(t, http.MethodGet, base, nil, &seeded); code != http.StatusOK || len(seeded) != 12 {
		t.Fatalf("GET keywords = %d, %d seeded", code, len(seeded))
	}

	var added Keyword
	if code := call(t, http.MethodPost, base, KeywordInput{Kind: "reject", Phrase: "  sama   płyta "}, &added); code != http.StatusCreated ||
		added.Phrase != "sama płyta" || added.Kind != "reject" {
		t.Fatalf("POST keyword = %d, %+v", code, added)
	}
	var errBody struct{ Error, Field string }
	for _, tt := range []struct {
		in    KeywordInput
		code  int
		field string
	}{
		{KeywordInput{Kind: "reject", Phrase: "PUDEŁKO"}, http.StatusConflict, "phrase"},
		{KeywordInput{Kind: "maybe", Phrase: "x"}, http.StatusUnprocessableEntity, "kind"},
		{KeywordInput{Kind: "accept", Phrase: " -- "}, http.StatusUnprocessableEntity, "phrase"},
		{KeywordInput{Kind: "accept", Phrase: strings.Repeat("a", maxPhrase+1)}, http.StatusUnprocessableEntity, "phrase"},
	} {
		if code := call(t, http.MethodPost, base, tt.in, &errBody); code != tt.code || errBody.Field != tt.field {
			t.Errorf("POST %+v = %d %+v, want %d on %s", tt.in, code, errBody, tt.code, tt.field)
		}
	}

	rules, err := store.Rules(context.Background())
	if err != nil {
		t.Fatalf("rules: %v", err)
	}
	if !rules.Rejected("Blade Runner DVD sama płyta") {
		t.Fatal("an added reject phrase does not reject")
	}

	path := base + "/" + strconv.FormatInt(added.ID, 10)
	if code := call(t, http.MethodDelete, path, nil, nil); code != http.StatusNoContent {
		t.Fatalf("DELETE = %d", code)
	}
	if code := call(t, http.MethodDelete, path, nil, nil); code != http.StatusNotFound {
		t.Fatalf("second DELETE = %d", code)
	}
}

func TestDealsViewAndManualRun(t *testing.T) {
	pool := dbtest.Migrated(t)
	logger := slog.New(slog.DiscardHandler)
	store := NewStore(pool)
	wishlist(t, pool, wantBluray(new(int64(5000))))

	src := blockingSource{started: make(chan struct{}, 1), release: make(chan struct{})}
	hunter := NewHunter(store, []Source{src, olxFixture(t)}, &fakeNotifier{}, Config{Interval: 2 * time.Hour}, logger)
	hunter.sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	srv := httptest.NewServer(server.New(server.Config{}, logger, server.Deps{
		DB: pool, API: []server.Mounter{NewHandler(store, hunter, logger)},
	}))
	t.Cleanup(srv.Close)

	var st Status
	if code := call(t, http.MethodPost, srv.URL+"/api/deals/run", nil, &st); code != http.StatusAccepted || !st.Running {
		t.Fatalf("POST run = %d, %+v", code, st)
	}
	<-src.started
	if code := call(t, http.MethodPost, srv.URL+"/api/deals/run", nil, nil); code != http.StatusConflict {
		t.Fatalf("POST run while running = %d, want 409", code)
	}
	close(src.release)
	hunter.Wait()

	var view View
	if code := call(t, http.MethodGet, srv.URL+"/api/deals", nil, &view); code != http.StatusOK {
		t.Fatalf("GET deals = %d", code)
	}
	if view.Status.Running || !view.Status.Scheduled || view.Status.IntervalSeconds != 7200 || !view.Status.Telegram ||
		view.Status.LastResult == nil || view.Status.LastResult.Sent != 1 {
		t.Fatalf("status = %+v", view.Status)
	}
	if len(view.Observations) != 2 {
		t.Fatalf("observations = %+v", view.Observations)
	}
	sent := 0
	photos := 0
	for _, o := range view.Observations {
		if o.Format == nil || *o.Format != "bluray" || o.MaxPriceCents == nil || *o.MaxPriceCents != 5000 {
			t.Fatalf("observation format or limit missing: %+v", o)
		}
		if o.ListingPhotoURL != nil {
			photos++
			if *o.ListingPhotoURL != "https://images.example.test/bluray;s=400x300" {
				t.Fatalf("unexpected photo: %q", *o.ListingPhotoURL)
			}
		}
		if o.TitleName == nil || *o.TitleName != "Blade Runner" || o.ListingTitle == "" || o.Source != "olx" {
			t.Fatalf("observation = %+v", o)
		}
		if o.Alert == AlertSent {
			sent++
		}
	}
	if photos != 1 {
		t.Fatalf("got %d observations with photos, want 1", photos)
	}
	if sent != 1 {
		t.Fatalf("sent alerts = %d, want 1", sent)
	}
	if code := call(t, http.MethodGet, srv.URL+"/api/deals?limit=1", nil, &view); code != http.StatusOK || len(view.Observations) != 1 {
		t.Fatalf("GET deals?limit=1 = %d, %d", code, len(view.Observations))
	}
	for _, bad := range []string{"0", "501", "x"} {
		if code := call(t, http.MethodGet, srv.URL+"/api/deals?limit="+bad, nil, nil); code != http.StatusUnprocessableEntity {
			t.Errorf("GET deals?limit=%s = %d, want 422", bad, code)
		}
	}
	if code := call(t, http.MethodGet, srv.URL+"/api/deals?under_limit=true", nil, &view); code != http.StatusOK ||
		len(view.Observations) != 1 || view.Observations[0].PriceCents > *view.Observations[0].MaxPriceCents {
		t.Fatalf("GET deals?under_limit=true = %d, %+v", code, view.Observations)
	}
	if code := call(t, http.MethodGet, srv.URL+"/api/deals?under_limit=maybe", nil, nil); code != http.StatusUnprocessableEntity {
		t.Errorf("GET deals?under_limit=maybe = %d, want 422", code)
	}
}

func TestTelegramSend(t *testing.T) {
	var gotPath, gotChat, gotText string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = r.ParseForm()
		gotChat, gotText = r.PostForm.Get("chat_id"), r.PostForm.Get("text")
		if gotChat == "bad" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"ok":false,"description":"Bad Request: chat not found"}`)
			return
		}
		if gotChat == "echo" {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"ok":false,"description":"upstream error for bot123:SECRET"}`)
			return
		}
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(srv.Close)
	tg := &Telegram{Client: srv.Client(), BaseURL: srv.URL, Token: "123:SECRET", ChatID: "42"}
	if err := tg.Send(context.Background(), "🎯 Okazja"); err != nil {
		t.Fatalf("send: %v", err)
	}
	if gotPath != "/bot123:SECRET/sendMessage" || gotChat != "42" || gotText != "🎯 Okazja" {
		t.Fatalf("request path %q chat %q text %q", gotPath, gotChat, gotText)
	}
	tg.ChatID = "bad"
	if err := tg.Send(context.Background(), "x"); err == nil || !strings.Contains(err.Error(), "chat not found") {
		t.Fatalf("rejected send err = %v", err)
	}
	tg.ChatID = "echo"
	if err := tg.Send(context.Background(), "x"); err == nil || strings.Contains(err.Error(), "SECRET") ||
		!strings.Contains(err.Error(), "upstream error for bot<token>") {
		t.Fatalf("echoed token err = %v, want the token cut out", err)
	}
	down := &Telegram{Client: srv.Client(), BaseURL: "http://127.0.0.1:1", Token: "123:SECRET", ChatID: "42"}
	err := down.Send(context.Background(), "x")
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("transport err = %v, want an error without the token", err)
	}
}

func TestRecentUsesMatchedWishAfterValuation(t *testing.T) {
	pool := dbtest.Migrated(t)
	store := NewStore(pool)
	ctx := context.Background()
	wishlist(t, pool, wantBluray(new(int64(5000))))
	wants, err := store.Wants(ctx)
	if err != nil || len(wants) != 1 {
		t.Fatalf("wants = %v, %v", wants, err)
	}
	wanted := &wants[0]
	const wantedCover = "https://images.example.test/wanted.jpg"
	if _, err := pool.Exec(ctx, "UPDATE edition SET cover_url = $1 WHERE id = $2", wantedCover, wanted.EditionID); err != nil {
		t.Fatal(err)
	}

	var ownedEdition int64
	if err := pool.QueryRow(ctx, `INSERT INTO edition (title_id, format, cover_url)
		VALUES ($1, 'bluray', 'https://images.example.test/owned.jpg') RETURNING id`, wanted.TitleID).Scan(&ownedEdition); err != nil {
		t.Fatal(err)
	}
	owned := &Want{EditionID: ownedEdition, TitleID: wanted.TitleID}
	for _, tc := range []struct {
		id    string
		price int64
		alert bool
	}{
		{id: "under-limit", price: 4500, alert: true},
		{id: "over-limit", price: 5500, alert: false},
	} {
		listing := Listing{Source: "olx", ExternalID: tc.id, Title: "Blade Runner Blu-ray", PriceCents: tc.price}
		if _, _, err := store.Record(ctx, &listing, owned, false); err != nil {
			t.Fatal(err)
		}
		if created, alerted, err := store.Record(ctx, &listing, wanted, tc.alert); err != nil || created || alerted != tc.alert {
			t.Fatalf("record %s: created=%t alerted=%t err=%v", tc.id, created, alerted, err)
		}
	}

	observations, err := store.Recent(ctx, RecentFilter{Limit: 10})
	if err != nil || len(observations) != 2 {
		t.Fatalf("recent = %v, %v", observations, err)
	}
	for _, got := range observations {
		if got.EditionID == nil || *got.EditionID != wanted.EditionID ||
			got.CoverURL == nil || *got.CoverURL != wantedCover ||
			got.MaxPriceCents == nil || *got.MaxPriceCents != 5000 {
			t.Fatalf("listing %s did not retain the match: %+v", got.ExternalID, got)
		}
		var storedEdition int64
		if err := pool.QueryRow(ctx, "SELECT edition_id FROM price_observation WHERE id = $1", got.ID).Scan(&storedEdition); err != nil || storedEdition != ownedEdition {
			t.Fatalf("valuation edition = %d, %v", storedEdition, err)
		}
	}
}

func TestRecentUnderLimitKeepsOlderListings(t *testing.T) {
	pool := dbtest.Migrated(t)
	store := NewStore(pool)
	ctx := context.Background()
	wishlist(t, pool, wantBluray(new(int64(4000))))
	wants, err := store.Wants(ctx)
	if err != nil || len(wants) != 1 {
		t.Fatalf("wants = %v, %v", wants, err)
	}
	wanted := &wants[0]
	var ownedEdition int64
	if err := pool.QueryRow(ctx, `INSERT INTO edition (title_id, format) VALUES ($1, 'bluray') RETURNING id`,
		wanted.TitleID).Scan(&ownedEdition); err != nil {
		t.Fatal(err)
	}
	owned := &Want{EditionID: ownedEdition, TitleID: wanted.TitleID}
	for _, tc := range []struct {
		id    string
		price int64
		want  *Want
	}{
		{id: "under", price: 3500, want: wanted},
		{id: "over-1", price: 5500, want: wanted},
		{id: "over-2", price: 5500, want: wanted},
		{id: "owned", price: 1000, want: owned},
	} {
		listing := Listing{Source: "olx", ExternalID: tc.id, Title: "Blade Runner Blu-ray", PriceCents: tc.price}
		if _, _, err := store.Record(ctx, &listing, tc.want, false); err != nil {
			t.Fatal(err)
		}
	}

	newest, err := store.Recent(ctx, RecentFilter{Limit: 2})
	if err != nil || !slices.Equal(externalIDs(newest), []string{"owned", "over-2"}) {
		t.Fatalf("recent = %v, %v", externalIDs(newest), err)
	}
	under, err := store.Recent(ctx, RecentFilter{Limit: 2, UnderLimit: true})
	if err != nil || !slices.Equal(externalIDs(under), []string{"under"}) {
		t.Fatalf("recent under limit = %v, %v", externalIDs(under), err)
	}
}

// TestRecentMatchesRecordedWishBeforeAlert pins which copy matches a listing: the wish it was
// last recorded for, even when that copy has left the wishlist, and its alert's copy only when
// it was recorded for none.
func TestRecentMatchesRecordedWishBeforeAlert(t *testing.T) {
	pool := dbtest.Migrated(t)
	store := NewStore(pool)
	ctx := context.Background()
	first := wishlist(t, pool, wantBluray(new(int64(4000))))
	second := wishlist(t, pool, wantBluray(new(int64(4000))))
	wants, err := store.Wants(ctx)
	if err != nil || len(wants) != 2 {
		t.Fatalf("wants = %v, %v", wants, err)
	}
	listing := Listing{Source: "olx", ExternalID: "bought", Title: "Blade Runner Blu-ray", PriceCents: 3000}
	if _, alerted, err := store.Record(ctx, &listing, &wants[0], true); err != nil || !alerted {
		t.Fatalf("record for the first wish: alerted=%t err=%v", alerted, err)
	}
	if _, _, err := store.Record(ctx, &listing, &wants[1], false); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE copy SET status = 'owned', max_price_cents = NULL WHERE id = $1`, second); err != nil {
		t.Fatal(err)
	}
	var alertOnly int64
	if err := pool.QueryRow(ctx, `INSERT INTO price_observation (source, external_id, price_cents, edition_id, raw)
		VALUES ('olx', 'alert-only', 3500, $1, '{"title": "Blade Runner"}') RETURNING id`, wants[0].EditionID).Scan(&alertOnly); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO deal_alert (observation_id, copy_id, sent_at, attempts) VALUES ($1, $2, now(), 1)`,
		alertOnly, first); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name   string
		filter RecentFilter
		want   []string
	}{
		{name: "all", filter: RecentFilter{Limit: 10}, want: []string{"alert-only", "bought"}},
		{name: "matched", filter: RecentFilter{Limit: 10, Matched: true}, want: []string{"alert-only"}},
		{name: "under limit", filter: RecentFilter{Limit: 10, UnderLimit: true}, want: []string{"alert-only"}},
		{name: "sent", filter: RecentFilter{Limit: 10, Sent: true}, want: []string{"alert-only"}},
	} {
		got, err := store.Recent(ctx, tc.filter)
		if err != nil || !slices.Equal(externalIDs(got), tc.want) {
			t.Fatalf("%s = %v, %v; want %v", tc.name, externalIDs(got), err, tc.want)
		}
		for i := range got {
			matched := got[i].ExternalID == "alert-only"
			if matched != (got[i].MaxPriceCents != nil) {
				t.Fatalf("%s: %s max price = %v", tc.name, got[i].ExternalID, got[i].MaxPriceCents)
			}
		}
	}
}

func externalIDs(obs []Observation) []string {
	out := make([]string, 0, len(obs))
	for i := range obs {
		out = append(out, obs[i].ExternalID)
	}
	return out
}

// matchedListing returns the one matched listing with externalID.
func matchedListing(t *testing.T, store *Store, externalID string) Observation {
	t.Helper()
	obs, err := store.Recent(context.Background(), RecentFilter{Limit: 100, Matched: true})
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	var found []Observation
	for i := range obs {
		if obs[i].ExternalID == externalID {
			found = append(found, obs[i])
		}
	}
	if len(found) != 1 {
		t.Fatalf("listing %s appears %d times, want once", externalID, len(found))
	}
	return found[0]
}

func TestDealsMatchedListings(t *testing.T) {
	pool := dbtest.Migrated(t)
	logger := slog.New(slog.DiscardHandler)
	store := NewStore(pool)
	ctx := context.Background()
	wishlist(t, pool, wantBluray(new(int64(4000))))
	noLimit := bladeRunner("dvd")
	wishlist(t, pool, &noLimit)
	wants, err := store.Wants(ctx)
	if err != nil || len(wants) != 2 {
		t.Fatalf("wants = %v, %v", wants, err)
	}
	limited, unlimited := &wants[0], &wants[1]
	var ownedEdition int64
	if err := pool.QueryRow(ctx, `INSERT INTO edition (title_id, format) VALUES ($1, 'uhd') RETURNING id`,
		limited.TitleID).Scan(&ownedEdition); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO copy (edition_id, status) VALUES ($1, 'owned')`, ownedEdition); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO copy (edition_id, status) VALUES ($1, 'owned')`, limited.EditionID); err != nil {
		t.Fatal(err)
	}
	owned := &Want{EditionID: ownedEdition, TitleID: limited.TitleID}
	wishedAndOwned := &Want{EditionID: limited.EditionID, TitleID: limited.TitleID}
	ids := map[string]int64{}
	for _, tc := range []struct {
		id    string
		price int64
		want  *Want
		alert bool
	}{
		{id: "under", price: 3500, want: limited, alert: true},
		{id: "over", price: 5500, want: limited},
		{id: "no-limit", price: 2000, want: unlimited},
		{id: "owned", price: 1000, want: owned},
		{id: "valuation-wished", price: 3000, want: wishedAndOwned},
	} {
		listing := Listing{Source: "olx", ExternalID: tc.id, Title: "Blade Runner", PriceCents: tc.price}
		if _, _, err := store.Record(ctx, &listing, tc.want, tc.alert); err != nil {
			t.Fatal(err)
		}
		var id int64
		if err := pool.QueryRow(ctx, `SELECT id FROM price_observation WHERE external_id = $1`, tc.id).Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids[tc.id] = id
	}
	if err := store.MarkSent(ctx, ids["under"], 3500); err != nil {
		t.Fatal(err)
	}
	// A further drop under the limit queues a repeat alert on the same row:
	// the listing stays one row, pending until the repeat is sent.
	dropped := Listing{Source: "olx", ExternalID: "under", Title: "Blade Runner", PriceCents: 3000}
	if _, alerted, err := store.Record(ctx, &dropped, limited, true); err != nil || !alerted {
		t.Fatalf("record drop = %v, %v; want a repeat alert", alerted, err)
	}
	if obs := matchedListing(t, store, "under"); obs.Alert != AlertPending || obs.PriceCents != 3000 {
		t.Fatalf("dropped listing = %+v, want one pending alert at 30 zł", obs)
	}
	if err := store.MarkSent(ctx, ids["under"], 3000); err != nil {
		t.Fatal(err)
	}
	if obs := matchedListing(t, store, "under"); obs.Alert != AlertSent {
		t.Fatalf("listing after the repeat alert = %+v, want sent", obs)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO deal_alert (observation_id, copy_id, sent_at, attempts) VALUES ($1, $2, now(), 1)`,
		ids["no-limit"], unlimited.CopyID); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(server.New(server.Config{}, logger, server.Deps{
		DB: pool, API: []server.Mounter{NewHandler(store, NewHunter(store, nil, nil, Config{}, logger), logger)},
	}))
	t.Cleanup(srv.Close)

	for _, tc := range []struct {
		query string
		want  []string
	}{
		{query: "", want: []string{"valuation-wished", "owned", "no-limit", "over", "under"}},
		{query: "?matched=true", want: []string{"no-limit", "over", "under"}},
		{query: "?matched=true&limit=2", want: []string{"no-limit", "over"}},
		{query: "?matched=false&limit=2", want: []string{"valuation-wished", "owned"}},
		{query: "?under_limit=true", want: []string{"under"}},
		{query: "?matched=true&under_limit=true", want: []string{"under"}},
		{query: "?sent=true", want: []string{"under"}},
		{query: "?sent=true&limit=1", want: []string{"under"}},
	} {
		var view View
		if code := call(t, http.MethodGet, srv.URL+"/api/deals"+tc.query, nil, &view); code != http.StatusOK ||
			view.Wishes != 2 || !slices.Equal(externalIDs(view.Observations), tc.want) {
			t.Errorf("GET deals%s = %d, %d wishes, %v; want 2 wishes, %v",
				tc.query, code, view.Wishes, externalIDs(view.Observations), tc.want)
		}
		for i := range view.Observations {
			o := &view.Observations[i]
			if o.ExternalID == "no-limit" && (o.MaxPriceCents != nil || o.EditionID == nil || *o.EditionID != unlimited.EditionID) {
				t.Errorf("no-limit listing = %+v, want the unlimited wish's edition and no limit", o)
			}
		}
	}
	for _, param := range []string{"matched", "sent"} {
		if code := call(t, http.MethodGet, srv.URL+"/api/deals?"+param+"=maybe", nil, nil); code != http.StatusUnprocessableEntity {
			t.Errorf("GET deals?%s=maybe = %d, want 422", param, code)
		}
	}
}

func TestRecentClearsPhotoOnRepeatObservation(t *testing.T) {
	pool := dbtest.Migrated(t)
	store := NewStore(pool)
	ctx := context.Background()
	wishlist(t, pool, wantBluray(nil))
	wants, err := store.Wants(ctx)
	if err != nil || len(wants) != 1 {
		t.Fatalf("wants = %v, %v", wants, err)
	}

	listing := Listing{Source: "olx", ExternalID: "repeat", Title: "Blade Runner Blu-ray", PriceCents: 5000}
	if _, _, err := store.Record(ctx, &listing, &wants[0], false); err != nil {
		t.Fatal(err)
	}
	for _, photo := range []string{"https://images.example.test/offer.jpg", ""} {
		listing.PhotoURL = photo
		if created, _, err := store.Record(ctx, &listing, &wants[0], false); err != nil || created {
			t.Fatalf("record photo %q: created=%t err=%v", photo, created, err)
		}
		got, err := store.Recent(ctx, RecentFilter{Limit: 10})
		if err != nil || len(got) != 1 {
			t.Fatalf("recent = %v, %v", got, err)
		}
		if photo == "" && got[0].ListingPhotoURL != nil || photo != "" &&
			(got[0].ListingPhotoURL == nil || *got[0].ListingPhotoURL != photo) {
			t.Fatalf("listing photo = %v, want %q", got[0].ListingPhotoURL, photo)
		}
	}
}

func TestDismissDeal(t *testing.T) {
	pool := dbtest.Migrated(t)
	logger := slog.New(slog.DiscardHandler)
	store := NewStore(pool)
	ctx := context.Background()
	wishlist(t, pool, wantBluray(new(int64(5000))))
	srv := httptest.NewServer(server.New(server.Config{}, logger, server.Deps{
		DB: pool, API: []server.Mounter{NewHandler(store, NewHunter(store, nil, nil, Config{}, logger), logger)},
	}))
	t.Cleanup(srv.Close)

	listing := func(id string, price int64) Listing {
		return Listing{Source: "olx", ExternalID: id, Title: "Blade Runner Blu-ray", URL: "https://www.olx.pl/d/oferta/" + id, PriceCents: price}
	}
	src := &fakeSource{name: "olx", listings: []Listing{listing("pending", 4000), listing("sent", 3000), listing("over", 6000)}}
	notifier := &fakeNotifier{fail: 1}
	h := newTestHunter(pool, notifier, src)
	if res := mustRun(t, h); res.Alerts != 2 || res.Sent != 1 {
		t.Fatalf("first run = %+v, want 2 alerts, 1 sent", res)
	}
	ids := map[string]int64{}
	rows, err := pool.Query(ctx, `SELECT external_id, id FROM price_observation`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var ext string
		var id int64
		if err := rows.Scan(&ext, &id); err != nil {
			t.Fatal(err)
		}
		ids[ext] = id
	}
	if rows.Err() != nil || len(ids) != 3 {
		t.Fatalf("observations = %v, %v", ids, rows.Err())
	}
	pending, err := store.PendingAlerts(ctx)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending before dismissal = %+v, %v", pending, err)
	}
	unsent := pending[0].ExternalID
	if n := count(t, pool, `SELECT count(*) FROM deal_alert WHERE sent_at IS NOT NULL`); n != 1 {
		t.Fatalf("sent alerts = %d, want 1", n)
	}

	dismissal := func(ext string) string {
		return srv.URL + "/api/deals/" + strconv.FormatInt(ids[ext], 10) + "/dismissal"
	}
	for _, ext := range []string{"pending", "sent", "over"} {
		for range 2 {
			if code := call(t, http.MethodPost, dismissal(ext), nil, nil); code != http.StatusNoContent {
				t.Fatalf("POST dismissal of %s = %d, want 204", ext, code)
			}
		}
	}
	listed := func() map[string][]string {
		got := map[string][]string{}
		for _, q := range []string{"", "?under_limit=true", "?matched=true", "?sent=true"} {
			var view View
			if code := call(t, http.MethodGet, srv.URL+"/api/deals"+q, nil, &view); code != http.StatusOK {
				t.Fatalf("GET deals%s = %d", q, code)
			}
			got[q] = externalIDs(view.Observations)
		}
		return got
	}
	for q, got := range listed() {
		if len(got) != 0 {
			t.Errorf("GET deals%s lists dismissed listings %v", q, got)
		}
	}
	if pending, err := store.PendingAlerts(ctx); err != nil || len(pending) != 0 {
		t.Fatalf("pending after dismissal = %+v, %v, want none", pending, err)
	}
	if n := count(t, pool, `SELECT count(*) FROM price_observation`); n != 3 {
		t.Fatalf("observations after dismissal = %d, want 3 kept for the valuation", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM deal_alert`); n != 2 {
		t.Fatalf("alerts after dismissal = %d, want 2 kept", n)
	}

	src.mu.Lock()
	src.listings = []Listing{listing("pending", 1000), listing("sent", 1000), listing("over", 1000)}
	src.mu.Unlock()
	if res := mustRun(t, h); res.Alerts != 0 || res.Sent != 0 {
		t.Fatalf("run seeing dismissed listings under the limit = %+v, want no alert", res)
	}
	if got := notifier.messages(); len(got) != 1 {
		t.Fatalf("messages = %v, want only the one sent before the dismissal", got)
	}
	if n := count(t, pool, `SELECT count(*) FROM deal_alert`); n != 2 {
		t.Fatalf("alerts after a run = %d, want no new alert for a dismissed listing", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM price_observation WHERE dismissed_at IS NULL OR price_cents <> 1000`); n != 0 {
		t.Fatalf("%d listings lost their dismissal or kept an old price after a run", n)
	}
	for q, got := range listed() {
		if len(got) != 0 {
			t.Errorf("GET deals%s lists dismissed listings %v after a run", q, got)
		}
	}

	for range 2 {
		if code := call(t, http.MethodDelete, dismissal(unsent), nil, nil); code != http.StatusNoContent {
			t.Fatalf("DELETE dismissal = %d, want 204", code)
		}
	}
	if got := listed()["?under_limit=true"]; !slices.Equal(got, []string{unsent}) {
		t.Fatalf("under limit after undo = %v, want %s back", got, unsent)
	}
	if pending, err := store.PendingAlerts(ctx); err != nil || len(pending) != 1 || pending[0].ExternalID != unsent {
		t.Fatalf("pending after undo = %+v, %v", pending, err)
	}

	var valuationID int64
	if err := pool.QueryRow(ctx, `INSERT INTO price_observation (source, external_id, price_cents, url)
		VALUES ('skupszop', 'quote', 500, '') RETURNING id`).Scan(&valuationID); err != nil {
		t.Fatal(err)
	}
	paths := []string{
		"/api/deals/999999/dismissal", "/api/deals/x/dismissal",
		"/api/deals/" + strconv.FormatInt(valuationID, 10) + "/dismissal",
	}
	for _, path := range paths {
		for _, method := range []string{http.MethodPost, http.MethodDelete} {
			if code := call(t, method, srv.URL+path, nil, nil); code != http.StatusNotFound {
				t.Errorf("%s %s = %d, want 404", method, path, code)
			}
		}
	}
}

func TestDismissDuringDeliveryStopsAlert(t *testing.T) {
	pool := dbtest.Migrated(t)
	store := NewStore(pool)
	ctx := context.Background()
	wishlist(t, pool, wantBluray(new(int64(5000))))
	src := &fakeSource{name: "olx", listings: []Listing{
		{Source: "olx", ExternalID: "a", Title: "Blade Runner Blu-ray", URL: "https://www.olx.pl/d/oferta/a", PriceCents: 4000},
		{Source: "olx", ExternalID: "b", Title: "Blade Runner Blu-ray", URL: "https://www.olx.pl/d/oferta/b", PriceCents: 4000},
	}}
	notifier := &fakeNotifier{}
	h := newTestHunter(pool, notifier, src)
	h.sleep = func(ctx context.Context, _ time.Duration) error {
		_, err := pool.Exec(ctx, `UPDATE price_observation o SET dismissed_at = now()
			WHERE NOT EXISTS (SELECT 1 FROM deal_alert a WHERE a.observation_id = o.id AND a.sent_at IS NOT NULL)`)
		return err
	}
	if res := mustRun(t, h); res.Alerts != 2 || res.Sent != 1 || len(notifier.messages()) != 1 {
		t.Fatalf("run = %+v, %d messages, want the alert dismissed during the pause unsent", res, len(notifier.messages()))
	}
	if pending, err := store.PendingAlerts(ctx); err != nil || len(pending) != 0 {
		t.Fatalf("pending = %+v, %v", pending, err)
	}
}

// dismissingNotifier dismisses the listing it is sending, as a collector
// pressing "Ignoruj" while the message is on its way does, and reports
// whether that dismissal finished before the message went out.
type dismissingNotifier struct {
	store         *Store
	observationID func() int64
	dismissed     chan error
	early         bool
}

func (n *dismissingNotifier) Send(ctx context.Context, _ string) error {
	go func() { n.dismissed <- n.store.Dismiss(context.WithoutCancel(ctx), n.observationID()) }()
	select {
	case <-n.dismissed:
		n.early = true
	case <-time.After(300 * time.Millisecond):
	}
	return nil
}

func TestDismissWaitsForAlertBeingSent(t *testing.T) {
	pool := dbtest.Migrated(t)
	store := NewStore(pool)
	ctx := context.Background()
	wishlist(t, pool, wantBluray(new(int64(5000))))
	src := &fakeSource{name: "olx", listings: []Listing{
		{Source: "olx", ExternalID: "a", Title: "Blade Runner Blu-ray", URL: "https://www.olx.pl/d/oferta/a", PriceCents: 4000},
	}}
	notifier := &dismissingNotifier{store: store, dismissed: make(chan error, 1), observationID: func() int64 {
		var id int64
		if err := pool.QueryRow(ctx, `SELECT id FROM price_observation`).Scan(&id); err != nil {
			t.Error(err)
		}
		return id
	}}
	h := newTestHunter(pool, notifier, src)
	if res := mustRun(t, h); res.Sent != 1 {
		t.Fatalf("run = %+v, want the alert sent", res)
	}
	if notifier.early {
		t.Fatal("a dismissal committed while its listing's alert was being sent")
	}
	if err := <-notifier.dismissed; err != nil {
		t.Fatalf("dismiss after the send: %v", err)
	}
	if n := count(t, pool, `SELECT count(*) FROM deal_alert WHERE sent_at IS NOT NULL`); n != 1 {
		t.Fatalf("sent alerts = %d, want the message that went out recorded", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM price_observation WHERE dismissed_at IS NOT NULL`); n != 1 {
		t.Fatalf("dismissed listings = %d, want the dismissal applied after the send", n)
	}
}
