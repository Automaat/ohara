package deals

import (
	"context"
	"errors"
	"io"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPacerSpacesLanes(t *testing.T) {
	const gap = 50 * time.Millisecond
	p := NewPacer(gap)
	ctx := context.Background()
	var mu sync.Mutex
	var starts []time.Time
	var wg sync.WaitGroup
	for range 4 {
		lane := p.Lane()
		wg.Go(func() {
			sent, err := lane.Wait(ctx, "olx")
			if err != nil {
				t.Errorf("Wait: %v", err)
				return
			}
			mu.Lock()
			starts = append(starts, time.Now())
			mu.Unlock()
			sent()
		})
	}
	wg.Wait()
	if len(starts) != 4 {
		t.Fatalf("%d requests started, want 4", len(starts))
	}
	for i := 1; i < len(starts); i++ {
		if d := starts[i].Sub(starts[i-1]); d < gap {
			t.Fatalf("request %d started %v after the previous one, want at least %v", i, d, gap)
		}
	}
}

func TestLaneDoesNotWaitForItself(t *testing.T) {
	p := NewPacer(time.Hour)
	lane := p.Lane()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for i := range 3 {
		sent, err := lane.Wait(ctx, "olx")
		if err != nil {
			t.Fatalf("request %d of one lane: %v", i, err)
		}
		sent()
	}
}

func TestPacerLeavesOtherSitesAlone(t *testing.T) {
	p := NewPacer(time.Hour)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	first, second := p.Lane(), p.Lane()
	for _, site := range []string{"olx", "pepper", "skupszop"} {
		sent, err := first.Wait(ctx, site)
		if err != nil {
			t.Fatalf("first lane on %s: %v", site, err)
		}
		sent()
	}
	sent, err := second.Wait(ctx, "allegro")
	if err != nil {
		t.Fatalf("second lane on a site the first never asked: %v", err)
	}
	sent()
}

func TestPacerWaitStopsWithTheContext(t *testing.T) {
	p := NewPacer(time.Hour)
	first, second := p.Lane(), p.Lane()
	sent, err := first.Wait(context.Background(), "olx")
	if err != nil {
		t.Fatalf("first Wait: %v", err)
	}
	sent()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := second.Wait(ctx, "olx"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Wait = %v, want the context's deadline", err)
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if _, err := second.Wait(canceled, "olx"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait on a canceled context = %v, want context.Canceled", err)
	}
	again, cancelAgain := context.WithTimeout(context.Background(), time.Second)
	defer cancelAgain()
	sent, err = first.Wait(again, "olx")
	if err != nil {
		t.Fatalf("the lanes that gave up kept holding the site: %v", err)
	}
	sent()
}

func pacedClient(lane *Lane, base http.RoundTripper) *http.Client {
	return &http.Client{Timeout: 10 * time.Second, Transport: lane.Transport(base)}
}

func TestPacedTransportKeepsTheAnswer(t *testing.T) {
	srv, arrived := olxServer(t, "", nil)
	olx := OLX{Client: pacedClient(NewPacer(time.Hour).Lane(), nil), BaseURL: srv.URL + "/"}
	got, err := olx.Search(context.Background(), "diuna")
	if err != nil || len(got) == 0 {
		t.Fatalf("Search = %d listings, %v", len(got), err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := olx.Search(ctx, "diuna"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Search on a canceled context = %v", err)
	}
	if n := len(arrived()); n != 1 {
		t.Fatalf("OLX got %d requests, want only the first search", n)
	}
}

// stalledTransport waits before sending, like a request still connecting.
type stalledTransport struct{ stall time.Duration }

func (s stalledTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	time.Sleep(s.stall)
	return http.DefaultTransport.RoundTrip(r)
}

// olxServer answers with the OLX fixture and keeps when each request
// arrived. A request whose query is held waits until release closes.
func olxServer(t *testing.T, held string, release <-chan struct{}) (*httptest.Server, func() []time.Time) {
	t.Helper()
	body, err := os.ReadFile("testdata/olx.json")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var arrived []time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		arrived = append(arrived, time.Now())
		mu.Unlock()
		if held != "" && r.URL.Query().Get("query") == held {
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []time.Time {
		mu.Lock()
		defer mu.Unlock()
		return append([]time.Time(nil), arrived...)
	}
}

const networkJitter = 5 * time.Millisecond

func TestPacedTransportHoldsTheSiteUntilTheRequestIsSent(t *testing.T) {
	const gap = 100 * time.Millisecond
	srv, arrived := olxServer(t, "", nil)
	p := NewPacer(gap)
	stalled := OLX{Client: pacedClient(p.Lane(), stalledTransport{stall: gap * 3 / 2}), BaseURL: srv.URL + "/"}
	quick := OLX{Client: pacedClient(p.Lane(), nil), BaseURL: srv.URL + "/"}
	ctx := context.Background()
	var wg sync.WaitGroup
	wg.Go(func() {
		if _, err := stalled.Search(ctx, "diuna"); err != nil {
			t.Errorf("stalled search: %v", err)
		}
	})
	time.Sleep(gap / 10)
	if _, err := quick.Search(ctx, "katedra"); err != nil {
		t.Fatalf("quick search: %v", err)
	}
	wg.Wait()
	times := arrived()
	if len(times) != 2 {
		t.Fatalf("OLX got %d requests, want 2", len(times))
	}
	if d := times[1].Sub(times[0]); d < gap-networkJitter {
		t.Fatalf("the second lane's request arrived %v after the stalled one, want about %v", d, gap)
	}
}

func TestPacedTransportDoesNotHoldTheSiteForAnAnswer(t *testing.T) {
	const gap = 100 * time.Millisecond
	release := make(chan struct{})
	defer close(release)
	srv, _ := olxServer(t, "held", release)
	p := NewPacer(gap)
	held := OLX{Client: pacedClient(p.Lane(), nil), BaseURL: srv.URL + "/"}
	other := OLX{Client: pacedClient(p.Lane(), nil), BaseURL: srv.URL + "/"}
	heldCtx, stopHeld := context.WithCancel(context.Background())
	defer stopHeld()
	go func() { _, _ = held.Search(heldCtx, "held") }()
	time.Sleep(gap / 2)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := other.Search(ctx, "diuna"); err != nil {
		t.Fatalf("search beside a request waiting for its answer: %v", err)
	}
}

func TestPacedTransportPacesARedirect(t *testing.T) {
	const gap = 100 * time.Millisecond
	body, err := os.ReadFile("testdata/olx.json")
	if err != nil {
		t.Fatal(err)
	}
	otherArrived := make(chan struct{})
	var mu sync.Mutex
	arrivals := map[string]time.Time{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Path + "?" + r.URL.Query().Get("query")
		mu.Lock()
		arrivals[key] = time.Now()
		mu.Unlock()
		switch key {
		case "/?moved":
			select {
			case <-otherArrived:
			case <-r.Context().Done():
				return
			}
			http.Redirect(w, r, "/moved?"+r.URL.RawQuery, http.StatusFound)
			return
		case "/?other":
			close(otherArrived)
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	p := NewPacer(gap)
	redirected := OLX{Client: pacedClient(p.Lane(), nil), BaseURL: srv.URL + "/"}
	other := OLX{Client: pacedClient(p.Lane(), nil), BaseURL: srv.URL + "/"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	wg.Go(func() {
		if _, err := redirected.Search(ctx, "moved"); err != nil {
			t.Errorf("redirected search: %v", err)
		}
	})
	time.Sleep(gap / 10)
	if _, err := other.Search(ctx, "other"); err != nil {
		t.Fatalf("other search: %v", err)
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	followed, ok := arrivals["/moved?moved"]
	if !ok {
		t.Fatalf("the redirect was not followed: %v", arrivals)
	}
	if d := followed.Sub(arrivals["/?other"]); d < gap-networkJitter {
		t.Fatalf("the redirect's request arrived %v after the other lane's, want about %v", d, gap)
	}
}

// writeClock is the dialer of a test transport: it keeps when each write
// to its connections reached the connection underneath. With stall set,
// the first write that starts after the request was reported written
// waits that long first, like a writer descheduled between writing a
// request into its buffer and flushing it.
type writeClock struct {
	stall    time.Duration
	wrote    atomic.Bool
	stalling chan struct{}
	mu       sync.Mutex
	held     bool
	stalled  time.Time
	writes   []time.Time
}

func newWriteClock(stall time.Duration) *writeClock {
	return &writeClock{stall: stall, stalling: make(chan struct{})}
}

// transport is a clone of base that dials through c.
func (c *writeClock) transport(base *http.Transport) *http.Transport {
	t := base.Clone()
	dial := base.DialContext
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := dial(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		return clockedConn{Conn: conn, clock: c}, nil
	}
	return t
}

// get asks url through client and marks the request written when its
// transport reports so.
func (c *writeClock) get(client *http.Client, target string) (*http.Response, error) {
	return c.getWithHeader(client, target, 0)
}

// getWithHeader is get with a header of headerBytes bytes, none when 0.
func (c *writeClock) getWithHeader(client *http.Client, target string, headerBytes int) (*http.Response, error) {
	ctx := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{
		WroteRequest: func(httptrace.WroteRequestInfo) { c.wrote.Store(true) },
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, http.NoBody)
	if err != nil {
		return nil, err
	}
	if headerBytes > 0 {
		req.Header.Set("X-Padding", strings.Repeat("a", headerBytes))
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp, resp.Body.Close()
}

func (c *writeClock) write() {
	c.mu.Lock()
	stall := c.stall > 0 && !c.held && c.wrote.Load()
	c.held = c.held || stall
	c.mu.Unlock()
	if stall {
		close(c.stalling)
		time.Sleep(c.stall)
	}
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if stall {
		c.stalled = now
	}
	c.writes = append(c.writes, now)
}

// stalledAt is when the stalled write reached the connection.
func (c *writeClock) stalledAt() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stalled
}

func (c *writeClock) times() []time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Time(nil), c.writes...)
}

type clockedConn struct {
	net.Conn
	clock *writeClock
}

func (c clockedConn) Write(b []byte) (int, error) {
	c.clock.write()
	return c.Conn.Write(b)
}

func okServer(t *testing.T, tls bool) (*httptest.Server, *http.Transport) {
	t.Helper()
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	if !tls {
		srv := httptest.NewServer(handler)
		t.Cleanup(srv.Close)
		return srv, http.DefaultTransport.(*http.Transport)
	}
	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)
	return srv, srv.Client().Transport.(*http.Transport)
}

// connectProxy is an HTTPS proxy that tunnels CONNECT requests.
func connectProxy(t *testing.T) *url.URL {
	t.Helper()
	proxy := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "CONNECT only", http.StatusMethodNotAllowed)
			return
		}
		dst, err := (&net.Dialer{}).DialContext(r.Context(), "tcp", r.Host)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		conn, buf, err := http.NewResponseController(w).Hijack()
		if err != nil {
			_ = dst.Close()
			return
		}
		_, _ = conn.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n"))
		go func() {
			_, _ = io.Copy(dst, buf)
			_ = dst.Close()
		}()
		_, _ = io.Copy(conn, dst)
		_ = conn.Close()
	}))
	t.Cleanup(proxy.Close)
	u, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestPacedTransportSpacesRequestsByTheirWriteToTheConnection(t *testing.T) {
	for _, tc := range []struct {
		name        string
		tls         bool
		proxy       bool
		headerBytes int
	}{
		{name: "http"},
		{name: "https", tls: true},
		{name: "header larger than the write buffer", headerBytes: 5000},
		{name: "https through an https proxy", tls: true, proxy: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const gap = 100 * time.Millisecond
			srv, base := okServer(t, tc.tls)
			slowBase := base
			if tc.proxy {
				slowBase = base.Clone()
				slowBase.Proxy = http.ProxyURL(connectProxy(t))
			}
			p := NewPacer(gap)
			slow, quick := newWriteClock(gap*3/2), newWriteClock(0)
			slowClient := pacedClient(p.Lane(), slow.transport(slowBase))
			quickClient := pacedClient(p.Lane(), quick.transport(base))
			t.Cleanup(slowClient.CloseIdleConnections)
			t.Cleanup(quickClient.CloseIdleConnections)

			done := make(chan error, 1)
			go func() {
				_, err := slow.getWithHeader(slowClient, srv.URL, tc.headerBytes)
				done <- err
			}()
			select {
			case <-slow.stalling:
			case err := <-done:
				t.Fatalf("the first lane's request ended without a write after it was reported written: %v", err)
			}
			if _, err := quick.get(quickClient, srv.URL); err != nil {
				t.Fatalf("second lane: %v", err)
			}
			if err := <-done; err != nil {
				t.Fatalf("first lane: %v", err)
			}

			stalled, writes := slow.stalledAt(), quick.times()
			if len(writes) == 0 {
				t.Fatal("the second lane wrote nothing to its connection")
			}
			if d := writes[0].Sub(stalled); d < gap {
				t.Fatalf("the second lane wrote to its connection %v after the first lane's request reached its own, want at least %v", d, gap)
			}
		})
	}
}

// arrivals is a server that keeps the path and arrival time of each
// request.
func arrivals(t *testing.T) (*httptest.Server, func() map[string]time.Time) {
	t.Helper()
	var mu sync.Mutex
	at := map[string]time.Time{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		at[r.URL.Path] = time.Now()
		mu.Unlock()
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(srv.Close)
	return srv, func() map[string]time.Time {
		mu.Lock()
		defer mu.Unlock()
		return maps.Clone(at)
	}
}

func getURL(ctx context.Context, client *http.Client, target string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, http.NoBody)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	_, err = io.Copy(io.Discard, resp.Body)
	return errors.Join(err, resp.Body.Close())
}

func TestPacedTransportWatchesAZeroTransport(t *testing.T) {
	const gap = 100 * time.Millisecond
	srv, arrived := arrivals(t)
	p := NewPacer(gap)
	zero := pacedClient(p.Lane(), &http.Transport{})
	t.Cleanup(zero.CloseIdleConnections)
	other := pacedClient(p.Lane(), nil)

	stalling := make(chan struct{})
	ctx := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{
		WroteRequest: func(httptrace.WroteRequestInfo) {
			close(stalling)
			time.Sleep(gap * 3 / 2)
		},
	})
	done := make(chan error, 1)
	go func() { done <- getURL(ctx, zero, srv.URL+"/first") }()
	select {
	case <-stalling:
	case err := <-done:
		t.Fatalf("the first request was never reported written: %v", err)
	}
	if err := getURL(context.Background(), other, srv.URL+"/second"); err != nil {
		t.Fatalf("second lane: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("first lane: %v", err)
	}
	at := arrived()
	if d := at["/second"].Sub(at["/first"]); d < gap-networkJitter {
		t.Fatalf("the second lane's request arrived %v after the first, want about %v", d, gap)
	}
}

// hookedConn runs before ahead of each write; a write it fails sends
// nothing.
type hookedConn struct {
	net.Conn
	before func() error
}

func (c hookedConn) Write(b []byte) (int, error) {
	if err := c.before(); err != nil {
		return 0, err
	}
	return c.Conn.Write(b)
}

func TestPacedTransportHoldsTheSiteWhileARequestIsTriedAgain(t *testing.T) {
	const gap = 100 * time.Millisecond
	srv, base := okServer(t, false)
	var dials atomic.Int32
	var broken atomic.Bool
	redialing, resume := make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	var retried time.Time
	retrying := base.Clone()
	retrying.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		n := dials.Add(1)
		if n == 2 {
			close(redialing)
			<-resume
		}
		conn, err := (&net.Dialer{}).DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		before := func() error {
			mu.Lock()
			defer mu.Unlock()
			if retried.IsZero() {
				retried = time.Now()
			}
			return nil
		}
		if n == 1 {
			before = func() error {
				if broken.Load() {
					return errors.New("connection reset")
				}
				return nil
			}
		}
		return hookedConn{Conn: conn, before: before}, nil
	}
	p := NewPacer(gap)
	first := pacedClient(p.Lane(), retrying)
	t.Cleanup(first.CloseIdleConnections)
	quick := newWriteClock(0)
	second := pacedClient(p.Lane(), quick.transport(base))
	t.Cleanup(second.CloseIdleConnections)
	ctx := context.Background()

	if err := getURL(ctx, first, srv.URL+"/warm"); err != nil {
		t.Fatalf("warming the connection: %v", err)
	}
	broken.Store(true)
	done := make(chan error, 1)
	go func() { done <- getURL(ctx, first, srv.URL+"/again") }()
	select {
	case <-redialing:
	case err := <-done:
		t.Fatalf("the transport did not try the request again on a new connection: %v", err)
	}
	secondDone := make(chan error, 1)
	go func() {
		_, err := quick.get(second, srv.URL+"/second")
		secondDone <- err
	}()
	time.Sleep(gap / 2)
	close(resume)
	if err := <-done; err != nil {
		t.Fatalf("first lane: %v", err)
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second lane: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	writes := quick.times()
	if len(writes) == 0 || retried.IsZero() {
		t.Fatalf("writes: second lane %d, retried at %v", len(writes), retried)
	}
	if d := writes[0].Sub(retried); d < gap {
		t.Fatalf("the second lane wrote %v after the first lane's retried request, want at least %v", d, gap)
	}
}

func TestPacedTransportDoesNotHoldTheSiteForAnAnswerOnEachProtocol(t *testing.T) {
	for _, tc := range []struct {
		name      string
		tls       bool
		http2     bool
		protoWant int
	}{
		{name: "HTTP/1.1 over TLS", tls: true, protoWant: 1},
		{name: "HTTP/2 over TLS", tls: true, http2: true, protoWant: 2},
		{name: "cleartext HTTP/2", http2: true, protoWant: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const gap = 100 * time.Millisecond
			release := make(chan struct{})
			defer close(release)
			srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/held" {
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
				}
				_, _ = w.Write([]byte("ok"))
			}))
			var base *http.Transport
			if tc.tls {
				srv.EnableHTTP2 = tc.http2
				srv.StartTLS()
				base = srv.Client().Transport.(*http.Transport)
			} else {
				srv.Config.Protocols = new(http.Protocols)
				srv.Config.Protocols.SetUnencryptedHTTP2(true)
				srv.Start()
				base = http.DefaultTransport.(*http.Transport).Clone()
				base.Protocols = new(http.Protocols)
				base.Protocols.SetUnencryptedHTTP2(true)
			}
			t.Cleanup(srv.Close)
			p := NewPacer(gap)
			held, other := newWriteClock(0), newWriteClock(0)
			heldClient := pacedClient(p.Lane(), held.transport(base))
			otherClient := pacedClient(p.Lane(), other.transport(base))
			t.Cleanup(heldClient.CloseIdleConnections)
			t.Cleanup(otherClient.CloseIdleConnections)
			go func() { _, _ = held.get(heldClient, srv.URL+"/held") }()
			time.Sleep(gap / 2)

			finished := make(chan struct{})
			var resp *http.Response
			var err error
			go func() {
				defer close(finished)
				resp, err = other.get(otherClient, srv.URL+"/other")
			}()
			select {
			case <-finished:
			case <-time.After(2 * time.Second):
				t.Fatal("the other lane still waits beside a request waiting for its answer")
			}
			if err != nil {
				t.Fatalf("other lane: %v", err)
			}
			if resp.ProtoMajor != tc.protoWant {
				t.Fatalf("answered over HTTP/%d, want HTTP/%d", resp.ProtoMajor, tc.protoWant)
			}
		})
	}
}

// brokenConn fails every write.
type brokenConn struct{ net.Conn }

func (brokenConn) Write([]byte) (int, error) { return 0, errors.New("connection reset") }

func TestPacedTransportReleasesTheSiteAfterAFailedWrite(t *testing.T) {
	const gap = 50 * time.Millisecond
	srv, _ := okServer(t, false)
	broken := http.DefaultTransport.(*http.Transport).Clone()
	broken.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		return brokenConn{Conn: conn}, nil
	}
	p := NewPacer(gap)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	get := func(client *http.Client) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, http.NoBody)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		return resp.Body.Close()
	}
	if err := get(pacedClient(p.Lane(), broken)); err == nil {
		t.Fatal("a request whose write failed succeeded")
	}
	if err := get(pacedClient(p.Lane(), nil)); err != nil {
		t.Fatalf("the next lane after a failed write: %v", err)
	}
}
