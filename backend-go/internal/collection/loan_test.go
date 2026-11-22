package collection_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Automaat/ohara/backend-go/internal/collection"
	"github.com/Automaat/ohara/backend-go/internal/dbtest"
)

// todayInPoland is the calendar day a loan without lent_on starts on.
func todayInPoland(t *testing.T) string {
	t.Helper()
	warsaw, err := time.LoadLocation("Europe/Warsaw")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	return time.Now().In(warsaw).Format(time.DateOnly)
}

func day(d *collection.Date) string {
	if d == nil {
		return ""
	}
	return d.Format(time.DateOnly)
}

func TestLendAndReturnCopy(t *testing.T) {
	a := newAPI(t)
	edition := a.edition(obj{"title_id": a.title("movie", "Rejs"), "format": "dvd"})
	owned := a.copyOf(obj{"edition_id": edition, "status": "owned"})
	wish := a.copyOf(obj{"edition_id": edition, "status": "wishlist"})
	lend := fmt.Sprintf("/api/copies/%d/loans", owned)

	before := todayInPoland(t)
	var first collection.Loan
	a.mustDo(http.MethodPost, lend, obj{"borrower": "  Ania  ", "due_on": "2999-01-31"}, &first, http.StatusCreated)
	if after := todayInPoland(t); first.Borrower != "Ania" || first.CopyID != owned ||
		(day(&first.LentOn) != before && day(&first.LentOn) != after) ||
		day(first.DueOn) != "2999-01-31" || first.ReturnedOn != nil {
		t.Fatalf("loan = %+v, want Ania's open loan from today until 2999-01-31", first)
	}

	a.fails(http.MethodPost, lend, obj{"borrower": "Bartek"}, http.StatusConflict, "")
	a.fails(http.MethodPost, fmt.Sprintf("/api/copies/%d/loans", wish), obj{"borrower": "Ania"}, http.StatusConflict, "")
	a.fails(http.MethodPost, "/api/copies/999999/loans", obj{"borrower": "Ania"}, http.StatusNotFound, "")
	a.fails(http.MethodPut, fmt.Sprintf("/api/copies/%d", owned), obj{"edition_id": edition, "status": "wishlist"},
		http.StatusConflict, "status")

	loan := fmt.Sprintf("/api/loans/%d", first.ID)
	a.fails(http.MethodPut, loan, obj{"returned_on": "2000-01-01"}, http.StatusUnprocessableEntity, "returned_on")
	a.fails(http.MethodPut, loan, obj{}, http.StatusUnprocessableEntity, "returned_on")
	a.fails(http.MethodPut, "/api/loans/999999", obj{"returned_on": "2999-01-01"}, http.StatusNotFound, "")

	var returned collection.Loan
	a.mustDo(http.MethodPut, loan, obj{"returned_on": "2999-02-01"}, &returned, http.StatusOK)
	if returned.ID != first.ID || day(returned.ReturnedOn) != "2999-02-01" || returned.Borrower != "Ania" {
		t.Fatalf("returned loan = %+v, want Ania's loan back on 2999-02-01", returned)
	}

	a.mustDo(http.MethodPut, loan, obj{"returned_on": "2999-02-01"}, &returned, http.StatusOK)
	if day(returned.ReturnedOn) != "2999-02-01" {
		t.Fatalf("repeated return = %+v, want the day kept", returned)
	}
	a.fails(http.MethodPut, loan, obj{"returned_on": "2999-02-02"}, http.StatusConflict, "")
	var kept collection.CopyDetail
	a.mustDo(http.MethodGet, fmt.Sprintf("/api/copies/%d", owned), nil, &kept, http.StatusOK)
	if len(kept.Loans) != 1 || day(kept.Loans[0].ReturnedOn) != "2999-02-01" {
		t.Fatalf("loans after a stale return = %+v, want the first return day kept", kept.Loans)
	}

	var second collection.Loan
	a.mustDo(http.MethodPost, lend, obj{"borrower": "Bartek", "lent_on": "2999-03-01"}, &second, http.StatusCreated)
	if day(&second.LentOn) != "2999-03-01" || second.DueOn != nil {
		t.Fatalf("second loan = %+v, want one from 2999-03-01 without a due date", second)
	}

	var detail collection.CopyDetail
	a.mustDo(http.MethodGet, fmt.Sprintf("/api/copies/%d", owned), nil, &detail, http.StatusOK)
	if len(detail.Loans) != 2 || detail.Loans[0].ID != second.ID || detail.Loans[1].ID != first.ID ||
		detail.Loans[1].ReturnedOn == nil {
		t.Fatalf("loans = %+v, want Bartek's open loan, then Ania's returned one", detail.Loans)
	}
	var wishDetail collection.CopyDetail
	a.mustDo(http.MethodGet, fmt.Sprintf("/api/copies/%d", wish), nil, &wishDetail, http.StatusOK)
	if wishDetail.Loans == nil || len(wishDetail.Loans) != 0 {
		t.Fatalf("wish loans = %#v, want an empty list", wishDetail.Loans)
	}

	a.mustDo(http.MethodDelete, fmt.Sprintf("/api/copies/%d", owned), nil, nil, http.StatusNoContent)
	a.fails(http.MethodPut, loan, obj{"returned_on": "2999-02-02"}, http.StatusNotFound, "")
	a.fails(http.MethodPut, fmt.Sprintf("/api/loans/%d", second.ID), obj{"returned_on": "2999-03-02"},
		http.StatusNotFound, "")
}

func TestLoanValidation(t *testing.T) {
	a := newAPI(t)
	edition := a.edition(obj{"title_id": a.title("book", "Lalka")})
	lend := fmt.Sprintf("/api/copies/%d/loans", a.copyOf(obj{"edition_id": edition, "status": "owned"}))
	yesterday := time.Now().AddDate(0, 0, -2).Format(time.DateOnly)

	tests := []struct {
		name  string
		body  obj
		field string
	}{
		{"blank borrower", obj{"borrower": "   "}, "borrower"},
		{"no borrower", obj{}, "borrower"},
		{"long borrower", obj{"borrower": strings.Repeat("ż", 251)}, "borrower"},
		{"NUL in borrower", obj{"borrower": "An\x00ia"}, "borrower"},
		{"due before lent", obj{"borrower": "Ania", "lent_on": "2026-05-10", "due_on": "2026-05-09"}, "due_on"},
		{"due before today", obj{"borrower": "Ania", "due_on": yesterday}, "due_on"},
		{"bad date", obj{"borrower": "Ania", "lent_on": "10.05.2026"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.field == "" {
				if got := a.do(http.MethodPost, lend, tt.body, nil); got != http.StatusBadRequest &&
					got != http.StatusUnprocessableEntity {
					t.Fatalf("POST %s = %d, want a refusal", lend, got)
				}
				return
			}
			a.fails(http.MethodPost, lend, tt.body, http.StatusUnprocessableEntity, tt.field)
		})
	}

	var l collection.Loan
	longest := obj{"borrower": strings.Repeat("ż", 250), "lent_on": "2026-05-10", "due_on": "2026-05-10"}
	a.mustDo(http.MethodPost, lend, longest, &l, http.StatusCreated)
	a.mustDo(http.MethodPut, fmt.Sprintf("/api/loans/%d", l.ID), obj{"returned_on": "2026-05-10"}, nil, http.StatusOK)
}

func TestDiscardRequestKeepsALentCopy(t *testing.T) {
	a := newAPI(t)
	edition := a.edition(obj{"title_id": a.title("movie", "Miś"), "format": "dvd"})
	var c collection.Copy
	a.mustDo(http.MethodPost, "/api/copies", obj{"edition_id": edition, "status": "owned", "request_key": "k-lent"},
		&c, http.StatusCreated)
	a.mustDo(http.MethodPost, fmt.Sprintf("/api/copies/%d/loans", c.ID), obj{"borrower": "Ania"}, nil, http.StatusCreated)

	a.mustDo(http.MethodDelete, "/api/requests/k-lent", nil, nil, http.StatusNoContent)
	a.mustDo(http.MethodGet, fmt.Sprintf("/api/copies/%d", c.ID), nil, nil, http.StatusOK)
}

// lendInTx opens a loan of copyID in a transaction that holds the copy's row
// lock, as LendCopy does, and leaves it uncommitted.
func lendInTx(ctx context.Context, t *testing.T, pool *pgxpool.Pool, copyID int64) func() {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })
	if _, err := tx.Exec(ctx, `SELECT status FROM copy WHERE id = $1 FOR UPDATE`, copyID); err != nil {
		t.Fatalf("lock copy: %v", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO loan (copy_id, borrower, lent_on) VALUES ($1, 'Ania', '2026-05-10')`,
		copyID); err != nil {
		t.Fatalf("insert loan: %v", err)
	}
	return func() {
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit loan: %v", err)
		}
	}
}

// awaitLockWait returns once a statement containing fragment waits on a lock.
func awaitLockWait(ctx context.Context, t *testing.T, pool *pgxpool.Pool, fragment string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting bool
		err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity
			WHERE wait_event_type = 'Lock' AND strpos(query, $1) > 0)`, fragment).Scan(&waiting)
		if err != nil {
			t.Fatalf("poll locks: %v", err)
		}
		if waiting {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no statement with %q waited on a lock", fragment)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func ownedCopy(ctx context.Context, t *testing.T, store *collection.Store, key *string) collection.Copy {
	t.Helper()
	title := collection.TitleInput{Kind: "movie", Name: "Race"}
	if fe := title.Normalize(); fe != nil {
		t.Fatalf("normalize title: %v", fe)
	}
	created, _, err := store.CreateTitle(ctx, &title)
	if err != nil {
		t.Fatalf("create title: %v", err)
	}
	format := "dvd"
	edition := collection.EditionInput{TitleID: created.ID, Format: &format}
	if fe := edition.Normalize(); fe != nil {
		t.Fatalf("normalize edition: %v", fe)
	}
	e, _, err := store.CreateEdition(ctx, &edition)
	if err != nil {
		t.Fatalf("create edition: %v", err)
	}
	in := collection.CopyInput{EditionID: e.ID, Status: collection.StatusOwned, RequestKey: key}
	if fe := in.Normalize(); fe != nil {
		t.Fatalf("normalize copy: %v", fe)
	}
	c, _, err := store.CreateCopy(ctx, &in)
	if err != nil {
		t.Fatalf("create copy: %v", err)
	}
	return c
}

func TestDiscardWaitingOnALoanKeepsTheCopy(t *testing.T) {
	pool := dbtest.Migrated(t)
	store := collection.NewStore(pool)
	ctx := context.Background()
	key := "race-lend"
	c := ownedCopy(ctx, t, store, &key)
	commit := lendInTx(ctx, t, pool, c.ID)

	done := make(chan error, 1)
	go func() { done <- store.DiscardRequest(ctx, key) }()
	awaitLockWait(ctx, t, pool, "updated_at = created_at FOR UPDATE")
	commit()

	if err := <-done; err != nil {
		t.Fatalf("discard: %v", err)
	}
	loans, err := store.ListLoans(ctx, c.ID)
	if _, getErr := store.GetCopy(ctx, c.ID); getErr != nil || err != nil || len(loans) != 1 {
		t.Fatalf("after the race: copy err %v, loans %v (%v); want the lent copy and its loan", getErr, loans, err)
	}
}

func TestWishChangeWaitingOnALoanIsRefused(t *testing.T) {
	pool := dbtest.Migrated(t)
	store := collection.NewStore(pool)
	ctx := context.Background()
	c := ownedCopy(ctx, t, store, nil)
	commit := lendInTx(ctx, t, pool, c.ID)

	done := make(chan error, 1)
	go func() {
		wish := collection.CopyInput{EditionID: c.EditionID, Status: collection.StatusWishlist}
		_, err := store.UpdateCopy(ctx, c.ID, &wish)
		done <- err
	}()
	awaitLockWait(ctx, t, pool, "SELECT status FROM copy WHERE id = $1 FOR UPDATE")
	commit()

	if err := <-done; !errors.Is(err, collection.ErrLentCopyStays) {
		t.Fatalf("wish change err = %v, want ErrLentCopyStays", err)
	}
	if got, err := store.GetCopy(ctx, c.ID); err != nil || got.Status != collection.StatusOwned {
		t.Fatalf("copy after the race = %+v, %v; want it owned", got, err)
	}
}
