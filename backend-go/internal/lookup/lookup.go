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
)

// TitleFields mirrors the title body of POST /api/titles, so a client can
// send a picked candidate's title as is.
type TitleFields struct {
	Name          string   `json:"name"`
	OriginalTitle *string  `json:"original_title,omitempty"`
	PolishTitle   *string  `json:"polish_title,omitempty"`
	Year          *int     `json:"year,omitempty"`
	TMDBID        *int64   `json:"tmdb_id,omitempty"`
	Authors       []string `json:"authors,omitempty"`
	BNID          *string  `json:"bn_id,omitempty"`
	OpenLibraryID *string  `json:"openlibrary_id,omitempty"`
}

// EditionFields mirrors the edition body of POST /api/editions without
// title_id.
type EditionFields struct {
	Barcode     string   `json:"barcode"`
	Label       string   `json:"label,omitempty"`
	Publisher   string   `json:"publisher,omitempty"`
	Year        *int     `json:"year,omitempty"`
	Format      *string  `json:"format,omitempty"`
	PLSubtitles string   `json:"pl_subtitles,omitempty"`
	PLVoiceover string   `json:"pl_voiceover,omitempty"`
	Packaging   []string `json:"packaging,omitempty"`
	Binding     *string  `json:"binding,omitempty"`
}

// Candidate is one possible match for a barcode. Type is "movie" or "book";
// Confidence is in [0, 1].
type Candidate struct {
	Type       string        `json:"type"`
	Title      TitleFields   `json:"title"`
	Edition    EditionFields `json:"edition"`
	Provider   string        `json:"provider"`
	Confidence float64       `json:"confidence"`
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

// Cache stores raw provider responses keyed by provider and barcode.
type Cache interface {
	Get(ctx context.Context, provider, barcode string, maxAge time.Duration) (json.RawMessage, bool, error)
	Put(ctx context.Context, provider, barcode string, raw json.RawMessage) error
}

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
// confidence, then by provider registration order.
type Result struct {
	Barcode    string          `json:"barcode"`
	Candidates []Candidate     `json:"candidates"`
	Errors     []ProviderError `json:"errors"`
}

// Service runs a lookup across every registered provider.
type Service struct {
	registry *Registry
	cache    Cache
	logger   *slog.Logger
	timeout  time.Duration
}

// NewService wires the registry. A nil cache disables caching.
func NewService(registry *Registry, cache Cache, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{registry: registry, cache: cache, logger: logger, timeout: 10 * time.Second}
}

type outcome struct {
	candidates []Candidate
	err        error
}

// Lookup queries every provider concurrently for a normalized barcode. A
// failing provider is reported in Result.Errors and does not fail the lookup.
func (s *Service) Lookup(ctx context.Context, code string) Result {
	outcomes := make([]outcome, len(s.registry.entries))
	var wg sync.WaitGroup
	for i, e := range s.registry.entries {
		wg.Go(func() {
			cands, err := s.query(ctx, e, code)
			outcomes[i] = outcome{candidates: cands, err: err}
		})
	}
	wg.Wait()

	res := Result{Barcode: code, Candidates: []Candidate{}, Errors: []ProviderError{}}
	order := make(map[string]int, len(outcomes))
	for i, o := range outcomes {
		name := s.registry.entries[i].provider.Name()
		order[name] = i
		if o.err != nil {
			s.logger.Warn("lookup provider failed", "provider", name, "barcode", code, "err", o.err)
			res.Errors = append(res.Errors, ProviderError{Provider: name, Error: o.err.Error()})
			continue
		}
		for j := range o.candidates {
			c := o.candidates[j]
			c.Provider = name
			c.Confidence = min(max(c.Confidence, 0), 1)
			res.Candidates = append(res.Candidates, c)
		}
	}
	slices.SortStableFunc(res.Candidates, func(a, b Candidate) int {
		if c := cmp.Compare(b.Confidence, a.Confidence); c != 0 {
			return c
		}
		return cmp.Compare(order[a.Provider], order[b.Provider])
	})
	return res
}

func (s *Service) query(ctx context.Context, e entry, code string) ([]Candidate, error) {
	p := e.provider
	if s.cache != nil && e.ttl > 0 {
		raw, ok, err := s.cache.Get(ctx, p.Name(), code, e.ttl)
		if err != nil {
			s.logger.Warn("lookup cache read failed", "provider", p.Name(), "err", err)
		}
		if ok {
			cands, err := p.Parse(code, raw)
			if err == nil {
				return cands, nil
			}
			s.logger.Warn("cached lookup response unreadable, refetching", "provider", p.Name(), "err", err)
		}
	}
	fetchCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	raw, err := p.Fetch(fetchCtx, code)
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	cands, err := p.Parse(code, raw)
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	if s.cache != nil && e.ttl > 0 {
		if err := s.cache.Put(ctx, p.Name(), code, raw); err != nil {
			s.logger.Warn("lookup cache write failed", "provider", p.Name(), "err", err)
		}
	}
	return cands, nil
}
