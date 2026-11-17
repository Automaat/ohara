package movie

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"

	"github.com/Automaat/ohara/backend-go/internal/barcode"
	"github.com/Automaat/ohara/backend-go/internal/lookup"
)

// Searcher finds TMDB movies for a title; TMDB implements it.
type Searcher interface {
	Search(ctx context.Context, query string, year *int) ([]TMDBMovie, error)
}

// Provider is the "movie" lookup provider: product names from the catalog
// sources, cleaned into titles, searched on TMDB. The cached raw response
// keeps the product names and the TMDB results, so a cache hit re-scores
// without any request.
type Provider struct {
	Sources []NameSource
	TMDB    Searcher
}

// maxQueries bounds TMDB searches per barcode when sources disagree.
const maxQueries = 3

// Raw is the provider's cached response.
type Raw struct {
	Products []Product `json:"products"`
	Searches []Search  `json:"searches"`
}

// Search is one TMDB query and its results. Year is the year the product
// name gives, kept when a fallback search ran without it, so a result from
// another year is still scored down.
type Search struct {
	Query   string      `json:"query"`
	Year    *int        `json:"year,omitempty"`
	Results []TMDBMovie `json:"results"`
}

// Name is "movie".
func (*Provider) Name() string { return "movie" }

// Accepts every barcode except ISBNs: books have their own providers.
func (*Provider) Accepts(code string) bool { return !barcode.IsBook(code) }

// Fetch asks every name source concurrently, then searches TMDB for the
// distinct titles. With no product found, a failed source fails the fetch so
// a "no match" built on an outage is not cached. When another source found
// the product, the answer comes back as a *lookup.PartialError naming the
// failed sources (an Allegro refusal included), so the lookup reports them
// and does not cache the answer.
func (p *Provider) Fetch(ctx context.Context, code string) (json.RawMessage, error) {
	raw := Raw{Products: []Product{}, Searches: []Search{}}
	found := make([][]Product, len(p.Sources))
	errs := make([]error, len(p.Sources))
	var wg sync.WaitGroup
	for i, s := range p.Sources {
		wg.Go(func() { found[i], errs[i] = s.Products(ctx, code) })
	}
	wg.Wait()
	for _, f := range found {
		raw.Products = append(raw.Products, f...)
	}
	if len(raw.Products) == 0 {
		if err := errors.Join(errs...); err != nil {
			return nil, err
		}
		return json.Marshal(raw)
	}

	var searchErrs []error
	for _, q := range queries(raw.Products) {
		s, err := p.search(ctx, q)
		if err != nil {
			searchErrs = append(searchErrs, err)
			continue
		}
		raw.Searches = append(raw.Searches, s)
	}
	if len(raw.Searches) == 0 && len(searchErrs) > 0 {
		return nil, errors.Join(searchErrs...)
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("encode movie response: %w", err)
	}
	if failed := errors.Join(errors.Join(errs...), errors.Join(searchErrs...)); failed != nil {
		return nil, &lookup.PartialError{Raw: data, Err: failed}
	}
	return data, nil
}

// search narrows by year first, then drops the year, then falls back to
// the main title when the full one finds nothing.
func (p *Provider) search(ctx context.Context, q query) (Search, error) {
	attempts := []query{q}
	if q.year != nil {
		attempts = append(attempts, query{title: q.title})
	}
	if short := mainTitle(q.title); short != "" {
		attempts = append(attempts, query{title: short, year: q.year})
	}
	var last Search
	for _, a := range attempts {
		res, err := p.TMDB.Search(ctx, a.title, a.year)
		if err != nil {
			return Search{}, err
		}
		last = Search{Query: a.title, Year: q.year, Results: res}
		if len(res) > 0 {
			break
		}
	}
	return last, nil
}

type query struct {
	title string
	year  *int
}

// queries turns product names into distinct TMDB searches: each name's
// title, then its alternative (usually original) title.
func queries(products []Product) []query {
	var out []query
	seen := map[string]bool{}
	add := func(title string, year *int) {
		key := normalize(title)
		if key == "" || seen[key] || len(out) == maxQueries {
			return
		}
		seen[key] = true
		out = append(out, query{title: title, year: year})
	}
	hints := make([]Hints, len(products))
	for i, pr := range products {
		hints[i] = ParseName(pr.Name)
		add(hints[i].Title, hints[i].Year)
	}
	for _, h := range hints {
		add(h.AltTitle, h.Year)
	}
	return out
}

// Parse scores every TMDB result against the product titles and attaches
// the edition hints of the product names.
func (p *Provider) Parse(code string, data json.RawMessage) ([]lookup.Candidate, error) {
	var raw Raw
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("decode movie response: %w", err)
	}
	edition := editionHints(code, raw.Products)
	var titles []weighted
	for _, pr := range raw.Products {
		h := ParseName(pr.Name)
		titles = append(titles, weighted{h.Title, 1}, weighted{h.AltTitle, 1},
			weighted{mainTitle(h.Title), shortWeight}, weighted{mainTitle(h.AltTitle), shortWeight})
	}

	best := map[int64]int{}
	cands := []lookup.Candidate{}
	for _, s := range raw.Searches {
		for rank, m := range s.Results {
			conf := score(m, rank, s, titles)
			if i, ok := best[m.ID]; ok {
				cands[i].Confidence = max(cands[i].Confidence, conf)
				continue
			}
			best[m.ID] = len(cands)
			cands = append(cands, lookup.Candidate{
				Type:       "movie",
				Title:      titleFields(m),
				Edition:    edition,
				Confidence: conf,
			})
		}
	}
	return cands, nil
}

// Confidence model: title similarity carries the score; a year from the
// product name confirms or contradicts a result; lower TMDB ranks and
// several equally good matches (remakes, same-name films) pull a result
// under lookup.ConfirmThreshold so the user picks.
const (
	simWeight      = 0.9
	yearBonus      = 0.05
	yearPenalty    = 0.3
	rankPenalty    = 0.05
	ambiguousScale = 0.8
	maxConfidence  = 0.98
	shortWeight    = 0.85
)

// weighted is a product title and how much a match on it counts: a match on
// the main title alone (subtitle dropped) never reaches the threshold.
type weighted struct {
	title  string
	weight float64
}

func score(m TMDBMovie, rank int, s Search, titles []weighted) float64 {
	sim := bestSim(m, titles)
	conf := simWeight*sim - rankPenalty*float64(rank)
	y := m.Year()
	switch {
	case s.Year != nil && y != nil && absInt(*s.Year-*y) <= 1:
		conf += yearBonus
	case s.Year != nil && y != nil:
		conf -= yearPenalty
	default:
		alike := 0
		for _, other := range s.Results {
			if bestSim(other, titles) == 1 {
				alike++
			}
		}
		if alike > 1 {
			conf *= ambiguousScale
		}
	}
	return math.Round(min(max(conf, 0), maxConfidence)*100) / 100
}

func bestSim(m TMDBMovie, titles []weighted) float64 {
	best := 0.0
	for _, t := range titles {
		if t.title == "" {
			continue
		}
		best = max(best, t.weight*max(similarity(t.title, m.Title), similarity(t.title, m.OriginalTitle)))
	}
	return best
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func titleFields(m TMDBMovie) lookup.TitleFields {
	name := m.Title
	if name == "" {
		name = m.OriginalTitle
	}
	id := m.ID
	t := lookup.TitleFields{Name: name, Year: m.Year(), TMDBID: &id}
	if m.OriginalTitle != "" {
		orig := m.OriginalTitle
		t.OriginalTitle = &orig
	}
	if m.Title != "" {
		pl := m.Title
		t.PolishTitle = &pl
	}
	return t
}

// editionHints merges what the product names say about the edition. The
// first source that names a format wins; a Polish track or packaging named
// in any product counts, and an explicit absence beats silence.
func editionHints(code string, products []Product) lookup.EditionFields {
	e := lookup.EditionFields{Barcode: code, PLSubtitles: "unknown", PLVoiceover: "unknown"}
	seen := map[string]bool{}
	for _, pr := range products {
		h := ParseName(pr.Name)
		if e.Format == nil {
			e.Format = h.Format
		}
		e.PLSubtitles = mergeTri(e.PLSubtitles, h.PLSubtitles)
		e.PLVoiceover = mergeTri(e.PLVoiceover, h.PLVoiceover)
		for _, pk := range h.Packaging {
			if !seen[pk] {
				seen[pk] = true
				e.Packaging = append(e.Packaging, pk)
			}
		}
	}
	return e
}

func mergeTri(cur, next string) string {
	switch {
	case cur == "yes" || next == "yes":
		return "yes"
	case cur == "no" || next == "no":
		return "no"
	}
	return "unknown"
}
