package deals

import (
	"context"
	"net"
	"net/http"
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
// A request holds the site from its turn until it is sent, so a request
// still connecting cannot let another lane's request overtake it. It
// never holds the site while waiting for an answer, so one run never
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
// the returned func once it is sent, or once it gave up, which releases
// the site.
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

// Transport is base with every request waiting its turn in the lane
// first, under the request's host; base is http.DefaultTransport when nil.
// Each request a client sends through it is paced, a redirect's next
// request too.
//
// When base is an *http.Transport that dials with DialContext and leaves
// TLS to itself, as http.DefaultTransport does, Transport uses a clone of
// it with connections of its own, and a request is sent once it is
// written and a write to its connection has ended since the request got
// it, whichever comes last. HTTP/1 reports a request written while it
// still sits in the connection's buffer and flushes it only afterwards;
// HTTP/2 flushes it first. With any other base a request is sent once
// base reports it written. Either way the round trip ending first, a
// failed write too, releases the site.
func (l *Lane) Transport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	if t, ok := base.(*http.Transport); ok && t.DialContext != nil && t.DialTLSContext == nil {
		base = watchWrites(t)
	}
	return pacedTransport{base: base, lane: l}
}

type pacedTransport struct {
	base http.RoundTripper
	lane *Lane
}

func (t pacedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	sent, err := t.lane.Wait(req.Context(), req.URL.Host)
	if err != nil {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, err
	}
	defer sent()
	w := &sendWatch{sent: sent}
	defer w.stop()
	trace := &httptrace.ClientTrace{GotConn: w.gotConn, WroteRequest: w.wroteRequest}
	return t.base.RoundTrip(req.WithContext(httptrace.WithClientTrace(req.Context(), trace)))
}

// watchWrites returns a clone of t whose connections tell the request
// that uses them when a write to them ends.
func watchWrites(t *http.Transport) *http.Transport {
	clone := t.Clone()
	dial := t.DialContext
	clone.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := dial(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		return &watchedConn{Conn: conn}, nil
	}
	return clone
}

// watchedConn is a connection that tells its current request's sendWatch
// each time a write to it ends.
type watchedConn struct {
	net.Conn
	mu    sync.Mutex
	watch *sendWatch
}

func (c *watchedConn) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	c.mu.Lock()
	w := c.watch
	c.mu.Unlock()
	if w != nil {
		w.wroteConn()
	}
	return n, err
}

// use makes w the request c tells about its writes.
func (c *watchedConn) use(w *sendWatch) {
	c.mu.Lock()
	c.watch = w
	c.mu.Unlock()
}

// drop stops telling w, unless another request uses c by now.
func (c *watchedConn) drop(w *sendWatch) {
	c.mu.Lock()
	if c.watch == w {
		c.watch = nil
	}
	c.mu.Unlock()
}

// sendWatch decides when one request is sent. Without a watched
// connection that is when the transport reports it written. With one, it
// is the first write to the connection that ends once the request is
// written and the connection has had a write since the request got it:
// HTTP/1 reports a request written before flushing it, so its flush is
// that write, and HTTP/2 flushes a request before reporting it written.
type sendWatch struct {
	sent  func()
	mu    sync.Mutex
	conn  *watchedConn
	wrote bool
	moved bool
}

func (w *sendWatch) gotConn(info httptrace.GotConnInfo) {
	conn := info.Conn
	if tc, ok := conn.(interface{ NetConn() net.Conn }); ok {
		conn = tc.NetConn()
	}
	wc, ok := conn.(*watchedConn)
	if !ok {
		wc = nil
	}
	w.mu.Lock()
	prev := w.conn
	w.conn, w.moved = wc, false
	w.mu.Unlock()
	if prev != nil {
		prev.drop(w)
	}
	if wc != nil {
		wc.use(w)
	}
}

func (w *sendWatch) wroteRequest(httptrace.WroteRequestInfo) {
	w.mu.Lock()
	w.wrote = true
	done := w.conn == nil || w.moved
	w.mu.Unlock()
	if done {
		w.sent()
	}
}

func (w *sendWatch) wroteConn() {
	w.mu.Lock()
	w.moved = true
	done := w.wrote
	w.mu.Unlock()
	if done {
		w.sent()
	}
}

// stop detaches w from its connection once the round trip ends, so the
// connection's next request starts clean.
func (w *sendWatch) stop() {
	w.mu.Lock()
	conn := w.conn
	w.conn = nil
	w.mu.Unlock()
	if conn != nil {
		conn.drop(w)
	}
}
