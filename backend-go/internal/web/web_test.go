package web_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Automaat/ohara/backend-go/internal/web"
)

func testFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":                 {Data: []byte("<!doctype html><title>Ohara</title>")},
		"robots.txt":                 {Data: []byte("User-agent: *")},
		"_app/immutable/entry/a1.js": {Data: []byte("console.log(1)")},
		"_app/version.json":          {Data: []byte(`{"version":"1"}`)},
	}
}

func TestHandler(t *testing.T) {
	tests := []struct {
		name      string
		method    string
		target    string
		status    int
		bodyHas   string
		cacheCtrl string
	}{
		{"root serves index", http.MethodGet, "/", http.StatusOK, "<title>Ohara</title>", "no-cache"},
		{"client route falls back to index", http.MethodGet, "/collection/42", http.StatusOK, "<title>Ohara</title>", "no-cache"},
		{"static file", http.MethodGet, "/robots.txt", http.StatusOK, "User-agent", "no-cache"},
		{"immutable asset cached", http.MethodGet, "/_app/immutable/entry/a1.js", http.StatusOK, "console.log", "public, max-age=31536000, immutable"},
		{"version file revalidates", http.MethodGet, "/_app/version.json", http.StatusOK, `"version"`, "no-cache"},
		{"directory falls back to index", http.MethodGet, "/_app/", http.StatusOK, "<title>Ohara</title>", "no-cache"},
		{"traversal rejected", http.MethodGet, "/../../etc/passwd", http.StatusBadRequest, "", ""},
		{"head allowed", http.MethodHead, "/", http.StatusOK, "", "no-cache"},
		{"post rejected", http.MethodPost, "/", http.StatusMethodNotAllowed, "", ""},
	}
	h := web.Handler(testFS())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), tt.method, tt.target, http.NoBody))
			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d", rec.Code, tt.status)
			}
			if tt.bodyHas != "" && !strings.Contains(rec.Body.String(), tt.bodyHas) {
				t.Fatalf("body %q lacks %q", rec.Body.String(), tt.bodyHas)
			}
			if got := rec.Header().Get("Cache-Control"); got != tt.cacheCtrl {
				t.Fatalf("Cache-Control = %q, want %q", got, tt.cacheCtrl)
			}
		})
	}
}

func TestCheck(t *testing.T) {
	if err := web.Check(testFS()); err != nil {
		t.Fatalf("Check with index: %v", err)
	}
	if err := web.Check(fstest.MapFS{"robots.txt": {Data: []byte("x")}}); !errors.Is(err, web.ErrNoIndex) {
		t.Fatalf("Check without index = %v, want ErrNoIndex", err)
	}
}
