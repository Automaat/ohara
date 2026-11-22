package server_test

import (
	"bufio"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Automaat/ohara/backend-go/internal/server"
)

// switched is what the hijacking handlers write on the raw connection.
const switched = "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: test\r\n\r\nhello"

// upgradeAPI has handlers that panic or hijack, before or after the request
// timeout.
type upgradeAPI struct{}

func (upgradeAPI) Mount(r chi.Router) {
	r.Get("/panic", func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})
	r.Get("/late-panic", func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		panic("boom")
	})
	r.Get("/hijack-then-panic", func(w http.ResponseWriter, _ *http.Request) {
		hijackAndSwitch(w)
		panic("boom")
	})
	r.Get("/late-hijack", func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		hijackAndSwitch(w)
	})
	r.Get("/copy-then-panic", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.Copy(w, panicReader{})
	})
	r.Get("/copy-late-eof", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(w, lateEOF{done: r.Context().Done()})
	})
	r.Get("/pusher", func(w http.ResponseWriter, _ *http.Request) {
		if _, ok := w.(http.Pusher); !ok {
			http.Error(w, "no Pusher", http.StatusNotImplemented)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// panicReader panics on its first Read, before it yields a byte.
type panicReader struct{}

func (panicReader) Read([]byte) (int, error) { panic("boom") }

// lateEOF yields nothing and ends once done closes.
type lateEOF struct{ done <-chan struct{} }

func (l lateEOF) Read([]byte) (int, error) {
	<-l.done
	return 0, io.EOF
}

// hijackAndSwitch takes the connection over through http.ResponseController,
// as an upgrading handler would, writes switched on it and closes it.
func hijackAndSwitch(w http.ResponseWriter) {
	conn, rw, err := http.NewResponseController(w).Hijack()
	if err != nil {
		panic("hijack: " + err.Error())
	}
	defer conn.Close()
	_, _ = rw.WriteString(switched)
	_ = rw.Flush()
}

// rawGet sends a GET with the given header lines, and no others but Host,
// on its own connection and returns a reader of what the server sends back.
func rawGet(t *testing.T, addr, path string, header ...string) *bufio.Reader {
	t.Helper()
	var d net.Dialer
	conn, err := d.DialContext(t.Context(), "tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	req := "GET " + path + " HTTP/1.1\r\nHost: ohara.test\r\n" + strings.Join(header, "\r\n") + "\r\n\r\n"
	if _, err := io.WriteString(conn, req); err != nil {
		t.Fatalf("write request: %v", err)
	}
	return bufio.NewReader(conn)
}

// readAnswer reads one HTTP response to a GET from br.
func readAnswer(t *testing.T, br *bufio.Reader) response {
	t.Helper()
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read answer: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return response{code: resp.StatusCode, header: resp.Header, body: string(body)}
}

// TestPanicAnswersWhileWritable covers a panic the response can still answer:
// one on a request marked Connection: Upgrade that no handler upgraded, and
// one after the request timeout. Both get the JSON 500, not an empty 200 or
// the timeout's 504. A handler that hijacked the connection gets nothing
// written after it, whether it panicked or outlived the timeout.
func TestPanicAnswersWhileWritable(t *testing.T) {
	logs := &lockedBuffer{}
	logger := slog.New(slog.NewTextHandler(logs, nil))
	serverLogs := &lockedBuffer{}
	srv := httptest.NewUnstartedServer(server.NewWithTimeout(server.Config{}, logger,
		server.Deps{API: []server.Mounter{upgradeAPI{}}}, 50*time.Millisecond))
	srv.Config.ErrorLog = slog.NewLogLogger(slog.NewTextHandler(serverLogs, nil), slog.LevelError)
	srv.Start()
	t.Cleanup(srv.Close)
	addr := srv.Listener.Addr().String()

	t.Run("panic on a request marked for upgrade", func(t *testing.T) {
		got := readAnswer(t, rawGet(t, addr, "/api/panic", "Connection: Upgrade", "Upgrade: websocket"))
		checkJSONError(t, got, http.StatusInternalServerError, "internal server error")
	})
	t.Run("panic after the timeout", func(t *testing.T) {
		checkJSONError(t, do(t, http.MethodGet, srv.URL+"/api/late-panic"), http.StatusInternalServerError, "internal server error")
	})
	// Go's transport asks for gzip, so these copies go through the
	// compress middleware's writer, which has no ReadFrom of its own.
	t.Run("copy from a source that panics", func(t *testing.T) {
		checkJSONError(t, do(t, http.MethodGet, srv.URL+"/api/copy-then-panic"), http.StatusInternalServerError, "internal server error")
	})
	t.Run("copy from a source that ends after the timeout", func(t *testing.T) {
		checkJSONError(t, do(t, http.MethodGet, srv.URL+"/api/copy-late-eof"), http.StatusGatewayTimeout, "gateway timeout")
	})
	for _, path := range []string{"/api/hijack-then-panic", "/api/late-hijack"} {
		t.Run(path, func(t *testing.T) {
			out, err := io.ReadAll(rawGet(t, addr, path, "Connection: Upgrade", "Upgrade: test"))
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if got := string(out); got != switched {
				t.Errorf("GET %s sent %q, want only the handler's %q", path, got, switched)
			}
		})
	}

	srv.Close()
	if out := serverLogs.String(); out != "" {
		t.Errorf("server log = %q, want nothing written to a hijacked connection", out)
	}
	out := logs.String()
	if strings.Contains(out, "encode response") {
		t.Errorf("log shows an answer written to a hijacked connection:\n%s", out)
	}
	if n := strings.Count(out, `msg="handler panic"`); n != 4 {
		t.Errorf("logged %d handler panics, want 4:\n%s", n, out)
	}
}

// TestHTTP2Pusher covers an HTTP/2 handler on an uncompressed answer: it
// still finds the connection's http.Pusher through the recoverer and the
// timeout.
func TestHTTP2Pusher(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	srv := httptest.NewUnstartedServer(server.New(server.Config{}, logger, server.Deps{API: []server.Mounter{upgradeAPI{}}}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/api/pusher", http.NoBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("GET /api/pusher: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.ProtoMajor != 2 || resp.StatusCode != http.StatusNoContent {
		t.Fatalf("GET /api/pusher = %s %d %q, want HTTP/2 204", resp.Proto, resp.StatusCode, body)
	}
}
