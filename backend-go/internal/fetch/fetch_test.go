package fetch

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Automaat/ohara/backend-go/internal/metrics"
)

const testCap = 16

// serve answers every request with status and body, plus header X-Reset.
func serve(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Reset", "60")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestDoBodyCap(t *testing.T) {
	for _, tc := range []struct {
		name    string
		size    int
		wantErr error
	}{
		{name: "exactly the cap", size: testCap},
		{name: "one byte over the cap", size: testCap + 1, wantErr: ErrTooLarge},
		{name: "far over the cap", size: 10 * testCap, wantErr: ErrTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := strings.Repeat("x", tc.size)
			srv := serve(t, http.StatusOK, body)
			resp, err := Do(t.Context(), srv.Client(), Request{URL: srv.URL, MaxBody: testCap})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr != nil {
				if resp.Body != nil {
					t.Errorf("body = %q after %v, want none", resp.Body, err)
				}
				if got := Outcome(err); got != metrics.SourceParseError {
					t.Errorf("Outcome = %q, want %q", got, metrics.SourceParseError)
				}
				return
			}
			if string(resp.Body) != body || resp.Status != http.StatusOK {
				t.Errorf("response = %d %q, want 200 %q", resp.Status, resp.Body, body)
			}
			if got := Outcome(err); got != metrics.SourceOK {
				t.Errorf("Outcome = %q, want %q", got, metrics.SourceOK)
			}
		})
	}
}

func TestDoTransportErrorHidesQuery(t *testing.T) {
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = Do(t.Context(), nil, Request{URL: "http://" + addr + "/rates?api_key=secret", MaxBody: testCap})
	if err == nil {
		t.Fatal("Do on a closed port succeeded")
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "api_key") {
		t.Errorf("error %q carries the query", err)
	}
	if want := "GET http://" + addr + "/rates: "; !strings.HasPrefix(err.Error(), want) {
		t.Errorf("error %q, want prefix %q", err, want)
	}
	if got := Outcome(err); got != metrics.SourceError {
		t.Errorf("Outcome = %q, want %q", got, metrics.SourceError)
	}
}

func TestDoStatus(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		body     string
		ok       []int
		wantCode int
		wantErr  string
	}{
		{name: "200 by default", status: http.StatusOK, body: "{}"},
		{name: "accepted 404", status: http.StatusNotFound, body: "gone", ok: []int{http.StatusOK, http.StatusNotFound}},
		{name: "refused", status: http.StatusTooManyRequests, body: "slow down", wantCode: http.StatusTooManyRequests, wantErr: "status 429"},
		{name: "not accepted 404", status: http.StatusNotFound, body: "gone", ok: []int{http.StatusOK}, wantCode: http.StatusNotFound, wantErr: "status 404"},
		{
			name: "refused with a page over the cap", status: http.StatusForbidden, body: strings.Repeat("x", testCap+1),
			wantCode: http.StatusForbidden, wantErr: "status 403",
		},
		{
			name: "server error with a page over the cap", status: http.StatusServiceUnavailable, body: strings.Repeat("x", testCap+1),
			wantCode: http.StatusServiceUnavailable, wantErr: "response too large (status 503)",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := serve(t, tc.status, tc.body)
			resp, err := Do(t.Context(), srv.Client(), Request{URL: srv.URL, MaxBody: testCap, OK: tc.ok})
			if resp.Status != tc.status || resp.Header.Get("X-Reset") != "60" {
				t.Errorf("response = %d with X-Reset %q, want %d with 60", resp.Status, resp.Header.Get("X-Reset"), tc.status)
			}
			if tc.wantCode == 0 {
				if err != nil || string(resp.Body) != tc.body {
					t.Fatalf("Do = %q, %v, want %q", resp.Body, err, tc.body)
				}
				return
			}
			se, ok := errors.AsType[StatusError](err)
			if !ok || se.Code != tc.wantCode || err.Error() != tc.wantErr {
				t.Fatalf("err = %v, want %q carrying status %d", err, tc.wantErr, tc.wantCode)
			}
			if resp.Body != nil {
				t.Errorf("body = %q with a refused status, want none", resp.Body)
			}
			if got, want := Outcome(err), "http_"+strconv.Itoa(tc.wantCode); got != want {
				t.Errorf("Outcome = %q, want %q", got, want)
			}
		})
	}
}

// TestDoStatusWithBrokenBody checks that an error status whose page
// breaks off still counts as its status.
func TestDoStatusWithBrokenBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("short"))
	}))
	t.Cleanup(srv.Close)
	_, err := Do(t.Context(), srv.Client(), Request{URL: srv.URL, MaxBody: testCap})
	if se, ok := errors.AsType[StatusError](err); !ok || se.Code != http.StatusServiceUnavailable {
		t.Fatalf("err = %v, want one carrying status 503", err)
	}
	if !strings.Contains(err.Error(), "unexpected EOF") {
		t.Errorf("err = %v, want it to name the broken body", err)
	}
	if got := Outcome(err); got != "http_503" {
		t.Errorf("Outcome = %q, want http_503", got)
	}
}

// TestDoRefusalWithStalledBody checks that a 429 returns with its headers
// while its page never ends.
func TestDoRefusalWithStalledBody(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-RateLimit-Reset", "1700000000")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("slow"))
		w.(http.Flusher).Flush()
		<-release
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	start := time.Now()
	resp, err := Do(ctx, srv.Client(), Request{URL: srv.URL, MaxBody: testCap})
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("Do took %v, want it back once the headers arrived", took)
	}
	if se, ok := errors.AsType[StatusError](err); !ok || se.Code != http.StatusTooManyRequests || err.Error() != "status 429" {
		t.Fatalf("err = %v, want status 429", err)
	}
	if resp.Header.Get("X-RateLimit-Reset") != "1700000000" {
		t.Errorf("X-RateLimit-Reset = %q, want the answer's", resp.Header.Get("X-RateLimit-Reset"))
	}
}

// TestDoRedirectErrorHidesQuery checks that a malformed redirect target,
// which net/http quotes in its error, loses its query too.
func TestDoRedirectErrorHidesQuery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/%zz?"+r.URL.RawQuery)
		w.WriteHeader(http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	_, err := Do(t.Context(), srv.Client(), Request{URL: srv.URL + "/search?api_key=secret", MaxBody: testCap})
	if err == nil {
		t.Fatal("Do followed a malformed redirect")
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "api_key") {
		t.Errorf("error %q carries the query", err)
	}
	if !strings.Contains(err.Error(), "Location") {
		t.Errorf("error %q, want it to name the redirect failure", err)
	}
}

// TestDoTimeoutStaysATimeout checks that a cleaned error still unwraps to
// its cause.
func TestDoTimeoutStaysATimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	_, err := Do(ctx, srv.Client(), Request{URL: srv.URL + "/?token=secret", MaxBody: testCap})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if strings.Contains(err.Error(), "secret") {
		t.Errorf("error %q carries the query", err)
	}
}

func TestDoSendsMethodHeadersAndBody(t *testing.T) {
	var got struct{ method, ua, auth, body string }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got.method, got.ua, got.auth, got.body = r.Method, r.UserAgent(), r.Header.Get("X-Csrf-Token"), string(b)
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(srv.Close)
	_, err := Do(t.Context(), srv.Client(), Request{
		Method:  http.MethodPost,
		URL:     srv.URL,
		Body:    strings.NewReader("isbn=1"),
		Header:  http.Header{"User-Agent": {BrowserUserAgent}, "X-Csrf-Token": {"tok"}},
		MaxBody: testCap,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.method != http.MethodPost || got.ua != BrowserUserAgent || got.auth != "tok" || got.body != "isbn=1" {
		t.Errorf("server saw %+v", got)
	}
}

func TestDoInvalidURLHidesIt(t *testing.T) {
	_, err := Do(t.Context(), nil, Request{URL: "http://exa mple.com/?api_key=secret", MaxBody: testCap})
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("err = %v, want one without the URL", err)
	}
	if got := Outcome(err); got != metrics.SourceError {
		t.Errorf("Outcome = %q, want %q", got, metrics.SourceError)
	}
}
