package server_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/go-chi/chi/v5"

	"github.com/Automaat/ohara/backend-go/internal/httpx"
	"github.com/Automaat/ohara/backend-go/internal/server"
)

const (
	appHost   = "ohara.mskalski.dev"
	appOrigin = "https://" + appHost
)

// writeAPI stands in for the domain handlers: it records which route ran.
type writeAPI struct {
	logger *slog.Logger
	called []string
}

func (a *writeAPI) Mount(r chi.Router) {
	r.Get("/titles", a.answer("GET /titles", http.StatusOK))
	r.Head("/titles", a.answer("HEAD /titles", http.StatusOK))
	r.Post("/titles", func(w http.ResponseWriter, r *http.Request) {
		a.called = append(a.called, "POST /titles")
		var in struct {
			Name string `json:"name"`
		}
		if !httpx.DecodeJSON(w, r, a.logger, &in) {
			return
		}
		httpx.WriteJSON(w, a.logger, http.StatusCreated, in)
	})
	r.Delete("/titles/{id}", a.answer("DELETE /titles/{id}", http.StatusNoContent))
	r.Post("/deals/run", a.answer("POST /deals/run", http.StatusAccepted))
	r.Post("/valuation/run", a.answer("POST /valuation/run", http.StatusAccepted))
	r.Put("/editions/{id}/cover", func(w http.ResponseWriter, r *http.Request) {
		a.called = append(a.called, "PUT /editions/{id}/cover")
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			httpx.WriteInternal(w, a.logger, "read cover", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func (a *writeAPI) answer(route string, code int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		a.called = append(a.called, route)
		w.WriteHeader(code)
	}
}

func TestCrossOriginWrites(t *testing.T) {
	sameOrigin := map[string]string{"Origin": appOrigin, "Sec-Fetch-Site": "same-origin"}
	sameSite := map[string]string{"Origin": "https://other.mskalski.dev", "Sec-Fetch-Site": "same-site"}
	crossSite := map[string]string{"Origin": "https://evil.example", "Sec-Fetch-Site": "cross-site"}
	tests := []struct {
		name        string
		method      string
		path        string
		contentType string
		body        string
		headers     map[string]string
		wantCode    int
		wantHandler string
	}{
		{"sibling site text/plain write", http.MethodPost, "/api/titles", "text/plain", `{"name":"x"}`, sameSite, http.StatusForbidden, ""},
		{"cross-site JSON write", http.MethodPost, "/api/titles", "application/json", `{"name":"x"}`, crossSite, http.StatusForbidden, ""},
		{"old browser, foreign Origin", http.MethodPost, "/api/titles", "text/plain", `{"name":"x"}`, map[string]string{"Origin": "https://other.mskalski.dev"}, http.StatusForbidden, ""},
		{"old browser, opaque Origin", http.MethodPost, "/api/titles", "text/plain", `{"name":"x"}`, map[string]string{"Origin": "null"}, http.StatusForbidden, ""},
		{"sibling site delete", http.MethodDelete, "/api/titles/1", "", "", sameSite, http.StatusForbidden, ""},
		{"sibling site deal run", http.MethodPost, "/api/deals/run", "", "", sameSite, http.StatusForbidden, ""},
		{"sibling site valuation run", http.MethodPost, "/api/valuation/run", "", "", sameSite, http.StatusForbidden, ""},
		{"sibling site cover upload", http.MethodPut, "/api/editions/1/cover", "application/octet-stream", "jpeg", sameSite, http.StatusForbidden, ""},
		{"sibling site write outside the API", http.MethodPost, "/collection", "text/plain", "x", sameSite, http.StatusForbidden, ""},

		{"same-origin JSON write", http.MethodPost, "/api/titles", "application/json", `{"name":"x"}`, sameOrigin, http.StatusCreated, "POST /titles"},
		{"same-origin text/plain write", http.MethodPost, "/api/titles", "text/plain", `{"name":"x"}`, sameOrigin, http.StatusCreated, "POST /titles"},
		{"old browser, own Origin", http.MethodPost, "/api/titles", "application/json", `{"name":"x"}`, map[string]string{"Origin": appOrigin}, http.StatusCreated, "POST /titles"},
		{"user-initiated request", http.MethodPost, "/api/titles", "application/json", `{"name":"x"}`, map[string]string{"Sec-Fetch-Site": "none"}, http.StatusCreated, "POST /titles"},
		{"no browser headers", http.MethodPost, "/api/titles", "text/plain", `{"name":"x"}`, nil, http.StatusCreated, "POST /titles"},
		{"same-origin delete", http.MethodDelete, "/api/titles/1", "", "", sameOrigin, http.StatusNoContent, "DELETE /titles/{id}"},
		{"same-origin deal run", http.MethodPost, "/api/deals/run", "", "", sameOrigin, http.StatusAccepted, "POST /deals/run"},
		{"same-origin valuation run", http.MethodPost, "/api/valuation/run", "", "", sameOrigin, http.StatusAccepted, "POST /valuation/run"},
		{"same-origin cover upload", http.MethodPut, "/api/editions/1/cover", "application/octet-stream", "jpeg", sameOrigin, http.StatusNoContent, "PUT /editions/{id}/cover"},

		{"sibling site GET", http.MethodGet, "/api/titles", "", "", sameSite, http.StatusOK, "GET /titles"},
		{"cross-site GET", http.MethodGet, "/api/titles", "", "", crossSite, http.StatusOK, "GET /titles"},
		{"cross-site HEAD", http.MethodHead, "/api/titles", "", "", crossSite, http.StatusOK, "HEAD /titles"},
		{"cross-site GET outside the API", http.MethodGet, "/collection", "", "", crossSite, http.StatusOK, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := slog.New(slog.DiscardHandler)
			api := &writeAPI{logger: logger}
			webFS := fstest.MapFS{"index.html": {Data: []byte("<title>Ohara</title>")}}
			h := server.New(server.Config{}, logger, server.Deps{Web: webFS, API: []server.Mounter{api}})

			req := httptest.NewRequestWithContext(t.Context(), tt.method, appOrigin+tt.path, strings.NewReader(tt.body))
			req.Host = appHost
			if tt.contentType != "" {
				req.Header.Set("Content-Type", tt.contentType)
			}
			for k, v := range tt.headers {
				req.Header.Set(k, v)
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
			if tt.wantCode != http.StatusForbidden {
				return
			}
			var body httpx.Error
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error == "" {
				t.Fatalf("403 body = %q, want {\"error\": ...} (%v)", rec.Body.String(), err)
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
				t.Fatalf("403 Content-Type = %q, want application/json", ct)
			}
		})
	}
}
