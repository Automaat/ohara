package server_test

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/go-chi/chi/v5"

	"github.com/Automaat/ohara/backend-go/internal/httpx"
	"github.com/Automaat/ohara/backend-go/internal/server"
)

// failingAPI answers with each kind of API error.
type failingAPI struct{ logger *slog.Logger }

func (a failingAPI) Mount(r chi.Router) {
	r.Get("/items", func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteJSON(w, a.logger, http.StatusOK, []string{})
	})
	r.Post("/items", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name string `json:"name"`
		}
		if !httpx.DecodeJSON(w, r, a.logger, &in) {
			return
		}
		httpx.WriteJSON(w, a.logger, http.StatusCreated, in)
	})
	r.Get("/broken", func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteInternal(w, a.logger, "load", errors.New("boom"))
	})
	r.Get("/panic", func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})
}

func TestSecurityHeaders(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	webFS := fstest.MapFS{
		"index.html":                  {Data: []byte("<title>Ohara</title>")},
		"manifest.webmanifest":        {Data: []byte("{}")},
		"_app/immutable/entry/app.js": {Data: []byte("export {}")},
	}
	srv := newServer(t, server.Deps{DB: fakeDB{}, Web: webFS, API: []server.Mounter{failingAPI{logger: logger}}})

	tests := []struct {
		name    string
		method  string
		path    string
		headers map[string]string
		body    string
		code    int
	}{
		{"SPA root", http.MethodGet, "/", nil, "", http.StatusOK},
		{"SPA client route", http.MethodGet, "/scan", nil, "", http.StatusOK},
		{"SPA HEAD", http.MethodHead, "/scan", nil, "", http.StatusOK},
		{"hashed asset", http.MethodGet, "/_app/immutable/entry/app.js", nil, "", http.StatusOK},
		{"manifest", http.MethodGet, "/manifest.webmanifest", nil, "", http.StatusOK},
		{"SPA write", http.MethodPost, "/scan", nil, "", http.StatusMethodNotAllowed},
		{"API JSON", http.MethodGet, "/api/items", nil, "", http.StatusOK},
		{"API status", http.MethodGet, "/api/status", nil, "", http.StatusOK},
		{"API 404", http.MethodGet, "/api/nope", nil, "", http.StatusNotFound},
		{"API 405", http.MethodDelete, "/api/items", nil, "", http.StatusMethodNotAllowed},
		{"API 422", http.MethodPost, "/api/items", nil, "{", http.StatusUnprocessableEntity},
		{"API 500", http.MethodGet, "/api/broken", nil, "", http.StatusInternalServerError},
		{"API panic", http.MethodGet, "/api/panic", nil, "", http.StatusInternalServerError},
		{"cross-origin write", http.MethodPost, "/api/items", map[string]string{"Sec-Fetch-Site": "cross-site"}, "{}", http.StatusForbidden},
		{"health", http.MethodGet, "/health", nil, "", http.StatusOK},
		{"metrics", http.MethodGet, "/metrics", nil, "", http.StatusOK},
	}
	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "same-origin",
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(t.Context(), tt.method, srv.URL+tt.path, strings.NewReader(tt.body))
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			if tt.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("%s %s: %v", tt.method, tt.path, err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tt.code {
				t.Fatalf("%s %s = %d, want %d", tt.method, tt.path, resp.StatusCode, tt.code)
			}
			for name, value := range want {
				if got := resp.Header.Get(name); got != value {
					t.Errorf("%s = %q, want %q", name, got, value)
				}
			}
			policy := resp.Header.Get("Permissions-Policy")
			if !strings.Contains(policy, "camera=(self)") {
				t.Errorf("Permissions-Policy = %q, want camera=(self)", policy)
			}
		})
	}
}
