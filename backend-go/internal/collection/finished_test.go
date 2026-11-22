package collection_test

import (
	"fmt"
	"maps"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/Automaat/ohara/backend-go/internal/collection"
)

// finishedOf reads a title's finished state and day ("" when unknown) from
// GET /api/titles/{id}.
func (a *api) finishedOf(id int64) (bool, string) {
	a.t.Helper()
	var got collection.TitleDetail
	a.mustDo(http.MethodGet, fmt.Sprintf("/api/titles/%d", id), nil, &got, http.StatusOK)
	if got.Finished == nil {
		a.t.Fatalf("title %d carries no finished state", id)
	}
	return *got.Finished, day(got.FinishedOn)
}

func (a *api) wantFinished(id int64, finished bool, on string) {
	a.t.Helper()
	if gotFinished, gotOn := a.finishedOf(id); gotFinished != finished || gotOn != on {
		a.t.Fatalf("title %d finished = %v on %q, want %v on %q", id, gotFinished, gotOn, finished, on)
	}
}

func TestTitleFinishedStateIsSetKeptAndCleared(t *testing.T) {
	a := newAPI(t)
	var created collection.Title
	a.mustDo(http.MethodPost, "/api/titles",
		obj{"kind": "book", "name": "Lalka", "finished": true, "finished_on": "2024-03-01"}, &created, http.StatusCreated)
	if created.Finished == nil || !*created.Finished || day(created.FinishedOn) != "2024-03-01" {
		t.Fatalf("created = %v on %q", created.Finished, day(created.FinishedOn))
	}
	a.wantFinished(created.ID, true, "2024-03-01")
	a.wantFinished(a.title("movie", "Rejs"), false, "")

	path := fmt.Sprintf("/api/titles/%d", created.ID)
	body := func(extra obj) obj {
		b := obj{"kind": "book", "name": "Lalka", "notes": "Prus"}
		maps.Copy(b, extra)
		return b
	}
	steps := []struct {
		name     string
		extra    obj
		finished bool
		on       string
	}{
		{"an edit without the fields keeps them", nil, true, "2024-03-01"},
		{"nulls keep them too", obj{"finished": nil, "finished_on": nil}, true, "2024-03-01"},
		{"another day replaces the day", obj{"finished": true, "finished_on": "2025-01-31"}, true, "2025-01-31"},
		{"finished without a day forgets the day", obj{"finished": true}, true, ""},
		{"a day alone marks it finished on that day", obj{"finished_on": "2025-02-01"}, true, "2025-02-01"},
		{"not finished clears the day", obj{"finished": false}, false, ""},
		{"an edit keeps it unfinished", nil, false, ""},
		{"finished with a null day is an unknown day", obj{"finished": true, "finished_on": nil}, true, ""},
		{"today is not in the future", obj{"finished": true, "finished_on": todayInPoland(t)}, true, todayInPoland(t)},
	}
	for _, step := range steps {
		a.mustDo(http.MethodPut, path, body(step.extra), nil, http.StatusOK)
		if gotFinished, gotOn := a.finishedOf(created.ID); gotFinished != step.finished || gotOn != step.on {
			t.Fatalf("%s: finished = %v on %q, want %v on %q", step.name, gotFinished, gotOn, step.finished, step.on)
		}
	}

	var unfinished collection.Title
	a.mustDo(http.MethodPost, "/api/titles", obj{"kind": "music", "name": "Kind of Blue", "finished": false}, &unfinished, http.StatusCreated)
	a.wantFinished(unfinished.ID, false, "")
	var unknownDay collection.Title
	a.mustDo(http.MethodPost, "/api/titles", obj{"kind": "movie", "name": "Miś", "finished": true}, &unknownDay, http.StatusCreated)
	a.wantFinished(unknownDay.ID, true, "")
}

func TestTitleFinishedStateRefusesBadDays(t *testing.T) {
	a := newAPI(t)
	id := a.title("movie", "Miś")
	path := fmt.Sprintf("/api/titles/%d", id)
	a.mustDo(http.MethodPut, path, obj{"kind": "movie", "name": "Miś", "finished_on": "2020-05-05"}, nil, http.StatusOK)

	tomorrow := time.Now().AddDate(0, 0, 2).Format(time.DateOnly)
	for _, bad := range []obj{
		{"finished": true, "finished_on": tomorrow},
		{"finished_on": tomorrow},
		{"finished": false, "finished_on": "2020-01-01"},
	} {
		post := obj{"kind": "movie", "name": "Rejs"}
		put := obj{"kind": "movie", "name": "Miś"}
		for k, v := range bad {
			post[k], put[k] = v, v
		}
		a.fails(http.MethodPost, "/api/titles", post, http.StatusUnprocessableEntity, "finished_on")
		a.fails(http.MethodPut, path, put, http.StatusUnprocessableEntity, "finished_on")
	}
	// A day that is no YYYY-MM-DD fails decoding, as every date field does.
	for _, bad := range []string{"2020-13-01", "yesterday"} {
		a.fails(http.MethodPut, path, obj{"kind": "movie", "name": "Miś", "finished_on": bad}, http.StatusUnprocessableEntity, "body")
	}
	a.wantFinished(id, true, "2020-05-05")

	var titles []collection.Title
	a.mustDo(http.MethodGet, "/api/titles", nil, &titles, http.StatusOK)
	if len(titles) != 1 {
		t.Fatalf("a refused POST stored a title: %+v", titles)
	}
}

func TestCollectionViewUnfinishedFilter(t *testing.T) {
	a := newAPI(t)
	watched := a.title("movie", "Seksmisja")
	a.mustDo(http.MethodPut, fmt.Sprintf("/api/titles/%d", watched),
		obj{"kind": "movie", "name": "Seksmisja", "finished": true, "finished_on": "2024-06-01"}, nil, http.StatusOK)
	watchedDVD := a.copyOf(obj{"edition_id": a.edition(obj{"title_id": watched, "format": "dvd"}), "status": "owned"})
	unread := a.title("book", "Solaris")
	paper := a.edition(obj{"title_id": unread})
	unreadOwned := a.copyOf(obj{"edition_id": paper, "status": "owned", "price_cents": 2550})
	a.copyOf(obj{"edition_id": paper, "status": "wishlist"})
	sold := a.copyOf(obj{"edition_id": paper, "status": "owned"})
	a.mustDo(http.MethodPut, fmt.Sprintf("/api/copies/%d", sold),
		obj{"edition_id": paper, "status": "sold", "sold_on": "2025-01-01"}, nil, http.StatusOK)
	unheard := a.title("music", "Kind of Blue")
	unheardCD := a.copyOf(obj{"edition_id": a.edition(obj{"title_id": unheard, "format": "cd"}), "status": "owned"})

	tests := []struct {
		query string
		want  []int64
	}{
		{"?unfinished=1", []int64{unheardCD, unreadOwned}},
		{"?unfinished=1&kind=book", []int64{unreadOwned}},
		{"?unfinished=1&status=owned", []int64{unheardCD, unreadOwned}},
		{"?unfinished=1&status=wishlist", nil},
		{"?unfinished=1&status=sold", nil},
		{"?unfinished=1&q=seksmisja", nil},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			var view collection.CollectionView
			a.mustDo(http.MethodGet, "/api/collection"+tt.query, nil, &view, http.StatusOK)
			var got []int64
			for _, it := range view.Items {
				got = append(got, it.CopyID)
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("copies = %v, want %v", got, tt.want)
			}
			if view.Totals.Copies != len(tt.want) || view.Totals.Wishlist != 0 {
				t.Fatalf("totals = %+v", view.Totals)
			}
		})
	}

	var view collection.CollectionView
	a.mustDo(http.MethodGet, "/api/collection", nil, &view, http.StatusOK)
	if len(view.Items) != 5 {
		t.Fatalf("items = %+v", view.Items)
	}
	for _, it := range view.Items {
		want := it.CopyID == watchedDVD
		if it.Finished != want || (day(it.FinishedOn) == "2024-06-01") != want {
			t.Fatalf("copy %d finished = %v on %q", it.CopyID, it.Finished, day(it.FinishedOn))
		}
	}

	for _, q := range []string{"?unfinished=0", "?unfinished=true"} {
		a.fails(http.MethodGet, "/api/collection"+q, nil, http.StatusUnprocessableEntity, "unfinished")
	}
}

func TestSetFinishedKeepsANewerEditOfTheOtherFields(t *testing.T) {
	a := newAPI(t)
	id := a.title("book", "Lalka")
	path := fmt.Sprintf("/api/titles/%d", id)
	finished := path + "/finished"
	a.mustDo(http.MethodPut, path, obj{"kind": "book", "name": "Lalka (wyd. 2)", "notes": "Prus", "authors": []string{"Bolesław Prus"}}, nil, http.StatusOK)

	var got collection.Title
	a.mustDo(http.MethodPut, finished, obj{"finished": true, "finished_on": "2025-02-01"}, &got, http.StatusOK)
	if got.Name != "Lalka (wyd. 2)" || got.Notes != "Prus" || len(got.Authors) != 1 || got.Finished == nil || !*got.Finished || day(got.FinishedOn) != "2025-02-01" {
		t.Fatalf("answer = %+v", got)
	}
	a.wantFinished(id, true, "2025-02-01")

	steps := []struct {
		body     obj
		finished bool
		on       string
	}{
		{obj{"finished": true}, true, ""},
		{obj{"finished_on": "2025-03-01"}, true, "2025-03-01"},
		{obj{"finished": true, "finished_on": nil}, true, ""},
		{obj{"finished": false}, false, ""},
		{obj{"finished": true, "finished_on": todayInPoland(t)}, true, todayInPoland(t)},
	}
	for _, step := range steps {
		a.mustDo(http.MethodPut, finished, step.body, nil, http.StatusOK)
		a.wantFinished(id, step.finished, step.on)
	}

	tomorrow := time.Now().AddDate(0, 0, 2).Format(time.DateOnly)
	a.fails(http.MethodPut, finished, obj{}, http.StatusUnprocessableEntity, "finished")
	a.fails(http.MethodPut, finished, obj{"finished": nil, "finished_on": nil}, http.StatusUnprocessableEntity, "finished")
	a.fails(http.MethodPut, finished, obj{"finished": true, "finished_on": tomorrow}, http.StatusUnprocessableEntity, "finished_on")
	a.fails(http.MethodPut, finished, obj{"finished": false, "finished_on": "2020-01-01"}, http.StatusUnprocessableEntity, "finished_on")
	a.wantFinished(id, true, todayInPoland(t))
	a.fails(http.MethodPut, "/api/titles/999999/finished", obj{"finished": true}, http.StatusNotFound, "")

	var detail collection.TitleDetail
	a.mustDo(http.MethodGet, path, nil, &detail, http.StatusOK)
	if detail.Name != "Lalka (wyd. 2)" || detail.Notes != "Prus" || len(detail.Authors) != 1 {
		t.Fatalf("other fields changed: %+v", detail.Title)
	}
}
