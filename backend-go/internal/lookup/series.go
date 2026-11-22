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

// Film is what a catalog knows of one film for a title being saved: its
// cover, empty when it has none, and its series when InSeries.
type Film struct {
	CoverURL string
	Series   FilmSeries
	InSeries bool
}

// FilmFinder reads the film a catalog ID names. On an error the Film still
// carries what was read before it, such as the cover.
type FilmFinder interface {
	Film(ctx context.Context, tmdbID int64) (Film, error)
}
