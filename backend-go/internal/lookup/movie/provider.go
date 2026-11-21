package movie

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/Automaat/ohara/backend-go/internal/barcode"
	"github.com/Automaat/ohara/backend-go/internal/lookup"
	"github.com/Automaat/ohara/backend-go/internal/recovery"
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
	// Fallbacks are asked only when Sources name no product with a
	// searchable title, or have not answered within fallbackAfter, so a
	// source with a small request quota (UPCitemdb) spends it only where it
	// helps.
	Fallbacks []NameSource
	TMDB      Searcher
	// Logger records a name source's recovered panic; nil logs to
	// slog.Default().
	Logger *slog.Logger
	// Now is the clock for rate-limit pauses; nil means time.Now.
	Now func() time.Time

	// fallbackAfter overrides defaultFallbackAfter in tests.
	fallbackAfter time.Duration

	mu sync.Mutex
	// paused holds, per source name, when a source that answered with a
	// *RateLimitError takes requests again.
	paused map[string]time.Time
}

// limitPause is how long a source rests after a rate-limit answer that does
// not say when the limit resets. A reset more than maxLimitReset ahead is
// not a plausible Unix time for a daily limit and counts as unreadable.
const (
	limitPause    = time.Hour
	maxLimitReset = 48 * time.Hour
)

// defaultFallbackAfter is how long the fallbacks wait for a slow source
// before they are asked anyway, so a slow catalog miss does not leave them
// too little of the lookup's deadline (10 s, with 8 s per request).
const defaultFallbackAfter = 3 * time.Second

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

// Fetch asks every name source concurrently, and the fallbacks only when
// those name nothing searchable or are slow, then searches TMDB for the
// distinct titles. A
// source paused for its rate limit is skipped and counts as failed. With no
// product found, a failed source fails the fetch so a "no match" built on an
// outage is not cached. When another source found the product, the answer
// comes back as a *lookup.PartialError naming the failed sources (an Allegro
// refusal included), so the lookup reports them and does not cache the
// answer.
func (p *Provider) Fetch(ctx context.Context, code string) (json.RawMessage, error) {
	raw := Raw{Products: []Product{}, Searches: []Search{}}
	products, errs := p.names(ctx, code)
	raw.Products = append(raw.Products, products...)
	if len(raw.Products) == 0 {
		if errs != nil {
			return nil, errs
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
	if failed := errors.Join(errs, errors.Join(searchErrs...)); failed != nil {
		return nil, &lookup.PartialError{Raw: data, Err: failed}
	}
	return data, nil
}

// answer is what a group of name sources found.
type answer struct {
	products []Product
	err      error
}

// names asks the sources, then the fallbacks when the sources name no
// searchable title. When the sources take longer than fallbackAfter, the
// fallbacks are asked in parallel; their failure then counts only if the
// sources name nothing searchable either.
func (p *Provider) names(ctx context.Context, code string) ([]Product, error) {
	if len(p.Fallbacks) == 0 {
		return p.ask(ctx, nil, p.Sources, 0, code)
	}
	group := func(sources []NameSource, first int) <-chan answer {
		ch := make(chan answer, 1)
		go func() {
			found, err := p.ask(ctx, nil, sources, first, code)
			ch <- answer{found, err}
		}()
		return ch
	}
	primary := group(p.Sources, 0)
	wait := p.fallbackAfter
	if wait <= 0 {
		wait = defaultFallbackAfter
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	var fromSources, fromFallbacks answer
	select {
	case fromSources = <-primary:
		if len(queries(fromSources.products)) > 0 {
			return fromSources.products, fromSources.err
		}
		fromFallbacks = <-group(p.Fallbacks, len(p.Sources))
	case <-timer.C:
		fallback := group(p.Fallbacks, len(p.Sources))
		fromSources, fromFallbacks = <-primary, <-fallback
		if len(queries(fromSources.products)) > 0 {
			fromFallbacks.err = nil
		}
	}
	return append(fromSources.products, fromFallbacks.products...), errors.Join(fromSources.err, fromFallbacks.err)
}

// ask asks sources concurrently and appends what they name to out. first
// is the position of sources[0] among all the provider's name sources.
func (p *Provider) ask(ctx context.Context, out []Product, sources []NameSource, first int, code string) ([]Product, error) {
	found := make([][]Product, len(sources))
	errs := make([]error, len(sources))
	var wg sync.WaitGroup
	for i, s := range sources {
		wg.Go(func() { found[i], errs[i] = p.products(ctx, first+i, s, code) })
	}
	wg.Wait()
	for _, f := range found {
		out = append(out, f...)
	}
	return out, errors.Join(errs...)
}

// products asks one name source, unless it is paused for its rate limit. A
// panic in it, its name included, fails that source with an error naming
// it, or naming its position when the name itself panicked.
func (p *Provider) products(ctx context.Context, i int, s NameSource, code string) ([]Product, error) {
	name, err := recovery.Call(p.Logger, "movie name source panicked", func() (string, error) {
		return s.Name(), nil
	}, "source_index", i, "barcode", code)
	if err != nil {
		return nil, fmt.Errorf("name source %d: %w", i, err)
	}
	if until, ok := p.pausedUntil(name); ok {
		return nil, fmt.Errorf("%s: %w, skipped until %s", name, ErrQuotaExceeded, until.UTC().Format(time.RFC3339))
	}
	found, err := recovery.Call(p.Logger, "movie name source panicked", func() ([]Product, error) {
		return s.Products(ctx, code)
	}, "source", name, "barcode", code)
	if pe, ok := errors.AsType[*recovery.PanicError](err); ok {
		return nil, fmt.Errorf("%s: %w", name, pe)
	}
	if rl, ok := errors.AsType[*RateLimitError](err); ok {
		p.pause(name, rl.Reset)
	}
	return found, err
}

func (p *Provider) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// pause stops requests to a source until reset, or for limitPause when
// reset is unknown or implausibly far ahead. A reset already past needs no
// pause.
func (p *Provider) pause(name string, reset time.Time) {
	now := p.now()
	until := reset
	if reset.IsZero() || reset.After(now.Add(maxLimitReset)) {
		until = now.Add(limitPause)
	}
	if !until.After(now) {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.paused == nil {
		p.paused = map[string]time.Time{}
	}
	if until.After(p.paused[name]) {
		p.paused[name] = until
	}
}

// pausedUntil reports when a paused source takes requests again.
func (p *Provider) pausedUntil(name string) (time.Time, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	until, ok := p.paused[name]
	if !ok {
		return time.Time{}, false
	}
	if !p.now().Before(until) {
		delete(p.paused, name)
		return time.Time{}, false
	}
	return until, true
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
