package collection_test

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/Automaat/ohara/backend-go/internal/collection"
)

// seriesTitle adds a title of kind in series name with number (0 for none).
func (a *api) seriesTitle(kind, title, series string, number int) collection.Title {
	a.t.Helper()
	body := obj{"kind": kind, "name": title, "series_name": series}
	if number > 0 {
		body["series_number"] = number
	}
	var created collection.Title
	a.mustDo(http.MethodPost, "/api/titles", body, &created, http.StatusCreated)
	return created
}

func (a *api) titleOf(id int64) collection.TitleDetail {
	a.t.Helper()
	var detail collection.TitleDetail
	a.mustDo(http.MethodGet, fmt.Sprintf("/api/titles/%d", id), nil, &detail, http.StatusOK)
	return detail
}

func (a *api) seriesList() []collection.Series {
	a.t.Helper()
	var list []collection.Series
	a.mustDo(http.MethodGet, "/api/series", nil, &list, http.StatusOK)
	return list
}

func (a *api) seriesDetail(id int64) collection.SeriesDetail {
	a.t.Helper()
	var detail collection.SeriesDetail
	a.mustDo(http.MethodGet, fmt.Sprintf("/api/series/%d", id), nil, &detail, http.StatusOK)
	return detail
}

func seriesLine(t collection.Title) string {
	if t.SeriesID == nil {
		return "none"
	}
	line := fmt.Sprintf("%d %s", *t.SeriesID, *t.SeriesName)
	if t.SeriesNumber != nil {
		line += fmt.Sprintf(" #%d", *t.SeriesNumber)
	}
	return line
}

func missingLine(missing []collection.MissingVolume) string {
	lines := make([]string, 0, len(missing))
	for _, m := range missing {
		line := fmt.Sprintf("%d %s", m.Number, m.State)
		if m.TitleID != nil {
			line += fmt.Sprintf(" title %d", *m.TitleID)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, ", ")
}

func TestTitleJoinsASeriesOfItsKindByName(t *testing.T) {
	a := newAPI(t)
	first := a.seriesTitle("book", "Ostatnie życzenie", " Wiedźmin ", 1)
	if first.SeriesID == nil || *first.SeriesName != "Wiedźmin" || *first.SeriesNumber != 1 {
		t.Fatalf("created title's series = %s", seriesLine(first))
	}
	second := a.seriesTitle("book", "Miecz przeznaczenia", "WIEDŹMIN", 2)
	if *second.SeriesID != *first.SeriesID || *second.SeriesName != "Wiedźmin" {
		t.Fatalf("a name differing in case made another series: %s vs %s", seriesLine(second), seriesLine(first))
	}
	film := a.seriesTitle("movie", "Wiedźmin", "Wiedźmin", 1)
	if *film.SeriesID == *first.SeriesID {
		t.Fatalf("a film joined the book series %d", *first.SeriesID)
	}
	unnumbered := a.seriesTitle("book", "Sezon burz", "wiedźmin", 0)
	if *unnumbered.SeriesID != *first.SeriesID || unnumbered.SeriesNumber != nil {
		t.Fatalf("an unnumbered title's series = %s", seriesLine(unnumbered))
	}
	if plain := a.titleOf(a.title("book", "Lalka")); plain.SeriesID != nil || plain.SeriesName != nil {
		t.Fatalf("a title without a series = %s", seriesLine(plain.Title))
	}

	path := fmt.Sprintf("/api/titles/%d", second.ID)
	a.mustDo(http.MethodPut, path, obj{"kind": "book", "name": "Miecz przeznaczenia", "notes": "Zbiór"}, nil, http.StatusOK)
	if got := a.titleOf(second.ID); seriesLine(got.Title) != seriesLine(second) {
		t.Fatalf("a PUT without the series fields changed the series to %s", seriesLine(got.Title))
	}
	a.mustDo(http.MethodPut, path, obj{"kind": "book", "name": "Miecz przeznaczenia", "series_name": "Wiedźmin", "series_number": 3}, nil, http.StatusOK)
	if got := a.titleOf(second.ID); *got.SeriesNumber != 3 || *got.SeriesID != *first.SeriesID {
		t.Fatalf("renumbered title = %s", seriesLine(got.Title))
	}
	a.mustDo(http.MethodPut, path, obj{"kind": "book", "name": "Miecz przeznaczenia", "series_name": "Wiedźmin"}, nil, http.StatusOK)
	if got := a.titleOf(second.ID); got.SeriesNumber != nil || *got.SeriesID != *first.SeriesID {
		t.Fatalf("a body naming the series without a number kept %s", seriesLine(got.Title))
	}
	a.mustDo(http.MethodPut, path, obj{"kind": "book", "name": "Miecz przeznaczenia", "series_name": nil}, nil, http.StatusOK)
	if got := a.titleOf(second.ID); got.SeriesID != nil || got.SeriesName != nil || got.SeriesNumber != nil {
		t.Fatalf("a null series_name left %s", seriesLine(got.Title))
	}
	a.mustDo(http.MethodPut, path, obj{"kind": "book", "name": "Miecz przeznaczenia", "series_name": " ", "series_number": nil}, nil, http.StatusOK)
	if got := a.titleOf(second.ID); got.SeriesID != nil {
		t.Fatalf("a blank series_name joined %s", seriesLine(got.Title))
	}
}

func TestTitleSeriesRefusesBadValues(t *testing.T) {
	a := newAPI(t)
	id := a.seriesTitle("book", "Ostatnie życzenie", "Wiedźmin", 1).ID
	path := fmt.Sprintf("/api/titles/%d", id)

	for _, bad := range []any{0, -1, 2.5, "3", true, obj{}, 10001, 1e10} {
		body := obj{"kind": "book", "name": "Lalka", "series_name": "Wiedźmin", "series_number": bad}
		a.fails(http.MethodPost, "/api/titles", body, http.StatusUnprocessableEntity, "series_number")
		a.fails(http.MethodPut, path, body, http.StatusUnprocessableEntity, "series_number")
	}
	a.fails(http.MethodPost, "/api/titles", []byte(`{"kind": "book", "name": "Lalka", "series_name": "Wiedźmin", "series_number": 2.0}`),
		http.StatusUnprocessableEntity, "series_number")
	for _, body := range []obj{
		{"kind": "book", "name": "Lalka", "series_number": 2},
		{"kind": "book", "name": "Lalka", "series_name": nil, "series_number": 2},
		{"kind": "book", "name": "Lalka", "series_name": "  ", "series_number": 2},
	} {
		a.fails(http.MethodPost, "/api/titles", body, http.StatusUnprocessableEntity, "series_number")
		a.fails(http.MethodPut, path, body, http.StatusUnprocessableEntity, "series_number")
	}
	for _, bad := range []any{5, true, []string{"Wiedźmin"}, "Wied\nźmin", "Wied\x00źmin", strings.Repeat("a", 501)} {
		body := obj{"kind": "book", "name": "Lalka", "series_name": bad}
		a.fails(http.MethodPost, "/api/titles", body, http.StatusUnprocessableEntity, "series_name")
		a.fails(http.MethodPut, path, body, http.StatusUnprocessableEntity, "series_name")
	}

	if got := a.titleOf(id); *got.SeriesNumber != 1 || got.Name != "Ostatnie życzenie" {
		t.Fatalf("a refused PUT changed the title: %s", seriesLine(got.Title))
	}
	if list := a.seriesList(); len(list) != 1 || list[0].TitleCount != 1 {
		t.Fatalf("refused saves left series %+v", list)
	}
}

func TestTitleSeriesNumberIsUniqueInItsSeries(t *testing.T) {
	a := newAPI(t)
	first := a.seriesTitle("book", "Ostatnie życzenie", "Wiedźmin", 1)
	a.fails(http.MethodPost, "/api/titles", obj{"kind": "book", "name": "Kopia", "series_name": "wiedźmin", "series_number": 1},
		http.StatusConflict, "series_number")
	second := a.seriesTitle("book", "Miecz przeznaczenia", "Wiedźmin", 2)
	a.fails(http.MethodPut, fmt.Sprintf("/api/titles/%d", second.ID),
		obj{"kind": "book", "name": "Miecz przeznaczenia", "series_name": "Wiedźmin", "series_number": 1},
		http.StatusConflict, "series_number")
	a.mustDo(http.MethodPut, fmt.Sprintf("/api/titles/%d", first.ID),
		obj{"kind": "book", "name": "Ostatnie życzenie", "series_name": "Wiedźmin", "series_number": 1}, nil, http.StatusOK)
	a.seriesTitle("book", "Diuna", "Diuna", 1)
	a.seriesTitle("movie", "Wiedźmin", "Wiedźmin", 1)

	if got := a.titleOf(second.ID); *got.SeriesNumber != 2 {
		t.Fatalf("a refused renumber stored %s", seriesLine(got.Title))
	}
	var titles []collection.Title
	a.mustDo(http.MethodGet, "/api/titles", nil, &titles, http.StatusOK)
	if len(titles) != 4 {
		t.Fatalf("titles after a refused create = %d, want 4", len(titles))
	}
}

func TestSeriesListsVolumesAndMissingNumbers(t *testing.T) {
	a := newAPI(t)
	shelve := func(number int, status string) collection.Title {
		t.Helper()
		title := a.seriesTitle("book", fmt.Sprintf("Tom %d", number), "Saga", number)
		a.copyOf(obj{"edition_id": a.edition(obj{"title_id": title.ID}), "status": status})
		return title
	}
	shelve(1, "owned")
	shelve(2, "owned")
	shelve(4, "owned")
	fifth := shelve(5, "wishlist")
	a.seriesTitle("book", "Opowiadania", "Saga", 0)
	seriesID := *fifth.SeriesID
	path := fmt.Sprintf("/api/series/%d", seriesID)

	a.mustDo(http.MethodPut, path, obj{"total_volumes": 6}, nil, http.StatusOK)
	detail := a.seriesDetail(seriesID)
	if detail.Name != "Saga" || detail.Kind != "book" || *detail.TotalVolumes != 6 || detail.TitleCount != 5 {
		t.Fatalf("series = %+v", detail.Series)
	}
	volumes := make([]string, 0, len(detail.Volumes))
	for _, v := range detail.Volumes {
		number := "-"
		if v.Number != nil {
			number = strconv.Itoa(*v.Number)
		}
		volumes = append(volumes, fmt.Sprintf("%s %s %s", number, v.Name, v.State))
	}
	if got, want := strings.Join(volumes, ", "),
		"1 Tom 1 owned, 2 Tom 2 owned, 4 Tom 4 owned, 5 Tom 5 wishlist, - Opowiadania none"; got != want {
		t.Fatalf("volumes = %q, want %q", got, want)
	}
	if got, want := missingLine(detail.Missing), fmt.Sprintf("3 none, 5 wishlist title %d, 6 none", fifth.ID); got != want {
		t.Fatalf("missing = %q, want %q", got, want)
	}
	if detail.MissingCount != 3 {
		t.Fatalf("missing count = %d, want 3", detail.MissingCount)
	}

	a.mustDo(http.MethodPut, path, obj{"total_volumes": nil}, &detail, http.StatusOK)
	if detail.TotalVolumes != nil || missingLine(detail.Missing) != fmt.Sprintf("3 none, 5 wishlist title %d", fifth.ID) {
		t.Fatalf("series without a total = %+v, missing %q", detail.Series, missingLine(detail.Missing))
	}
	a.mustDo(http.MethodPut, path, obj{"total_volumes": 2}, &detail, http.StatusOK)
	if detail.MissingCount != 2 {
		t.Fatalf("series with a total under its numbers = %q", missingLine(detail.Missing))
	}

	third := a.seriesTitle("book", "Tom 3", "Saga", 3)
	a.copyOf(obj{"edition_id": a.edition(obj{"title_id": third.ID}), "status": "ordered"})
	sixth := a.seriesTitle("book", "Tom 6", "Saga", 6)
	a.copyOf(obj{"edition_id": a.edition(obj{"title_id": sixth.ID}), "status": "sold", "sold_on": "2026-01-02"})
	a.copyOf(obj{"edition_id": a.edition(obj{"title_id": fifth.ID}), "status": "owned"})
	detail = a.seriesDetail(seriesID)
	if got, want := missingLine(detail.Missing), fmt.Sprintf("3 ordered title %d, 6 none title %d", third.ID, sixth.ID); got != want {
		t.Fatalf("missing with an order, a sale and a second edition = %q, want %q", got, want)
	}
}

func TestSeriesListCountsAndDropsEmptySeries(t *testing.T) {
	a := newAPI(t)
	first := a.seriesTitle("book", "Ostatnie życzenie", "Wiedźmin", 1)
	a.copyOf(obj{"edition_id": a.edition(obj{"title_id": first.ID}), "status": "owned"})
	second := a.seriesTitle("book", "Miecz przeznaczenia", "Wiedźmin", 3)
	film := a.seriesTitle("movie", "Casino Royale", "Bond", 21)
	alone := a.seriesTitle("music", "Abbey Road", "Beatles", 0)

	lines := []string{}
	for _, s := range a.seriesList() {
		lines = append(lines, fmt.Sprintf("%s %s %d/%d", s.Name, s.Kind, s.TitleCount, s.MissingCount))
	}
	if got, want := strings.Join(lines, ", "), "Beatles music 1/0, Bond movie 1/21, Wiedźmin book 2/2"; got != want {
		t.Fatalf("series = %q, want %q", got, want)
	}

	a.mustDo(http.MethodPut, fmt.Sprintf("/api/titles/%d", film.ID),
		obj{"kind": "movie", "name": "Casino Royale", "series_name": "James Bond", "series_number": 21}, nil, http.StatusOK)
	a.mustDo(http.MethodPut, fmt.Sprintf("/api/titles/%d", alone.ID),
		obj{"kind": "music", "name": "Abbey Road", "series_name": nil}, nil, http.StatusOK)
	a.mustDo(http.MethodDelete, fmt.Sprintf("/api/titles/%d", second.ID), nil, nil, http.StatusNoContent)

	lines = lines[:0]
	for _, s := range a.seriesList() {
		lines = append(lines, fmt.Sprintf("%s %d", s.Name, s.TitleCount))
	}
	if got, want := strings.Join(lines, ", "), "James Bond 1, Wiedźmin 1"; got != want {
		t.Fatalf("series after titles left = %q, want %q", got, want)
	}
	a.fails(http.MethodGet, fmt.Sprintf("/api/series/%d", *film.SeriesID), nil, http.StatusNotFound, "")
	a.fails(http.MethodPut, fmt.Sprintf("/api/series/%d", *alone.SeriesID), obj{"total_volumes": 3}, http.StatusNotFound, "")

	a.mustDo(http.MethodPut, fmt.Sprintf("/api/series/%d", *first.SeriesID), obj{"total_volumes": 8}, nil, http.StatusOK)
	a.mustDo(http.MethodPut, fmt.Sprintf("/api/titles/%d", first.ID),
		obj{"kind": "book", "name": "Ostatnie życzenie", "series_name": nil}, nil, http.StatusOK)
	again := a.seriesTitle("book", "Krew elfów", "Wiedźmin", 1)
	if detail := a.seriesDetail(*again.SeriesID); *again.SeriesID == *first.SeriesID || detail.TotalVolumes != nil {
		t.Fatalf("a series named again after it went = %+v, the first was %d", detail.Series, *first.SeriesID)
	}
}

func TestSeriesTotalRefusesBadValues(t *testing.T) {
	a := newAPI(t)
	id := *a.seriesTitle("book", "Ostatnie życzenie", "Wiedźmin", 1).SeriesID
	path := fmt.Sprintf("/api/series/%d", id)
	for _, bad := range []any{0, -2, 2.5, "8", true, 10001} {
		a.fails(http.MethodPut, path, obj{"total_volumes": bad}, http.StatusUnprocessableEntity, "total_volumes")
	}
	a.fails(http.MethodPut, path, obj{}, http.StatusUnprocessableEntity, "total_volumes")
	a.fails(http.MethodPut, path, "not an object", http.StatusUnprocessableEntity, "body")
	a.fails(http.MethodPut, "/api/series/999999", obj{"total_volumes": 3}, http.StatusNotFound, "")
	a.fails(http.MethodGet, "/api/series/999999", nil, http.StatusNotFound, "")
	var detail collection.SeriesDetail
	a.mustDo(http.MethodPut, path, obj{"total_volumes": 7}, &detail, http.StatusOK)
	if *detail.TotalVolumes != 7 || detail.MissingCount != 7 {
		t.Fatalf("series with a total of 7 and nothing owned = %+v", detail.Series)
	}
}

func TestTitleOfAnotherKindMovesToTheSeriesOfItsKind(t *testing.T) {
	a := newAPI(t)
	book := a.seriesTitle("book", "Wiedźmin", "Wiedźmin", 1)
	film := a.seriesTitle("movie", "Wiedźmin 2", "Wiedźmin", 2)
	a.mustDo(http.MethodPut, fmt.Sprintf("/api/titles/%d", book.ID), obj{"kind": "movie", "name": "Wiedźmin"}, nil, http.StatusOK)
	got := a.titleOf(book.ID)
	if got.Kind != "movie" || *got.SeriesID != *film.SeriesID || *got.SeriesNumber != 1 {
		t.Fatalf("a title turned into a film = %s, want the film series %d", seriesLine(got.Title), *film.SeriesID)
	}
	if list := a.seriesList(); len(list) != 1 || list[0].Kind != "movie" || list[0].TitleCount != 2 {
		t.Fatalf("series after the kind change = %+v", list)
	}
	clash := a.seriesTitle("book", "Krew elfów", "Wiedźmin", 2)
	a.fails(http.MethodPut, fmt.Sprintf("/api/titles/%d", clash.ID), obj{"kind": "movie", "name": "Krew elfów"},
		http.StatusConflict, "series_number")
	if got := a.titleOf(clash.ID); got.Kind != "book" || *got.SeriesID != *clash.SeriesID {
		t.Fatalf("a refused kind change stored %s %s", got.Kind, seriesLine(got.Title))
	}
}

func TestTitleRequestKeyRepeatMakesNoSeries(t *testing.T) {
	a := newAPI(t)
	body := obj{"kind": "book", "name": "Lalka", "series_name": "Pozytywizm", "series_number": 1, "request_key": "series-add"}
	var first, again collection.Title
	a.mustDo(http.MethodPost, "/api/titles", body, &first, http.StatusCreated)
	body["series_name"] = "Inna seria"
	a.mustDo(http.MethodPost, "/api/titles", body, &again, http.StatusOK)
	if again.ID != first.ID || *again.SeriesName != "Pozytywizm" {
		t.Fatalf("repeat = %s, want title %d in Pozytywizm", seriesLine(again), first.ID)
	}
	if list := a.seriesList(); len(list) != 1 {
		t.Fatalf("a repeated create left series %+v", list)
	}
	a.mustDo(http.MethodDelete, "/api/requests/series-add", nil, nil, http.StatusNoContent)
	if list := a.seriesList(); len(list) != 0 {
		t.Fatalf("a discarded add left series %+v", list)
	}
}

func TestSeriesStaysWhenATitleJoinsAsTheLastLeaves(t *testing.T) {
	a, pool := newAPIWithPool(t)
	ctx := context.Background()
	leaving := a.seriesTitle("book", "Ostatnie życzenie", "Wiedźmin", 1)
	seriesID := *leaving.SeriesID

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT 1 FROM series WHERE id = $1 FOR UPDATE`, seriesID); err != nil {
		t.Fatal(err)
	}
	var joinedID int64
	if err := tx.QueryRow(ctx, `INSERT INTO title (kind, name, series_id, series_number)
		VALUES ('book', 'Miecz przeznaczenia', $1, 2) RETURNING id`, seriesID).Scan(&joinedID); err != nil {
		t.Fatal(err)
	}

	done := make(chan int, 1)
	go func() {
		done <- a.do(http.MethodPut, fmt.Sprintf("/api/titles/%d", leaving.ID),
			obj{"kind": "book", "name": "Ostatnie życzenie", "series_name": nil}, nil)
	}()
	waitForLockWait(t, pool)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if status := <-done; status != http.StatusOK {
		t.Fatalf("leaving the series = %d, want 200", status)
	}
	if detail := a.seriesDetail(seriesID); detail.TitleCount != 1 || detail.Volumes[0].TitleID != joinedID {
		t.Fatalf("series after a join raced the last title leaving = %+v", detail.Volumes)
	}
}

func TestConcurrentMovesBetweenSeriesAllSucceed(t *testing.T) {
	a := newAPI(t)
	for round := range 20 {
		left, right := fmt.Sprintf("Alfa %d", round), fmt.Sprintf("Beta %d", round)
		one := a.seriesTitle("book", fmt.Sprintf("Jeden %d", round), left, 0)
		a.seriesTitle("book", fmt.Sprintf("Trzy %d", round), left, 0)
		two := a.seriesTitle("book", fmt.Sprintf("Dwa %d", round), right, 0)
		a.seriesTitle("book", fmt.Sprintf("Cztery %d", round), right, 0)
		statuses := make(chan int, 2)
		for _, move := range []struct {
			title  collection.Title
			series string
		}{{one, right}, {two, left}} {
			go func() {
				statuses <- a.do(http.MethodPut, fmt.Sprintf("/api/titles/%d", move.title.ID),
					obj{"kind": "book", "name": move.title.Name, "series_name": move.series}, nil)
			}()
		}
		for range 2 {
			if status := <-statuses; status != http.StatusOK {
				t.Fatalf("round %d: moves between two series in opposite directions answered %d, want 200", round, status)
			}
		}
	}
}
