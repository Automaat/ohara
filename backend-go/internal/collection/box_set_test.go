package collection_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Automaat/ohara/backend-go/internal/collection"
)

// contentIDs lists the IDs of the titles an edition answer holds, in order.
func contentIDs(e collection.Edition) []int64 {
	ids := make([]int64, 0, len(e.Contents))
	for _, c := range e.Contents {
		ids = append(ids, c.ID)
	}
	return ids
}

func TestBoxSetContents(t *testing.T) {
	a := newAPI(t)
	trilogy := a.title("movie", "Władca Pierścieni: Trylogia")
	fellowship := a.title("movie", "Drużyna Pierścienia")
	towers := a.title("movie", "Dwie wieże")
	book := a.title("book", "Hobbit")
	box := a.edition(obj{"title_id": trilogy, "format": "bluray"})
	path := fmt.Sprintf("/api/editions/%d", box)
	body := func(contents any) obj {
		return obj{"title_id": trilogy, "format": "bluray", "contents": contents}
	}

	var e collection.Edition
	a.mustDo(http.MethodPut, path, body([]int64{towers, fellowship, towers, book}), &e, http.StatusOK)
	if got, want := contentIDs(e), []int64{towers, fellowship, book}; !slices.Equal(got, want) {
		t.Fatalf("PUT contents = %v, want %v", got, want)
	}
	var detail collection.EditionDetail
	a.mustDo(http.MethodGet, path, nil, &detail, http.StatusOK)
	if got, want := contentIDs(detail.Edition), []int64{towers, fellowship, book}; !slices.Equal(got, want) {
		t.Fatalf("GET contents = %v, want %v", got, want)
	}
	if c := detail.Contents[0]; c.Name != "Dwie wieże" || c.Kind != "movie" {
		t.Fatalf("first content = %+v", c)
	}
	if c := detail.Contents[2]; c.Name != "Hobbit" || c.Kind != "book" {
		t.Fatalf("book content = %+v", c)
	}

	a.mustDo(http.MethodPut, path, obj{"title_id": trilogy, "format": "bluray", "label": "Steelbook"}, &e, http.StatusOK)
	if got := contentIDs(e); len(got) != 3 {
		t.Fatalf("contents after a PUT without them = %v", got)
	}

	a.fails(http.MethodPut, path, body([]int64{fellowship, 999999}), http.StatusUnprocessableEntity, "contents")
	a.fails(http.MethodPut, path, body([]int64{trilogy}), http.StatusUnprocessableEntity, "contents")
	a.fails(http.MethodPut, path, body([]int64{0}), http.StatusUnprocessableEntity, "contents")
	a.fails(http.MethodPut, path, obj{"title_id": towers, "format": "bluray"}, http.StatusUnprocessableEntity, "contents")
	a.fails(http.MethodPost, "/api/editions", obj{"title_id": towers, "format": "dvd", "contents": []int64{towers}},
		http.StatusUnprocessableEntity, "contents")
	a.mustDo(http.MethodGet, path, nil, &detail, http.StatusOK)
	if got := contentIDs(detail.Edition); len(got) != 3 || detail.TitleID != trilogy {
		t.Fatalf("refused PUTs changed the edition: title %d, contents %v", detail.TitleID, got)
	}

	a.fails(http.MethodDelete, fmt.Sprintf("/api/titles/%d", towers), nil, http.StatusConflict, "")
	var cleared collection.Edition
	a.mustDo(http.MethodPut, path, body([]int64{}), &cleared, http.StatusOK)
	if len(cleared.Contents) != 0 {
		t.Fatalf("contents after an empty list = %v", contentIDs(cleared))
	}
	a.mustDo(http.MethodDelete, fmt.Sprintf("/api/titles/%d", towers), nil, nil, http.StatusNoContent)

	var created collection.Edition
	a.mustDo(http.MethodPost, "/api/editions", obj{"title_id": trilogy, "format": "dvd", "contents": []int64{book, fellowship}},
		&created, http.StatusCreated)
	if got, want := contentIDs(created), []int64{book, fellowship}; !slices.Equal(got, want) {
		t.Fatalf("POST contents = %v, want %v", got, want)
	}
	a.fails(http.MethodDelete, fmt.Sprintf("/api/titles/%d", fellowship), nil, http.StatusConflict, "")
	a.mustDo(http.MethodDelete, fmt.Sprintf("/api/editions/%d", created.ID), nil, nil, http.StatusNoContent)
	a.mustDo(http.MethodDelete, fmt.Sprintf("/api/titles/%d", fellowship), nil, nil, http.StatusNoContent)
}

func TestTitleInBoxSet(t *testing.T) {
	a := newAPI(t)
	trilogy := a.title("movie", "Władca Pierścieni: Trylogia")
	towers := a.title("movie", "Dwie wieże")
	box := a.edition(obj{"title_id": trilogy, "format": "bluray", "contents": []int64{towers}})
	wishedBox := a.title("movie", "Hobbit: Trylogia")
	wished := a.edition(obj{"title_id": wishedBox, "format": "dvd", "contents": []int64{towers}})
	a.copyOf(obj{"edition_id": wished, "status": "wishlist"})

	var detail collection.TitleDetail
	a.mustDo(http.MethodGet, fmt.Sprintf("/api/titles/%d", towers), nil, &detail, http.StatusOK)
	want := []collection.BoxSet{
		{EditionID: wished, TitleID: wishedBox, Name: "Hobbit: Trylogia"},
		{EditionID: box, TitleID: trilogy, Name: "Władca Pierścieni: Trylogia"},
	}
	if !slices.Equal(detail.BoxSets, want) || detail.InOwnedBoxSet {
		t.Fatalf("box sets = %+v (owned %v), want %+v", detail.BoxSets, detail.InOwnedBoxSet, want)
	}

	a.copyOf(obj{"edition_id": box, "status": "owned", "price_cents": 9999})
	a.mustDo(http.MethodGet, fmt.Sprintf("/api/titles/%d", towers), nil, &detail, http.StatusOK)
	want[1].Owned = true
	if !slices.Equal(detail.BoxSets, want) || !detail.InOwnedBoxSet || detail.CopyCount != 0 {
		t.Fatalf("box sets = %+v (owned %v, copies %d), want %+v", detail.BoxSets, detail.InOwnedBoxSet, detail.CopyCount, want)
	}

	var titles []collection.Title
	a.mustDo(http.MethodGet, "/api/titles", nil, &titles, http.StatusOK)
	for _, title := range titles {
		if title.InOwnedBoxSet != (title.ID == towers) {
			t.Fatalf("title %q in_owned_box_set = %v", title.Name, title.InOwnedBoxSet)
		}
	}
	var trilogyDetail collection.TitleDetail
	a.mustDo(http.MethodGet, fmt.Sprintf("/api/titles/%d", trilogy), nil, &trilogyDetail, http.StatusOK)
	if len(trilogyDetail.BoxSets) != 0 || len(trilogyDetail.Editions[0].Contents) != 1 {
		t.Fatalf("box set title = %+v", trilogyDetail)
	}
}

func TestCollectionSearchFindsBoxSetContents(t *testing.T) {
	a := newAPI(t)
	trilogy := a.title("movie", "Władca Pierścieni: Trylogia")
	towers := a.title("movie", "Dwie wieże")
	a.mustDo(http.MethodPut, fmt.Sprintf("/api/titles/%d", towers),
		obj{"kind": "movie", "name": "Dwie wieże", "original_title": "The Two Towers"}, nil, http.StatusOK)
	box := a.edition(obj{"title_id": trilogy, "format": "bluray", "contents": []int64{towers}})
	boxCopy := a.copyOf(obj{"edition_id": box, "status": "owned", "price_cents": 9999})
	other := a.edition(obj{"title_id": a.title("movie", "Seksmisja"), "format": "dvd"})
	a.copyOf(obj{"edition_id": other, "status": "owned"})

	dated := a.title("movie", "2001-09-11")
	a.mustDo(http.MethodPut, fmt.Sprintf("/api/editions/%d", box),
		obj{"title_id": trilogy, "format": "bluray", "contents": []int64{towers, dated}}, nil, http.StatusOK)

	for _, q := range []string{"dwie WIEŻE", "two towers", "2001-09-11"} {
		var view collection.CollectionView
		a.mustDo(http.MethodGet, "/api/collection?q="+url.QueryEscape(q), nil, &view, http.StatusOK)
		if len(view.Items) != 1 || view.Items[0].CopyID != boxCopy {
			t.Fatalf("q=%q items = %+v", q, view.Items)
		}
		if view.Totals != (collection.Totals{Copies: 1, Owned: 1, Priced: 1, SpentCents: 9999}) {
			t.Fatalf("q=%q totals = %+v", q, view.Totals)
		}
		if c := view.Items[0].Contents; len(c) != 2 || c[0].ID != towers || *c[0].OriginalTitle != "The Two Towers" {
			t.Fatalf("q=%q contents = %+v", q, c)
		}
	}
}

func TestSavingContentsWhileAListedTitleIsDeleted(t *testing.T) {
	a, pool := newAPIWithPool(t)
	ctx := context.Background()
	trilogy := a.title("movie", "Władca Pierścieni: Trylogia")
	towers := a.title("movie", "Dwie wieże")
	king := a.title("movie", "Powrót króla")
	box := a.edition(obj{"title_id": trilogy, "format": "bluray", "contents": []int64{towers}})

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT 1 FROM title WHERE id = $1 FOR UPDATE`, towers); err != nil {
		t.Fatal(err)
	}
	done := make(chan int, 1)
	go func() {
		done <- a.do(http.MethodPut, fmt.Sprintf("/api/editions/%d", box),
			obj{"title_id": trilogy, "format": "bluray", "contents": []int64{towers, king}}, nil)
	}()
	waitForLockWait(t, pool)
	_, err = tx.Exec(ctx, `DELETE FROM title WHERE id = $1`, towers)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23001" {
		t.Fatalf("delete of a listed title while contents are saved = %v, want restrict violation 23001", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if status := <-done; status != http.StatusOK {
		t.Fatalf("contents save = %d, want 200", status)
	}
	var detail collection.EditionDetail
	a.mustDo(http.MethodGet, fmt.Sprintf("/api/editions/%d", box), nil, &detail, http.StatusOK)
	if got, want := contentIDs(detail.Edition), []int64{towers, king}; !slices.Equal(got, want) {
		t.Fatalf("contents = %v, want %v", got, want)
	}
}
