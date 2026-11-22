package lookup

import "unicode/utf8"

// Limits of the POST /api/titles and /api/editions validation, so a picked
// candidate is always accepted as is.
const (
	maxText    = 500
	maxAuthors = 20
	minYear    = 1800
	maxYear    = 2200
	// maxSeriesNumber is the highest number a title has in its series.
	maxSeriesNumber = 10000
)

// fit trims a provider candidate to what the collection accepts: long text
// is cut at a rune boundary, extra authors are dropped and out-of-range years
// are cleared.
func fit(c *Candidate) {
	t := &c.Title
	t.Name = cut(t.Name)
	t.OriginalTitle = cutPtr(t.OriginalTitle)
	t.PolishTitle = cutPtr(t.PolishTitle)
	t.BNID = cutPtr(t.BNID)
	t.OpenLibraryID = cutPtr(t.OpenLibraryID)
	t.Year = yearInRange(t.Year)
	t.SeriesName = cutPtr(t.SeriesName)
	if t.SeriesName == nil || (t.SeriesNumber != nil && (*t.SeriesNumber < 1 || *t.SeriesNumber > maxSeriesNumber)) {
		t.SeriesNumber = nil
	}
	if len(t.Authors) > 0 {
		authors := make([]string, 0, min(len(t.Authors), maxAuthors))
		for _, a := range t.Authors[:min(len(t.Authors), maxAuthors)] {
			authors = append(authors, cut(a))
		}
		t.Authors = authors
	}

	e := &c.Edition
	e.Label = cut(e.Label)
	e.Publisher = cut(e.Publisher)
	e.Year = yearInRange(e.Year)
}

func cut(s string) string {
	if len(s) <= maxText {
		return s
	}
	end := maxText
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end]
}

func cutPtr(s *string) *string {
	if s == nil {
		return nil
	}
	v := cut(*s)
	return &v
}

func yearInRange(y *int) *int {
	if y == nil || *y < minYear || *y > maxYear {
		return nil
	}
	return y
}
