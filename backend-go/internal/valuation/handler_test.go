package valuation

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Automaat/ohara/backend-go/internal/dbtest"
	"github.com/Automaat/ohara/backend-go/internal/server"
)

// seedPooledBlurays adds to seed a second owned Blu-ray of "Diuna" (23)
// and a wished one (22), both priced with edition 20 as one group, and
// "Solaris", owned without any price.
func seedPooledBlurays(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	exec(t, pool, `INSERT INTO edition (id, title_id, barcode, format) OVERRIDING SYSTEM VALUE
		VALUES (22, 2, '5902600069720', 'bluray'), (23, 2, '5902600069737', 'bluray')`)
	exec(t, pool, `INSERT INTO title (id, kind, name) OVERRIDING SYSTEM VALUE VALUES (3, 'movie', 'Solaris')`)
	exec(t, pool, `INSERT INTO edition (id, title_id, barcode, format) OVERRIDING SYSTEM VALUE
		VALUES (30, 3, '5902600069744', 'dvd')`)
	exec(t, pool, `INSERT INTO copy (edition_id, status, price_cents) VALUES
		(23, 'owned', 4500), (22, 'wishlist', NULL), (30, 'owned', 2000)`)
	exec(t, pool, `INSERT INTO price_observation
			(source, external_id, price_cents, edition_id, acquired_as, observed_at, last_seen_at)
		VALUES ('olx', 'k1', 2000, 10, 'used', now(), now()),
		       ('olx', 'k2', 2600, 10, 'used', now(), now()),
		       ('skupszop', '9788308073209', 537, 10, NULL, now(), now()),
		       ('olx', 'b1', 4000, 20, 'used', now(), now()),
		       ('olx', 'b2', 4400, 20, 'used', now(), now()),
		       ('olx', 'b3', 9900, 23, 'new', now(), now()),
		       ('olx', 'b4', 4800, 23, 'used', now(), now()),
		       ('olx', 'b5', 5000, 22, 'used', now(), now()),
		       ('olx', 'old', 100, 23, 'used', now() - interval '100 days', now() - interval '100 days'),
		       ('olx', 'd1', 1500, 21, 'used', now(), now())`)
}

func TestTitleCopiesAPI(t *testing.T) {
	pool := dbtest.Migrated(t)
	seed(t, pool)
	seedPooledBlurays(t, pool)
	logger := slog.New(slog.DiscardHandler)
	store := NewStore(pool)
	runner := NewRunner(store, nil, nil, nil, Config{Window: 90 * 24 * time.Hour}, logger)
	srv := httptest.NewServer(server.New(server.Config{}, logger,
		server.Deps{DB: pool, API: []server.Mounter{NewHandler(store, runner, logger)}}))
	t.Cleanup(srv.Close)

	all, _, err := store.Valued(context.Background(), runner.Window())
	if err != nil {
		t.Fatalf("Valued: %v", err)
	}
	for _, title := range []int64{1, 2, 3} {
		want := []CopyValue{}
		for _, c := range all {
			if c.TitleID == title {
				want = append(want, c)
			}
		}
		var got TitleCopies
		url := srv.URL + "/api/valuation/copies?title_id=" + strconv.FormatInt(title, 10)
		if code := get(t, url, &got); code != http.StatusOK {
			t.Fatalf("GET title %d = %d", title, code)
		}
		if len(got.Copies) == 0 || !reflect.DeepEqual(got.Copies, want) {
			t.Fatalf("title %d copies = %+v, want the full view's %+v", title, got.Copies, want)
		}
	}

	var diuna TitleCopies
	get(t, srv.URL+"/api/valuation/copies?title_id=2", &diuna)
	for _, c := range diuna.Copies {
		if c.Mid == nil || c.Mid.Count != 4 {
			t.Fatalf("Diuna copy %d mid = %+v, want the 4 used Blu-ray prices of editions 20, 22 and 23", c.CopyID, c.Mid)
		}
	}
	var solaris TitleCopies
	get(t, srv.URL+"/api/valuation/copies?title_id=3", &solaris)
	if c := solaris.Copies[0]; c.Floor != nil || c.Mid != nil || c.Ceiling != nil {
		t.Fatalf("Solaris copy = %+v, want no value without prices", c)
	}

	var none TitleCopies
	if code := get(t, srv.URL+"/api/valuation/copies?title_id=999", &none); code != http.StatusOK ||
		none.Copies == nil || len(none.Copies) != 0 {
		t.Fatalf("unknown title = %d %+v, want 200 with no copies", code, none)
	}

	for _, raw := range []string{"", "0", "-1", "abc", "1.5", "99999999999999999999"} {
		var body map[string]string
		code := get(t, srv.URL+"/api/valuation/copies?title_id="+raw, &body)
		if code != http.StatusUnprocessableEntity || body["field"] != "title_id" {
			t.Fatalf("title_id %q = %d %v, want 422 naming title_id", raw, code, body)
		}
	}
}
