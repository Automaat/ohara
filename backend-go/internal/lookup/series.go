package lookup

import "context"

// FilmSeries is the series a catalog groups a film into: its name, the
// film's number by release order among the series' released films (nil
// when the film is not among them) and how many films it has released.
type FilmSeries struct {
	Name   string
	Number *int
	Total  int
}

// SeriesFinder finds the series of the film a catalog ID names. ok is
// false, with a nil error, for a film in no series.
type SeriesFinder interface {
	FilmSeries(ctx context.Context, tmdbID int64) (series FilmSeries, ok bool, err error)
}
