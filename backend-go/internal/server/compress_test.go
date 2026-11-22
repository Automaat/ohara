package server_test

import (
	"bytes"
	"compress/gzip"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/go-chi/chi/v5"

	"github.com/Automaat/ohara/backend-go/internal/httpx"
	"github.com/Automaat/ohara/backend-go/internal/server"
)

// coverAPI answers a JPEG the way the cover handler does.
type coverAPI struct{}

func (coverAPI) Mount(r chi.Router) {
	r.Get("/cover", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(bytes.Repeat([]byte{0xff, 0xd8, 0xff}, 400))
	})
}

// jsonAPI answers a JSON list long enough to be worth compressing.
type jsonAPI struct{ logger *slog.Logger }

func (a jsonAPI) Mount(r chi.Router) {
	r.Get("/collection", func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteJSON(w, a.logger, http.StatusOK, strings.Split(strings.Repeat("copy,", 400), ","))
	})
}

func compressFS() fstest.MapFS {
	text := strings.Repeat("ohara ", 400)
	return fstest.MapFS{
		"index.html":                  {Data: []byte("<!doctype html><title>Ohara</title>" + text)},
		"manifest.webmanifest":        {Data: []byte(`{"name":"` + text + `"}`)},
		"_app/immutable/entry/app.js": {Data: []byte("export const a = '" + text + "'")},
		"_app/immutable/app.css":      {Data: []byte("body{}/*" + text + "*/")},
		"_app/immutable/zxing.wasm":   {Data: append([]byte("\x00asm\x01\x00\x00\x00"), text...)},
		"favicon.svg":                 {Data: []byte("<svg xmlns=\"http://www.w3.org/2000/svg\"><!--" + text + "--></svg>")},
		"apple-touch-icon.png":        {Data: append([]byte("\x89PNG\r\n\x1a\n"), text...)},
	}
}

// fetch sends a request with the given headers and no transparent
// decompression, so the test sees the encoding on the wire.
func fetch(t *testing.T, method, url string, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, http.NoBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{Transport: &http.Transport{DisableCompression: true}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, body
}

func fileData(t *testing.T, fsys fstest.MapFS, name string) []byte {
	t.Helper()
	f, ok := fsys[name]
	if !ok || f == nil {
		t.Fatalf("no file %s", name)
		return nil
	}
	return f.Data
}

func gunzip(t *testing.T, b []byte) []byte {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("gunzip: %v", err)
	}
	return out
}

func TestCompression(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	webFS := compressFS()
	srv := newServer(t, server.Deps{DB: fakeDB{}, Web: webFS, API: []server.Mounter{coverAPI{}, jsonAPI{logger: logger}}})
	gz := map[string]string{"Accept-Encoding": "gzip, deflate, br"}

	compressed := []struct {
		name, path, file, contentType string
	}{
		{"SPA root", "/", "index.html", "text/html"},
		{"client route", "/scan/queue", "index.html", "text/html"},
		{"manifest", "/manifest.webmanifest", "manifest.webmanifest", "application/manifest+json"},
		{"script", "/_app/immutable/entry/app.js", "_app/immutable/entry/app.js", "text/javascript"},
		{"stylesheet", "/_app/immutable/app.css", "_app/immutable/app.css", "text/css"},
		{"decoder wasm", "/_app/immutable/zxing.wasm", "_app/immutable/zxing.wasm", "application/wasm"},
		{"svg icon", "/favicon.svg", "favicon.svg", "image/svg+xml"},
		{"API JSON", "/api/collection", "", "application/json"},
	}
	for _, tt := range compressed {
		t.Run("gzip "+tt.name, func(t *testing.T) {
			resp, body := fetch(t, http.MethodGet, srv.URL+tt.path, gz)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}
			if got := resp.Header.Get("Content-Encoding"); got != "gzip" {
				t.Fatalf("Content-Encoding = %q, want gzip", got)
			}
			if got := resp.Header.Get("Vary"); got != "Accept-Encoding" {
				t.Fatalf("Vary = %q, want Accept-Encoding", got)
			}
			if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, tt.contentType) {
				t.Fatalf("Content-Type = %q, want %s", got, tt.contentType)
			}
			checkSecurityHeaders(t, resp.Header)
			if resp.Header.Get("X-Request-Id") == "" {
				t.Fatal("X-Request-Id missing")
			}
			plain := gunzip(t, body)
			if len(body) >= len(plain) {
				t.Fatalf("compressed %d bytes, plain %d: not smaller", len(body), len(plain))
			}
			if tt.file != "" && !bytes.Equal(plain, fileData(t, webFS, tt.file)) {
				t.Fatalf("decoded body differs from %s", tt.file)
			}
		})
	}

	plain := []struct {
		name, path string
		headers    map[string]string
		code       int
	}{
		{"cover photo", "/api/cover", gz, http.StatusOK},
		{"PNG icon", "/apple-touch-icon.png", gz, http.StatusOK},
		{"no Accept-Encoding", "/", nil, http.StatusOK},
		{"identity only", "/api/collection", map[string]string{"Accept-Encoding": "identity"}, http.StatusOK},
	}
	for _, tt := range plain {
		t.Run("plain "+tt.name, func(t *testing.T) {
			resp, _ := fetch(t, http.MethodGet, srv.URL+tt.path, tt.headers)
			if resp.StatusCode != tt.code {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tt.code)
			}
			if got := resp.Header.Get("Content-Encoding"); got != "" {
				t.Fatalf("Content-Encoding = %q, want none", got)
			}
		})
	}
}

// TestCompressionRange checks that a Range request gets its bytes as they are
// on disk, not a slice of a compressed body.
func TestCompressionRange(t *testing.T) {
	webFS := compressFS()
	srv := newServer(t, server.Deps{DB: fakeDB{}, Web: webFS})
	resp, body := fetch(t, http.MethodGet, srv.URL+"/_app/immutable/entry/app.js",
		map[string]string{"Accept-Encoding": "gzip", "Range": "bytes=0-9"})
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want none", got)
	}
	if want := fileData(t, webFS, "_app/immutable/entry/app.js")[:10]; !bytes.Equal(body, want) {
		t.Fatalf("body = %q, want %q", body, want)
	}
}

// TestCompressionHead checks that HEAD announces the same encoding as GET
// without a body.
func TestCompressionHead(t *testing.T) {
	srv := newServer(t, server.Deps{DB: fakeDB{}, Web: compressFS()})
	resp, body := fetch(t, http.MethodHead, srv.URL+"/", map[string]string{"Accept-Encoding": "gzip"})
	if resp.StatusCode != http.StatusOK || len(body) != 0 {
		t.Fatalf("HEAD / = %d with %d bytes, want an empty 200", resp.StatusCode, len(body))
	}
	if got := resp.Header.Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
}

// TestCompressionMetrics checks that a scrape that accepts gzip gets the
// registry's own encoding once, not gzip inside gzip.
func TestCompressionMetrics(t *testing.T) {
	srv := newServer(t, server.Deps{DB: fakeDB{}})
	resp, body := fetch(t, http.MethodGet, srv.URL+"/metrics", map[string]string{"Accept-Encoding": "gzip"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	if text := gunzip(t, body); !bytes.HasPrefix(text, []byte("# HELP")) {
		t.Fatalf("metrics body starts %q, want # HELP", text[:min(len(text), 20)])
	}
}
