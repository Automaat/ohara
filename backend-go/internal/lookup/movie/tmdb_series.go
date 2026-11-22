package movie

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Automaat/ohara/backend-go/internal/lookup"
)

// tmdbCollectionPart is one film of a TMDB collection.
type tmdbCollectionPart struct {
	ID          int64  `json:"id"`
	ReleaseDate string `json:"release_date"`
}

// FilmSeries finds the TMDB collection of film tmdbID, in Polish, with two
// requests: /3/movie/{id} names the collection and /3/collection/{id} lists
// its films. A film in no collection, or in one without a name, is not ok
// after the first request. Only films released by today count: one with no
// release date or a later one is neither numbered nor counted.
func (t TMDB) FilmSeries(ctx context.Context, tmdbID int64) (lookup.FilmSeries, bool, error) {
	var film struct {
		Collection *struct {
			ID int64 `json:"id"`
		} `json:"belongs_to_collection"`
	}
	if err := t.getJSON(ctx, "/3/movie/"+strconv.FormatInt(tmdbID, 10), &film); err != nil {
		return lookup.FilmSeries{}, false, err
	}
	succeeded("tmdb")
	if film.Collection == nil || film.Collection.ID <= 0 {
		return lookup.FilmSeries{}, false, nil
	}
	var coll struct {
		Name  string                `json:"name"`
		Parts *[]tmdbCollectionPart `json:"parts"`
	}
	if err := t.getJSON(ctx, "/3/collection/"+strconv.FormatInt(film.Collection.ID, 10), &coll); err != nil {
		return lookup.FilmSeries{}, false, err
	}
	if coll.Parts == nil {
		return lookup.FilmSeries{}, false, parseFailed("tmdb", fmt.Errorf("collection without parts"))
	}
	succeeded("tmdb")
	name := seriesName(coll.Name)
	if name == "" {
		return lookup.FilmSeries{}, false, nil
	}
	released := releasedParts(*coll.Parts, time.Now().UTC().Format(time.DateOnly))
	series := lookup.FilmSeries{Name: name, Total: len(released)}
	for i, p := range released {
		if p.ID == tmdbID {
			n := i + 1
			series.Number = &n
			break
		}
	}
	return series, true, nil
}

// getJSON reads one TMDB answer for path, in Polish, into v.
func (t TMDB) getJSON(ctx context.Context, path string, v any) error {
	resp, err := t.get(ctx, path, url.Values{"language": {"pl-PL"}})
	if err != nil {
		return err
	}
	if err := json.Unmarshal(resp.Body, v); err != nil {
		return parseFailed("tmdb", err)
	}
	return nil
}

// releasedParts keeps the parts released by today, a YYYY-MM-DD date, in
// release order, earlier TMDB IDs first on the same day.
func releasedParts(parts []tmdbCollectionPart, today string) []tmdbCollectionPart {
	out := make([]tmdbCollectionPart, 0, len(parts))
	for _, p := range parts {
		if _, err := time.Parse(time.DateOnly, p.ReleaseDate); err != nil || p.ReleaseDate > today {
			continue
		}
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b tmdbCollectionPart) int {
		return cmp.Or(strings.Compare(a.ReleaseDate, b.ReleaseDate), cmp.Compare(a.ID, b.ID))
	})
	return out
}

// collectionSuffixes are the words TMDB adds to a collection's name to say
// it is one, in Polish and in English, which a series name goes without.
var collectionSuffixes = []string{" - kolekcja", " – kolekcja", " (kolekcja)", " kolekcja", " collection"}

// seriesName is a collection's name without the word "kolekcja" or
// "Collection" TMDB ends it with, unless nothing else is left.
func seriesName(name string) string {
	name = strings.Join(strings.Fields(name), " ")
	for _, suffix := range collectionSuffixes {
		cut := len(name) - len(suffix)
		if cut <= 0 || !strings.EqualFold(name[cut:], suffix) {
			continue
		}
		if rest := strings.TrimSpace(name[:cut]); rest != "" {
			return rest
		}
	}
	return name
}
