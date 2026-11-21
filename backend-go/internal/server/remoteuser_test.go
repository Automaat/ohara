package server_test

import (
	"bufio"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Automaat/ohara/backend-go/internal/httpx"
	"github.com/Automaat/ohara/backend-go/internal/server"
)

// remoteUserRouter is the router with the stand-in API, a two-file frontend
// and a healthy database.
func remoteUserRouter(require bool) (http.Handler, *writeAPI) {
	logger := slog.New(slog.DiscardHandler)
	api := &writeAPI{logger: logger}
	webFS := fstest.MapFS{
		"index.html":            {Data: []byte("<title>Ohara</title>")},
		"_app/immutable/app.js": {Data: []byte("console.log(1)")},
	}
	cfg := server.Config{RequireRemoteUser: require}
	return server.New(cfg, logger, server.Deps{DB: fakeDB{}, Web: webFS, API: []server.Mounter{api}}), api
}

func TestRemoteUserRequired(t *testing.T) {
	tests := []struct {
		name        string
		method      string
		path        string
		body        string
		remoteUser  []string
		wantCode    int
		wantHandler string
	}{
		{"API read without header", http.MethodGet, "/api/titles", "", nil, http.StatusUnauthorized, ""},
		{"API write without header", http.MethodPost, "/api/titles", `{"name":"x"}`, nil, http.StatusUnauthorized, ""},
		{"API delete without header", http.MethodDelete, "/api/titles/1", "", nil, http.StatusUnauthorized, ""},
		{"API status without header", http.MethodGet, "/api/status", "", nil, http.StatusUnauthorized, ""},
		{"unknown API path without header", http.MethodGet, "/api/nope", "", nil, http.StatusUnauthorized, ""},
		{"SPA page without header", http.MethodGet, "/collection", "", nil, http.StatusUnauthorized, ""},
		{"SPA root without header", http.MethodGet, "/", "", nil, http.StatusUnauthorized, ""},
		{"static file without header", http.MethodGet, "/_app/immutable/app.js", "", nil, http.StatusUnauthorized, ""},
		{"API read with empty header", http.MethodGet, "/api/titles", "", []string{""}, http.StatusUnauthorized, ""},
		{"health below its path", http.MethodGet, "/health/x", "", nil, http.StatusUnauthorized, ""},
		{"metrics with a trailing slash", http.MethodGet, "/metrics/", "", nil, http.StatusUnauthorized, ""},
		{"health spelled with an escape", http.MethodGet, "/heal%74h", "", nil, http.StatusUnauthorized, ""},
		{"metrics spelled with an escape", http.MethodGet, "/metric%73", "", nil, http.StatusUnauthorized, ""},
		{"health HEAD without header", http.MethodHead, "/health", "", nil, http.StatusUnauthorized, ""},
		{"metrics HEAD without header", http.MethodHead, "/metrics", "", nil, http.StatusUnauthorized, ""},
		{"health POST without header", http.MethodPost, "/health", "", nil, http.StatusUnauthorized, ""},

		{"health without header", http.MethodGet, "/health", "", nil, http.StatusOK, ""},
		{"metrics without header", http.MethodGet, "/metrics", "", nil, http.StatusOK, ""},

		{"API read with header", http.MethodGet, "/api/titles", "", []string{"someone"}, http.StatusOK, "GET /titles"},
		{"API write with header", http.MethodPost, "/api/titles", `{"name":"x"}`, []string{"someone"}, http.StatusCreated, "POST /titles"},
		{"API delete with header", http.MethodDelete, "/api/titles/1", "", []string{"someone"}, http.StatusNoContent, "DELETE /titles/{id}"},
		{"API status with header", http.MethodGet, "/api/status", "", []string{"someone"}, http.StatusOK, ""},
		{"unknown API path with header", http.MethodGet, "/api/nope", "", []string{"someone"}, http.StatusNotFound, ""},
		{"SPA page with header", http.MethodGet, "/collection", "", []string{"someone"}, http.StatusOK, ""},
		{"static file with header", http.MethodGet, "/_app/immutable/app.js", "", []string{"someone"}, http.StatusOK, ""},
		{"health with header", http.MethodGet, "/health", "", []string{"someone"}, http.StatusOK, ""},
		{"metrics with header", http.MethodGet, "/metrics", "", []string{"someone"}, http.StatusOK, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, api := remoteUserRouter(true)
			req := httptest.NewRequestWithContext(t.Context(), tt.method, appOrigin+tt.path, strings.NewReader(tt.body))
			req.Host = appHost
			if tt.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			for _, v := range tt.remoteUser {
				req.Header.Add("Remote-User", v)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tt.wantCode {
				t.Fatalf("%s %s = %d %q, want %d", tt.method, tt.path, rec.Code, rec.Body.String(), tt.wantCode)
			}
			switch {
			case tt.wantHandler != "":
				if len(api.called) != 1 || api.called[0] != tt.wantHandler {
					t.Fatalf("handlers run = %v, want [%s]", api.called, tt.wantHandler)
				}
			case len(api.called) != 0:
				t.Fatalf("handlers run = %v, want none", api.called)
			}
			if tt.wantCode != http.StatusUnauthorized {
				return
			}
			if loc := rec.Header().Get("Location"); loc != "" {
				t.Fatalf("401 carries Location %q, want no redirect", loc)
			}
			var body httpx.Error
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error == "" {
				t.Fatalf("401 body = %q, want {\"error\": ...} (%v)", rec.Body.String(), err)
			}
		})
	}
}

func TestRemoteUserNotRequired(t *testing.T) {
	tests := []struct {
		name        string
		method      string
		path        string
		wantCode    int
		wantHandler string
	}{
		{"API read", http.MethodGet, "/api/titles", http.StatusOK, "GET /titles"},
		{"API delete", http.MethodDelete, "/api/titles/1", http.StatusNoContent, "DELETE /titles/{id}"},
		{"SPA page", http.MethodGet, "/collection", http.StatusOK, ""},
		{"static file", http.MethodGet, "/_app/immutable/app.js", http.StatusOK, ""},
		{"health", http.MethodGet, "/health", http.StatusOK, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, api := remoteUserRouter(false)
			req := httptest.NewRequestWithContext(t.Context(), tt.method, appOrigin+tt.path, http.NoBody)
			req.Host = appHost
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tt.wantCode {
				t.Fatalf("%s %s = %d %q, want %d", tt.method, tt.path, rec.Code, rec.Body.String(), tt.wantCode)
			}
			if tt.wantHandler != "" && (len(api.called) != 1 || api.called[0] != tt.wantHandler) {
				t.Fatalf("handlers run = %v, want [%s]", api.called, tt.wantHandler)
			}
		})
	}
}

// TestRemoteUserGeneralOptions checks "OPTIONS *": an empty 200 with the
// header, a counted JSON 401 without it.
func TestRemoteUserGeneralOptions(t *testing.T) {
	h, _ := remoteUserRouter(true)
	const series = `ohara_http_requests_total{method="OPTIONS",route="unmatched",status="4xx"}`
	before := scrapeCounter(t, h, series)

	for _, tt := range []struct {
		name       string
		remoteUser string
		wantCode   int
	}{
		{"without header", "", http.StatusUnauthorized},
		{"with header", "someone", http.StatusOK},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodOptions, "*", http.NoBody)
			if tt.remoteUser != "" {
				req.Header.Set("Remote-User", tt.remoteUser)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tt.wantCode {
				t.Fatalf("OPTIONS * = %d %q, want %d", rec.Code, rec.Body.String(), tt.wantCode)
			}
			if tt.wantCode == http.StatusOK && rec.Body.Len() != 0 {
				t.Fatalf("OPTIONS * body = %q, want empty", rec.Body.String())
			}
			if tt.wantCode != http.StatusUnauthorized {
				return
			}
			var body httpx.Error
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error == "" {
				t.Fatalf("401 body = %q, want {\"error\": ...} (%v)", rec.Body.String(), err)
			}
		})
	}

	if after := scrapeCounter(t, h, series); after != before+1 {
		t.Fatalf("%s went from %v to %v, want one more", series, before, after)
	}
}

// TestRemoteUserRejectionCounted checks that the request observer still sees
// the 401: it runs before the guard.
func TestRemoteUserRejectionCounted(t *testing.T) {
	h, _ := remoteUserRouter(true)
	const series = `ohara_http_requests_total{method="GET",route="unmatched",status="4xx"}`
	before := scrapeCounter(t, h, series)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, appOrigin+"/api/titles", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET /api/titles = %d, want 401", rec.Code)
	}

	if after := scrapeCounter(t, h, series); after != before+1 {
		t.Fatalf("%s went from %v to %v, want one more", series, before, after)
	}
}

// scrapeCounter reads one series from /metrics, 0 when it is not there yet.
func scrapeCounter(t *testing.T, h http.Handler, series string) float64 {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, appOrigin+"/metrics", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/metrics = %d", rec.Code)
	}
	sc := bufio.NewScanner(rec.Body)
	for sc.Scan() {
		value, ok := strings.CutPrefix(sc.Text(), series+" ")
		if !ok {
			continue
		}
		v, err := strconv.ParseFloat(value, 64)
		if err != nil {
			t.Fatalf("parse %q: %v", sc.Text(), err)
		}
		return v
	}
	return 0
}
