package deals

import (
	"context"
	"net/http/httptrace"
	"sync"
	"time"
)

// Pacer spaces the requests that different runs send to one site, such as
// a deal hunt and a valuation run that overlap: a request waits until gap
// has passed since another lane's last request to the site was sent. Each
// runner asks through its own lane, whose requests never wait for each
// other: the runner's own pause already spaces them, so a run on its own,
// or right after its previous run, keeps its pacing.
//
// A request holds the site from its turn until it is written, so a
// request still connecting cannot let another lane's request overtake it.
// It never holds the site while waiting for an answer, so one run never
// waits for another to finish.
type Pacer struct {
	gap   time.Duration
	mu    sync.Mutex
	sites map[string]*paceSite
}

// paceSite is one site's turn: whoever holds a token in turn may read and
// set last, when the site's latest request was sent, and lane, which lane
// sent it.
type paceSite struct {
	turn chan struct{}
	last time.Time
	lane *Lane
}

// Lane is one runner's way through a pacer.
type Lane struct{ pacer *Pacer }

// NewPacer returns a pacer that keeps requests of different lanes to one
// site gap apart.
func NewPacer(gap time.Duration) *Pacer {
	return &Pacer{gap: gap, sites: map[string]*paceSite{}}
}

// Lane returns a new lane: requests through it wait only for requests of
// other lanes.
func (p *Pacer) Lane() *Lane { return &Lane{pacer: p} }

// Wait blocks until gap has passed since another lane last sent a request
// to site, then holds the site. The caller sends its request and calls
// the returned func once it is written, or once it gave up, which
// releases the site.
// Wait returns ctx's error, holding nothing, when ctx ends first.
func (l *Lane) Wait(ctx context.Context, site string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s := l.pacer.site(site)
	select {
	case s.turn <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if s.lane != nil && s.lane != l {
		if wait := time.Until(s.last.Add(l.pacer.gap)); wait > 0 {
			if err := sleepCtx(ctx, wait); err != nil {
				<-s.turn
				return nil, err
			}
		}
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			s.last = time.Now()
			s.lane = l
			<-s.turn
		})
	}, nil
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

// Source is src with every search waiting its turn in the lane first,
// under the source's name. The site is released once the search's first
// HTTP request is written, or when the search returns without one.
func (l *Lane) Source(src Source) Source { return pacedSource{Source: src, lane: l} }

type pacedSource struct {
	Source
	lane *Lane
}

func (s pacedSource) Search(ctx context.Context, query string) ([]Listing, error) {
	sent, err := s.lane.Wait(ctx, s.Name())
	if err != nil {
		return nil, err
	}
	defer sent()
	trace := &httptrace.ClientTrace{WroteRequest: func(httptrace.WroteRequestInfo) { sent() }}
	return s.Source.Search(httptrace.WithClientTrace(ctx, trace), query)
}
