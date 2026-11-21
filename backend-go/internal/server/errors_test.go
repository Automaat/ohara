package server_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Automaat/ohara/backend-go/internal/httpx"
	"github.com/Automaat/ohara/backend-go/internal/server"
)

// lockedBuffer is a log sink the handler goroutines and the test share.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type response struct {
	code   int
	header http.Header
	body   string
}

func do(t *testing.T, method, url string) response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, http.NoBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return response{code: resp.StatusCode, header: resp.Header, body: string(body)}
}

// checkJSONError asserts an API error answer: the status, the JSON content
// type and exactly {"error": msg}.
func checkJSONError(t *testing.T, got response, code int, msg string) {
	t.Helper()
	if got.code != code {
		t.Fatalf("status = %d, want %d (body %q)", got.code, code, got.body)
	}
	if ct := got.header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var e httpx.Error
	dec := json.NewDecoder(strings.NewReader(got.body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&e); err != nil {
		t.Fatalf("body %q is not an API error: %v", got.body, err)
	}
	if e != (httpx.Error{Error: msg}) {
		t.Errorf("body = %+v, want {Error: %q}", e, msg)
	}
}

// queryAPI has a route only for chi's QUERY method.
type queryAPI struct{}

func (queryAPI) Mount(r chi.Router) {
	r.Method("QUERY", "/search", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
}

func TestRouterErrorBodies(t *testing.T) {
	logs := &lockedBuffer{}
	logger := slog.New(slog.NewTextHandler(logs, nil))
	srv := httptest.NewServer(server.New(server.Config{}, logger, server.Deps{API: []server.Mounter{failingAPI{logger: logger}, queryAPI{}}}))
	t.Cleanup(srv.Close)

	tests := []struct {
		name   string
		method string
		path   string
		code   int
		msg    string
		allow  []string
	}{
		{"wrong method on status", http.MethodPatch, "/api/status", http.StatusMethodNotAllowed, "method not allowed", []string{http.MethodGet}},
		{"wrong method on a domain route", http.MethodDelete, "/api/items", http.StatusMethodNotAllowed, "method not allowed", []string{http.MethodGet, http.MethodPost}},
		{"wrong method on health", http.MethodPost, "/health", http.StatusMethodNotAllowed, "method not allowed", []string{http.MethodGet}},
		{"wrong method on a QUERY route", http.MethodPatch, "/api/search", http.StatusMethodNotAllowed, "method not allowed", []string{"QUERY"}},
		{"method chi does not route", "PROPFIND", "/api/status", http.StatusMethodNotAllowed, "method not allowed", nil},
		{"handler panic", http.MethodGet, "/api/panic", http.StatusInternalServerError, "internal server error", nil},
		{"handler error", http.MethodGet, "/api/broken", http.StatusInternalServerError, "internal server error", nil},
		{"unknown API path", http.MethodGet, "/api/nope", http.StatusNotFound, "not found", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := do(t, tt.method, srv.URL+tt.path)
			checkJSONError(t, got, tt.code, tt.msg)
			allow := got.header.Values("Allow")
			slices.Sort(allow)
			if !slices.Equal(allow, tt.allow) {
				t.Errorf("Allow = %v, want %v", allow, tt.allow)
			}
		})
	}

	out := logs.String()
	for _, want := range []string{"handler panic", "panic=boom", "goroutine ", "failingAPI.Mount"} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q:\n%s", want, out)
		}
	}
}

// lateAPI has handlers that panic or outlive the request timeout.
type lateAPI struct{ logger *slog.Logger }

func (a lateAPI) Mount(r chi.Router) {
	r.Get("/wrote-then-panic", func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteJSON(w, a.logger, http.StatusOK, map[string]bool{"ok": true})
		panic("boom")
	})
	r.Get("/wrapped-abort", func(http.ResponseWriter, *http.Request) {
		panic(fmt.Errorf("stream: %w", http.ErrAbortHandler))
	})
	r.Get("/abort", func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	})
	r.Get("/length-then-panic", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "1")
		panic("boom")
	})
	r.Get("/length-then-silent", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1")
		<-r.Context().Done()
	})
	r.Get("/silent", func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})
	r.Get("/answers-late", func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		httpx.WriteJSON(w, a.logger, http.StatusOK, map[string]bool{"ok": true})
	})
	r.Get("/answers-early", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, a.logger, http.StatusOK, map[string]bool{"ok": true})
		<-r.Context().Done()
	})
}

func TestTimeoutAndLatePanic(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	h := server.NewWithTimeout(server.Config{}, logger, server.Deps{API: []server.Mounter{lateAPI{logger: logger}}}, 50*time.Millisecond)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	t.Run("handler wrote nothing", func(t *testing.T) {
		checkJSONError(t, do(t, http.MethodGet, srv.URL+"/api/silent"), http.StatusGatewayTimeout, "gateway timeout")
	})
	t.Run("handler set a length and wrote nothing", func(t *testing.T) {
		checkJSONError(t, do(t, http.MethodGet, srv.URL+"/api/length-then-silent"), http.StatusGatewayTimeout, "gateway timeout")
	})
	t.Run("panic after setting a length", func(t *testing.T) {
		checkJSONError(t, do(t, http.MethodGet, srv.URL+"/api/length-then-panic"), http.StatusInternalServerError, "internal server error")
	})
	t.Run("panic with a wrapped abort", func(t *testing.T) {
		checkJSONError(t, do(t, http.MethodGet, srv.URL+"/api/wrapped-abort"), http.StatusInternalServerError, "internal server error")
	})
	t.Run("panic with the abort sentinel", func(t *testing.T) {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/api/abort", http.NoBody)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
			t.Fatalf("GET /api/abort = %d, want the connection aborted", resp.StatusCode)
		}
	})

	ok := "{\"ok\":true}\n"
	for _, path := range []string{"/api/answers-late", "/api/answers-early", "/api/wrote-then-panic"} {
		t.Run(path, func(t *testing.T) {
			got := do(t, http.MethodGet, srv.URL+path)
			if got.code != http.StatusOK || got.body != ok {
				t.Fatalf("GET %s = %d %q, want 200 %q", path, got.code, got.body, ok)
			}
		})
	}
}
