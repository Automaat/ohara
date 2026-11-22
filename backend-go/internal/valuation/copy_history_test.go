package valuation

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Automaat/ohara/backend-go/internal/dbtest"
	"github.com/Automaat/ohara/backend-go/internal/deals"
	"github.com/Automaat/ohara/backend-go/internal/server"
)

func grosze(v int64) *int64 { return new(v) }

// dbDay is the database's date daysAgo days before today, as YYYY-MM-DD.
func dbDay(t *testing.T, pool *pgxpool.Pool, daysAgo int) string {
	t.Helper()
	var day string
	if err := pool.QueryRow(context.Background(),
		`SELECT to_char(current_date - $1::int, 'YYYY-MM-DD')`, daysAgo).Scan(&day); err != nil {
		t.Fatal(err)
	}
	return day
}

// copyDays reads every stored copy day, by copy and day.
func copyDays(t *testing.T, pool *pgxpool.Pool) []CopyDay {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT copy_id, to_char(day, 'YYYY-MM-DD'),
			floor_cents, mid_cents, ceiling_cents
		FROM copy_value_history ORDER BY copy_id, day`)
	if err != nil {
		t.Fatal(err)
	}
	days, err := pgx.CollectRows(rows, pgx.RowToStructByPos[CopyDay])
	if err != nil {
		t.Fatal(err)
	}
	return days
}

// TestSaveCopyDays stores one row per copy and day: a second save of the
// same day replaces that day's rows (here after the book was sold, with a
// new Blu-ray price and a copy deleted since it was valued), other days
// stay, a copy no longer owned keeps its rows and deleting a copy deletes
// them.
func TestSaveCopyDays(t *testing.T) {
	pool := dbtest.Migrated(t)
	seed(t, pool)
	ctx := context.Background()
	store := NewStore(pool)
	today, yesterday := dbDay(t, pool, 0), dbDay(t, pool, 1)

	book := CopyValue{CopyID: 1, Floor: &Estimate{Cents: 537}, Mid: &Estimate{Cents: 2500}}
	bluray := CopyValue{CopyID: 2, Mid: &Estimate{Cents: 4000}, Ceiling: &Estimate{Cents: 9000}}
	unpriced := CopyValue{CopyID: 3}
	if err := store.SaveCopyDays(ctx, yesterday, []CopyValue{book, bluray, unpriced}); err != nil {
		t.Fatalf("SaveCopyDays yesterday: %v", err)
	}
	if err := store.SaveCopyDays(ctx, today, []CopyValue{book, bluray, unpriced}); err != nil {
		t.Fatalf("SaveCopyDays today: %v", err)
	}
	want := []CopyDay{
		{CopyID: 1, Day: yesterday, FloorCents: grosze(537), MidCents: grosze(2500)},
		{CopyID: 1, Day: today, FloorCents: grosze(537), MidCents: grosze(2500)},
		{CopyID: 2, Day: yesterday, MidCents: grosze(4000), CeilingCents: grosze(9000)},
		{CopyID: 2, Day: today, MidCents: grosze(4000), CeilingCents: grosze(9000)},
		{CopyID: 3, Day: yesterday},
		{CopyID: 3, Day: today},
	}
	if got := copyDays(t, pool); !reflect.DeepEqual(got, want) {
		t.Fatalf("after two days = %+v, want %+v", got, want)
	}

	exec(t, pool, `UPDATE copy SET status = 'wishlist', price_cents = NULL WHERE id = 1`)
	gone := CopyValue{CopyID: 999, Mid: &Estimate{Cents: 100}}
	bluray.Mid = &Estimate{Cents: 4200}
	if err := store.SaveCopyDays(ctx, today, []CopyValue{bluray, unpriced, gone}); err != nil {
		t.Fatalf("second SaveCopyDays today: %v", err)
	}
	want = []CopyDay{
		{CopyID: 1, Day: yesterday, FloorCents: grosze(537), MidCents: grosze(2500)},
		{CopyID: 2, Day: yesterday, MidCents: grosze(4000), CeilingCents: grosze(9000)},
		{CopyID: 2, Day: today, MidCents: grosze(4200), CeilingCents: grosze(9000)},
		{CopyID: 3, Day: yesterday},
		{CopyID: 3, Day: today},
	}
	if got := copyDays(t, pool); !reflect.DeepEqual(got, want) {
		t.Fatalf("after a second run today = %+v, want %+v", got, want)
	}

	exec(t, pool, `DELETE FROM copy WHERE id = 3`)
	want = want[:3]
	if got := copyDays(t, pool); !reflect.DeepEqual(got, want) {
		t.Fatalf("after deleting copy 3 = %+v, want %+v", got, want)
	}
}

// TestRunSavesCopyDays stores each owned copy's values with the day's
// collection value, and a second run the same day keeps one row a copy.
func TestRunSavesCopyDays(t *testing.T) {
	pool := dbtest.Migrated(t)
	seed(t, pool)
	ctx := context.Background()
	olx := &fakeSource{name: "olx", listings: []deals.Listing{
		listing("1", "Katedra Jacek Dukaj", 2000, used()),
		listing("4", "Diuna Blu-ray", 4000, used()),
	}}
	bb := &fakeBuyBack{quotes: map[string]Quote{
		"9788308073209": {Offered: true, Cents: 537, Title: "Katedra", URL: "https://skupszop.test/sprzedaj"},
	}}
	r := newTestRunner(pool, bb, olx)
	for range 2 {
		if _, err := r.Run(ctx); err != nil {
			t.Fatalf("Run: %v", err)
		}
	}
	history, err := r.store.History(ctx, 1)
	if err != nil || len(history) != 1 {
		t.Fatalf("history = %+v, %v", history, err)
	}
	day := history[0].Day
	want := []CopyDay{
		{CopyID: 1, Day: day, FloorCents: grosze(537), MidCents: grosze(2000)},
		{CopyID: 2, Day: day, MidCents: grosze(4000)},
		{CopyID: 3, Day: day, MidCents: grosze(4000)},
	}
	if got := copyDays(t, pool); !reflect.DeepEqual(got, want) {
		t.Fatalf("copy days = %+v, want %+v", got, want)
	}
}

// TestCopyHistoryAPI answers a title's copy days of the last 90 days,
// owned copies or not, and none of another title's.
func TestCopyHistoryAPI(t *testing.T) {
	pool := dbtest.Migrated(t)
	seed(t, pool)
	logger := slog.New(slog.DiscardHandler)
	store := NewStore(pool)
	runner := NewRunner(store, nil, nil, nil, Config{Window: 90 * 24 * time.Hour}, logger)
	srv := httptest.NewServer(server.New(server.Config{}, logger, server.Deps{
		DB: pool, API: []server.Mounter{NewHandler(store, runner, logger)},
	}))
	t.Cleanup(srv.Close)
	ctx := context.Background()
	for _, ago := range []int{90, 89, 2, 0} {
		values := []CopyValue{
			{CopyID: 1, Mid: &Estimate{Cents: 2500 + int64(ago)}},
			{CopyID: 2, Mid: &Estimate{Cents: 4000 + int64(ago)}},
		}
		if err := store.SaveCopyDays(ctx, dbDay(t, pool, ago), values); err != nil {
			t.Fatal(err)
		}
	}
	exec(t, pool, `UPDATE copy SET status = 'wishlist', price_cents = NULL WHERE id = 2`)

	var got CopyHistory
	if code := get(t, srv.URL+"/api/valuation/copies/history?title_id=2", &got); code != http.StatusOK {
		t.Fatalf("GET = %d", code)
	}
	want := CopyHistory{Days: 90, History: []CopyDay{
		{CopyID: 2, Day: dbDay(t, pool, 89), MidCents: grosze(4089)},
		{CopyID: 2, Day: dbDay(t, pool, 2), MidCents: grosze(4002)},
		{CopyID: 2, Day: dbDay(t, pool, 0), MidCents: grosze(4000)},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("title 2 = %+v, want %+v", got, want)
	}

	var none CopyHistory
	if code := get(t, srv.URL+"/api/valuation/copies/history?title_id=999", &none); code != http.StatusOK ||
		none.History == nil || len(none.History) != 0 {
		t.Fatalf("unknown title = %d %+v, want 200 with no days", code, none)
	}
	for _, raw := range []string{"", "0", "-1", "abc"} {
		var body map[string]string
		code := get(t, srv.URL+"/api/valuation/copies/history?title_id="+raw, &body)
		if code != http.StatusUnprocessableEntity || body["field"] != "title_id" {
			t.Fatalf("title_id %q = %d %v, want 422 naming title_id", raw, code, body)
		}
	}
}
