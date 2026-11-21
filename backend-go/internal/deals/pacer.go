package deals

import (
	"context"
	"sync"
	"time"
)

// Pacer spaces the requests to each site across runs that share it, such
// as a deal hunt and a valuation run that overlap: two requests to one
// site start at least gap apart. A run on its own never waits for it when
// its own pause between requests is at least gap. A site is held only
// while a request waits for its turn, never during a request, so one run
// never waits for another to finish.
type Pacer struct {
	gap   time.Duration
	mu    sync.Mutex
	sites map[string]*paceSite
}

// paceSite is one site's turn: whoever holds a token in turn may read and
// set last, the start of the site's latest request.
type paceSite struct {
	turn chan struct{}
	last time.Time
}

// NewPacer returns a pacer that keeps requests to one site gap apart.
func NewPacer(gap time.Duration) *Pacer {
	return &Pacer{gap: gap, sites: map[string]*paceSite{}}
}

// Wait blocks until gap has passed since the last request to site
// started, then counts now as the start of the next one. It returns ctx's
// error when ctx ends first.
func (p *Pacer) Wait(ctx context.Context, site string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s := p.site(site)
	select {
	case s.turn <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-s.turn }()
	if !s.last.IsZero() {
		if wait := time.Until(s.last.Add(p.gap)); wait > 0 {
			if err := sleepCtx(ctx, wait); err != nil {
				return err
			}
		}
	}
	s.last = time.Now()
	return nil
}

func (p *Pacer) site(name string) *paceSite {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.sites[name]
	if !ok {
		s = &paceSite{turn: make(chan struct{}, 1)}
		p.sites[name] = s
	}
	return s
}

// Source is src with every search waiting its turn at the pacer first,
// under the source's name.
func (p *Pacer) Source(src Source) Source { return pacedSource{Source: src, pacer: p} }

type pacedSource struct {
	Source
	pacer *Pacer
}

func (s pacedSource) Search(ctx context.Context, query string) ([]Listing, error) {
	if err := s.pacer.Wait(ctx, s.Name()); err != nil {
		return nil, err
	}
	return s.Source.Search(ctx, query)
}
