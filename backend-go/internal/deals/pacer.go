package deals

import (
	"bytes"
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptrace"
	"reflect"
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
// When base is an *http.Transport that leaves dialing TLS to itself, as
// http.DefaultTransport does, Transport uses a clone of it with
// connections of its own, and a request is sent once its last bytes
// reach the connection: HTTP/1 reports a request written while its end
// still sits in the connection's buffer, so an HTTP/1 request is sent
// with the first write to its connection after that. HTTP/2 flushes a
// request before reporting it written, and so does HTTP/1 a request with
// a fixed-length body that has already written some of it, which can
// leave the buffer empty; those are sent once reported written. With any
// other base a request is sent once base reports it written. Either way
// the round trip ending first, after a failed write too, releases the
// site, but a write that sent nothing keeps it while the transport tries
// the request again on another connection.
func (l *Lane) Transport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	if t, ok := base.(*http.Transport); ok && t.DialTLSContext == nil && (t.DialContext != nil || !dialsThroughDial(t)) {
		base = watchWrites(t)
	}
	return pacedTransport{base: base, lane: l}
}

// dialsThroughDial reports whether t dials with its deprecated Dial
// field, which a DialContext set on its clone would override. The field
// is read through reflection because naming it fails the linters.
func dialsThroughDial(t *http.Transport) bool {
	dial := reflect.ValueOf(t).Elem().FieldByName("Dial")
	return dial.IsValid() && !dial.IsNil()
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
	w := &sendWatch{sent: sent, fixedBody: req.Body != nil && req.Body != http.NoBody && req.ContentLength > 0}
	defer w.stop()
	trace := &httptrace.ClientTrace{GotConn: w.gotConn, WroteRequest: w.wroteRequest}
	return t.base.RoundTrip(req.WithContext(httptrace.WithClientTrace(req.Context(), trace)))
}

// watchWrites returns a clone of t whose connections tell the request
// that uses them when a write to them ends. A nil DialContext dials as
// http.Transport does then, with a zero net.Dialer.
func watchWrites(t *http.Transport) *http.Transport {
	clone := t.Clone()
	dial := t.DialContext
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	clone.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := dial(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		return &watchedConn{Conn: conn}, nil
	}
	return clone
}

// http2Preface opens every HTTP/2 connection, cleartext ones too.
var http2Preface = []byte("PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n")

// watchedConn is a connection that tells its current request's sendWatch
// each time a write to it sends something.
type watchedConn struct {
	net.Conn
	mu      sync.Mutex
	watch   *sendWatch
	written bool
	http2   bool
}

func (c *watchedConn) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	c.mu.Lock()
	if !c.written {
		c.written = true
		c.http2 = bytes.HasPrefix(b, http2Preface)
	}
	w := c.watch
	c.mu.Unlock()
	if w != nil && n > 0 {
		w.wroteConn()
	}
	return n, err
}

// use makes w the request c tells about its writes and reports whether c
// speaks cleartext HTTP/2.
func (c *watchedConn) use(w *sendWatch) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.watch = w
	return c.http2
}

// drop stops telling w, unless another request uses c by now.
func (c *watchedConn) drop(w *sendWatch) {
	c.mu.Lock()
	if c.watch == w {
		c.watch = nil
	}
	c.mu.Unlock()
}

// sendWatch decides when one request is sent, as Transport describes.
// Each connection the request gets starts it over: the transport sends
// the request again on another connection when a write sent nothing.
type sendWatch struct {
	sent      func()
	fixedBody bool
	mu        sync.Mutex
	conn      *watchedConn
	http2     bool
	early     bool
	wrote     bool
}

func (w *sendWatch) gotConn(info httptrace.GotConnInfo) {
	http2 := false
	if tc, ok := info.Conn.(interface{ ConnectionState() tls.ConnectionState }); ok {
		http2 = tc.ConnectionState().NegotiatedProtocol == "h2"
	}
	wc := underlyingWatchedConn(info.Conn)
	w.mu.Lock()
	prev := w.conn
	w.conn, w.http2, w.early, w.wrote = wc, http2, false, false
	w.mu.Unlock()
	if prev != nil && prev != wc {
		prev.drop(w)
	}
	if wc != nil && wc.use(w) {
		w.mu.Lock()
		w.http2 = true
		w.mu.Unlock()
	}
}

// underlyingWatchedConn finds the watched connection under conn's TLS
// layers: two of them for an HTTPS site behind an HTTPS proxy.
func underlyingWatchedConn(conn net.Conn) *watchedConn {
	for conn != nil {
		if wc, ok := conn.(*watchedConn); ok {
			return wc
		}
		tc, ok := conn.(interface{ NetConn() net.Conn })
		if !ok {
			return nil
		}
		conn = tc.NetConn()
	}
	return nil
}

func (w *sendWatch) wroteRequest(info httptrace.WroteRequestInfo) {
	w.mu.Lock()
	w.wrote = info.Err == nil
	done := w.wrote && (w.conn == nil || w.http2 || w.fixedBody && w.early)
	w.mu.Unlock()
	if done {
		w.sent()
	}
}

func (w *sendWatch) wroteConn() {
	w.mu.Lock()
	w.early = w.early || !w.wrote
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
