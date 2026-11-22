// Package lookup resolves a barcode to candidate titles and editions through
// a registry of metadata providers. Raw provider responses are cached in
// lookup_cache, so a repeated scan costs no provider request.
package lookup

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/Automaat/ohara/backend-go/internal/metrics"
	"github.com/Automaat/ohara/backend-go/internal/recovery"
)

// TitleFields mirrors the title body of POST /api/titles, so a client can
// send a picked candidate's title as is.
type TitleFields struct {
	Name          string   `json:"name"`
	OriginalTitle *string  `json:"original_title,omitempty" ts:",null"`
	PolishTitle   *string  `json:"polish_title,omitempty" ts:",null"`
	Year          *int     `json:"year,omitempty" ts:",null"`
	TMDBID        *int64   `json:"tmdb_id,omitempty" ts:",null"`
	Authors       []string `json:"authors,omitempty"`
	BNID          *string  `json:"bn_id,omitempty" ts:",null"`
	OpenLibraryID *string  `json:"openlibrary_id,omitempty" ts:",null"`
}

// EditionFields mirrors the edition body of POST /api/editions without
// title_id.
type EditionFields struct {
	Barcode     string   `json:"barcode"`
	Label       string   `json:"label,omitempty"`
	Publisher   string   `json:"publisher,omitempty"`
	Year        *int     `json:"year,omitempty" ts:",null"`
	Format      *string  `json:"format,omitempty" ts:"Format,null"`
	PLSubtitles string   `json:"pl_subtitles,omitempty" ts:"TriState"`
	PLVoiceover string   `json:"pl_voiceover,omitempty" ts:"TriState"`
	Packaging   []string `json:"packaging,omitempty" ts:"Packaging"`
	Binding     *string  `json:"binding,omitempty" ts:"Binding,null"`
	// DiscogsReleaseID names the Discogs release an album candidate is.
	DiscogsReleaseID *int64 `json:"discogs_release_id,omitempty" ts:",null"`
}

// Candidate is one possible match for a barcode. Type is "movie", "book" or
// "music"; Confidence is in [0, 1]. Provider is the main source and Sources
// lists every provider whose fields went into the candidate. CoverURL and
// Description are for display only; they are not stored.
type Candidate struct {
	Type        string        `json:"type" ts:"Kind"`
	Title       TitleFields   `json:"title"`
	Edition     EditionFields `json:"edition"`
	CoverURL    string        `json:"cover_url,omitempty"`
	Description string        `json:"description,omitempty"`
	Provider    string        `json:"provider"`
	Sources     []string      `json:"sources" ts:",null"`
	Confidence  float64       `json:"confidence"`
}

// Provider is one metadata source. Fetch returns the raw response for a
// normalized barcode as JSON (wrap HTML or text in a JSON string), which may
// be a valid "no match" answer; Parse turns a raw response, fresh or cached,
// into candidates.
type Provider interface {
	Name() string
	Fetch(ctx context.Context, barcode string) (json.RawMessage, error)
	Parse(barcode string, raw json.RawMessage) ([]Candidate, error)
}

// Scoped is implemented by providers that only know some barcodes, such as
// book catalogs. The service skips a provider whose Accepts returns false:
// no request, no cache entry and no error.
type Scoped interface {
	Accepts(barcode string) bool
}

// Fallback is implemented by providers that answer some barcodes only when
// the other providers found nothing for them, such as the movie and music
// chains for an ISBN-range code, which book catalogs answer first. A
// provider whose Fallback reports true for a code is asked in a second
// round, only when no provider of the first round returned a candidate; a
// failed first-round provider counts as no answer and keeps its error.
type Fallback interface {
	Fallback(barcode string) bool
}

// Cache stores raw provider responses keyed by provider and barcode.
type Cache interface {
	Get(ctx context.Context, provider, barcode string, maxAge time.Duration) (json.RawMessage, bool, error)
	Put(ctx context.Context, provider, barcode string, raw json.RawMessage) error
}

// PartialError is a Fetch answer built while some of the provider's own
// sources failed. The lookup reports Err next to the candidates parsed from
// Raw and does not cache Raw, so the failure shows on every scan until the
// source recovers or is switched off.
type PartialError struct {
	Raw json.RawMessage
	Err error
}

func (e *PartialError) Error() string { return e.Err.Error() }

func (e *PartialError) Unwrap() error { return e.Err }

// ErrDuplicateProvider reports a second registration under one name.
var ErrDuplicateProvider = errors.New("provider already registered")

type entry struct {
	provider Provider
	ttl      time.Duration
}

// Registry holds the providers in registration order.
type Registry struct {
	entries []entry
}

// Register adds p. A positive ttl caches its responses for that long; zero
// never caches, for providers that are cheap or synthetic.
func (r *Registry) Register(p Provider, ttl time.Duration) error {
	for _, e := range r.entries {
		if e.provider.Name() == p.Name() {
			return fmt.Errorf("%w: %s", ErrDuplicateProvider, p.Name())
		}
	}
	r.entries = append(r.entries, entry{provider: p, ttl: ttl})
	metrics.InitLookupProvider(p.Name())
	return nil
}

// Names lists the registered providers in order.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.entries))
	for _, e := range r.entries {
		names = append(names, e.provider.Name())
	}
	return names
}

// ProviderError reports one provider that failed for a lookup.
type ProviderError struct {
	Provider string `json:"provider"`
	Error    string `json:"error"`
}

// Result is the GET /api/lookup/{barcode} body. Candidates are sorted by
// confidence, then by provider registration order. Book candidates for the
// looked-up ISBN from several providers are merged into one (see
// mergeBooks). When NeedsConfirmation is false, Candidates[0] is the only
// candidate at or above ConfirmThreshold and a client may take it without
// asking; otherwise the user picks (or enters the item by hand when there
// are no candidates).
type Result struct {
	Barcode           string          `json:"barcode"`
	Candidates        []Candidate     `json:"candidates"`
	NeedsConfirmation bool            `json:"needs_confirmation"`
	Errors            []ProviderError `json:"errors"`
}

// ConfirmThreshold is the confidence a lone best candidate needs to be taken
// without a confirm step.
const ConfirmThreshold = 0.8

// SelectionProvider names a candidate the user picked for a barcode. A
// stored selection answers later lookups of that barcode with confidence 1
// and no provider requests.
const SelectionProvider = "selection"

// selectionTTL keeps a selection until the user replaces it.
const selectionTTL = 100 * 365 * 24 * time.Hour

func needsConfirmation(cands []Candidate) bool {
	switch {
	case len(cands) == 0, cands[0].Confidence < ConfirmThreshold:
		return true
	case len(cands) > 1 && cands[1].Confidence >= ConfirmThreshold:
		return true
	}
	return false
}

// Service runs a lookup across every registered provider.
type Service struct {
	registry *Registry
	cache    Cache
	logger   *slog.Logger
	timeout  time.Duration
	now      func() time.Time
	rechecks recheckLog
}

// NewService wires the registry. A nil cache disables caching.
func NewService(registry *Registry, cache Cache, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{registry: registry, cache: cache, logger: logger, timeout: 10 * time.Second, now: time.Now}
}

// recheckLog remembers when a recheck last asked a provider about a
// barcode, so the hourly floor holds while that request is in flight and
// after it failed, when no fresh answer reached the cache.
type recheckLog struct {
	mu sync.Mutex
	at map[string]time.Time
}

// claim records a recheck request for provider and code at now. It reports
// false when one was made less than RecheckAfter ago.
func (l *recheckLog) claim(provider, code string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for key, at := range l.at {
		if now.Sub(at) >= RecheckAfter {
			delete(l.at, key)
		}
	}
	key := provider + "\x00" + code
	if _, ok := l.at[key]; ok {
		return false
	}
	if l.at == nil {
		l.at = map[string]time.Time{}
	}
	l.at[key] = now
	return true
}

type outcome struct {
	candidates []Candidate
	err        error
}

// RecheckAfter is how old a provider's cached answer must be before Recheck
// asks the provider again. The server keeps this floor whatever the client
// sends, so repeated rechecks cost at most one request per provider and
// barcode per period.
const RecheckAfter = time.Hour

// Lookup queries every provider concurrently for a normalized barcode, and
// the providers that are a Fallback for it afterwards, when the others
// returned no candidate. A failing provider is reported in Result.Errors
// and does not fail the lookup.
// A stored selection short-circuits the providers.
func (s *Service) Lookup(ctx context.Context, code string) Result {
	return s.lookup(ctx, code, false)
}

// Recheck is Lookup that asks a provider again when its cached answer is
// older than RecheckAfter, so a source that learned the barcode since the
// last lookup can answer before the cache expires. A fresh answer replaces
// the cached one; a stored selection still answers first. A provider is
// asked at most once per barcode and RecheckAfter, even when that request
// fails or is still in flight; until then the cached answer serves.
func (s *Service) Recheck(ctx context.Context, code string) Result {
	return s.lookup(ctx, code, true)
}

func (s *Service) lookup(ctx context.Context, code string, recheck bool) Result {
	if sel, ok := s.selection(ctx, code); ok {
		return Result{Barcode: code, Candidates: []Candidate{sel}, Errors: []ProviderError{}}
	}
	outcomes := make([]outcome, len(s.registry.entries))
	s.round(ctx, code, recheck, outcomes, false)
	if !slices.ContainsFunc(outcomes, func(o outcome) bool { return len(o.candidates) > 0 }) {
		s.round(ctx, code, recheck, outcomes, true)
	}

	res := Result{Barcode: code, Candidates: []Candidate{}, Errors: []ProviderError{}}
	order := make(map[string]int, len(outcomes))
	for i, o := range outcomes {
		name := s.registry.entries[i].provider.Name()
		order[name] = i
		if o.err != nil {
			s.logger.Warn("lookup provider failed", "provider", name, "barcode", code, "err", o.err)
			res.Errors = append(res.Errors, ProviderError{Provider: name, Error: o.err.Error()})
		}
		for j := range o.candidates {
			c := o.candidates[j]
			c.Provider = name
			c.Sources = []string{name}
			c.Confidence = min(max(c.Confidence, 0), 1)
			fit(&c)
			res.Candidates = append(res.Candidates, c)
		}
	}
	res.Candidates = mergeBooks(code, res.Candidates)
	slices.SortStableFunc(res.Candidates, func(a, b Candidate) int {
		if c := cmp.Compare(b.Confidence, a.Confidence); c != 0 {
			return c
		}
		return cmp.Compare(order[a.Provider], order[b.Provider])
	})
	res.NeedsConfirmation = needsConfirmation(res.Candidates)
	return res
}

// round queries, concurrently, every provider that accepts code and is a
// fallback for it exactly when fallback is set, storing each answer at the
// provider's registry index in outcomes.
func (s *Service) round(ctx context.Context, code string, recheck bool, outcomes []outcome, fallback bool) {
	var wg sync.WaitGroup
	for i, e := range s.registry.entries {
		if scoped, ok := e.provider.(Scoped); ok && !scoped.Accepts(code) {
			continue
		}
		if f, ok := e.provider.(Fallback); (ok && f.Fallback(code)) != fallback {
			continue
		}
		wg.Go(func() {
			cands, err := s.query(ctx, e, code, recheck)
			outcomes[i] = outcome{candidates: cands, err: err}
		})
	}
	wg.Wait()
}

// ErrNoCache reports a selection without a cache to keep it in.
var ErrNoCache = errors.New("lookup cache disabled")

// Select stores the candidate the user picked for a normalized barcode.
func (s *Service) Select(ctx context.Context, code string, c Candidate) (Candidate, error) {
	if s.cache == nil {
		return Candidate{}, ErrNoCache
	}
	c.Provider = SelectionProvider
	c.Confidence = 1
	c.Edition.Barcode = code
	raw, err := json.Marshal(c)
	if err != nil {
		return Candidate{}, fmt.Errorf("encode selection: %w", err)
	}
	if err := s.cache.Put(ctx, SelectionProvider, code, raw); err != nil {
		return Candidate{}, err
	}
	return c, nil
}

func (s *Service) selection(ctx context.Context, code string) (Candidate, bool) {
	if s.cache == nil {
		return Candidate{}, false
	}
	raw, ok, err := s.cache.Get(ctx, SelectionProvider, code, selectionTTL)
	if err != nil {
		s.logger.Warn("lookup selection read failed", "barcode", code, "err", err)
		return Candidate{}, false
	}
	if !ok {
		return Candidate{}, false
	}
	var c Candidate
	if err := json.Unmarshal(raw, &c); err != nil {
		s.logger.Warn("stored lookup selection unreadable", "barcode", code, "err", err)
		return Candidate{}, false
	}
	return c, true
}

func (s *Service) query(ctx context.Context, e entry, code string, recheck bool) ([]Candidate, error) {
	p := e.provider
	if s.cache != nil && e.ttl > 0 {
		maxAge := e.ttl
		if recheck {
			maxAge = min(maxAge, RecheckAfter)
		}
		cands, hit, err := s.cached(ctx, p, code, maxAge)
		if !hit && err == nil && maxAge < e.ttl && !s.rechecks.claim(p.Name(), code, s.now()) {
			cands, hit, err = s.cached(ctx, p, code, e.ttl)
		}
		if hit || err != nil {
			return cands, err
		}
	}
	fetchCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	metrics.ObserveLookupRequest(p.Name())
	raw, err := s.fetch(fetchCtx, p, code)
	if partial, ok := errors.AsType[*PartialError](err); ok {
		metrics.ObserveLookupError(p.Name())
		cands, perr := s.parse(p, code, partial.Raw)
		if perr != nil {
			return nil, fmt.Errorf("parse: %w", perr)
		}
		return cands, fmt.Errorf("partial: %w", partial.Err)
	}
	if err != nil {
		metrics.ObserveLookupError(p.Name())
		return nil, fmt.Errorf("fetch: %w", err)
	}
	cands, err := s.parse(p, code, raw)
	if err != nil {
		metrics.ObserveLookupError(p.Name())
		return nil, fmt.Errorf("parse: %w", err)
	}
	if s.cache != nil && e.ttl > 0 {
		if err := s.cache.Put(ctx, p.Name(), code, raw); err != nil {
			s.logger.Warn("lookup cache write failed", "provider", p.Name(), "err", err)
		}
	}
	return cands, nil
}

// cached parses the provider's cached answer younger than maxAge. It
// reports no hit when there is none or it is unreadable, so the caller
// fetches, and an error when parsing it panicked.
func (s *Service) cached(ctx context.Context, p Provider, code string, maxAge time.Duration) ([]Candidate, bool, error) {
	raw, ok, err := s.cache.Get(ctx, p.Name(), code, maxAge)
	if err != nil {
		s.logger.Warn("lookup cache read failed", "provider", p.Name(), "err", err)
	}
	if !ok {
		return nil, false, nil
	}
	cands, err := s.parse(p, code, raw)
	if err == nil {
		metrics.ObserveLookupCacheHit(p.Name())
		return cands, true, nil
	}
	if _, panicked := errors.AsType[*recovery.PanicError](err); panicked {
		metrics.ObserveLookupError(p.Name())
		return nil, false, fmt.Errorf("parse cached: %w", err)
	}
	s.logger.Warn("cached lookup response unreadable, refetching", "provider", p.Name(), "err", err)
	return nil, false, nil
}

// fetch calls the provider's Fetch; a panic in it, which the router's recoverer
// cannot catch in this goroutine, fails the provider like an error.
func (s *Service) fetch(ctx context.Context, p Provider, code string) (json.RawMessage, error) {
	return recovery.Call(s.logger, "lookup provider panicked", func() (json.RawMessage, error) {
		return p.Fetch(ctx, code)
	}, "provider", p.Name(), "barcode", code, "call", "fetch")
}

// parse calls the provider's Parse; a panic in it fails the provider like
// an unreadable answer.
func (s *Service) parse(p Provider, code string, raw json.RawMessage) ([]Candidate, error) {
	return recovery.Call(s.logger, "lookup provider panicked", func() ([]Candidate, error) {
		return p.Parse(code, raw)
	}, "provider", p.Name(), "barcode", code, "call", "parse")
}
