package server_test

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Automaat/ohara/backend-go/internal/reqlog"
	"github.com/Automaat/ohara/backend-go/internal/server"
)

func shareRouter(share http.Handler) http.Handler {
	logger := slog.New(slog.DiscardHandler)
	webFS := fstest.MapFS{"index.html": {Data: []byte("<title>Ohara</title>")}}
	return server.New(server.Config{RequireRemoteUser: true}, logger,
		server.Deps{DB: fakeDB{}, Web: webFS, API: []server.Mounter{&writeAPI{logger: logger}}, Share: share})
}

func TestShareReadsSkipRemoteUser(t *testing.T) {
	share := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Share", "1")
		w.WriteHeader(http.StatusTeapot)
	})
	tests := []struct {
		name, method, path string
		withShare          bool
		code               int
	}{
		{"share read", http.MethodGet, "/share/abc", true, http.StatusTeapot},
		{"share data read", http.MethodGet, "/share/abc/wishes", true, http.StatusTeapot},
		{"share prefix alone", http.MethodGet, "/share", true, http.StatusUnauthorized},
		{"share write", http.MethodPost, "/share/abc", true, http.StatusUnauthorized},
		{"share HEAD", http.MethodHead, "/share/abc", true, http.StatusUnauthorized},
		{"share spelled with an escape", http.MethodGet, "/%73hare/abc", true, http.StatusUnauthorized},
		{"lookalike prefix", http.MethodGet, "/shared/abc", true, http.StatusUnauthorized},
		{"API beside share", http.MethodGet, "/api/titles", true, http.StatusUnauthorized},
		{"SPA beside share", http.MethodGet, "/deals", true, http.StatusUnauthorized},
		{"share off", http.MethodGet, "/share/abc", false, http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var h http.Handler
			if tt.withShare {
				h = shareRouter(share)
			} else {
				h = shareRouter(nil)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), tt.method, tt.path, http.NoBody))
			if rec.Code != tt.code {
				t.Fatalf("%s %s = %d, want %d", tt.method, tt.path, rec.Code, tt.code)
			}
			if reached := rec.Header().Get("X-Share") == "1"; reached != (tt.code == http.StatusTeapot) {
				t.Errorf("share handler reached = %v", reached)
			}
		})
	}
}

func TestShareTokenStaysOutOfLogs(t *testing.T) {
	const token = "SecretToken0123456789abcdefghijklmnopqrstu"
	var buf bytes.Buffer
	logger := slog.New(reqlog.NewHandler(slog.NewTextHandler(&buf, nil)))
	share := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/boom") {
			panic("share failed")
		}
		logger.ErrorContext(r.Context(), "shared wishes", "err", "db down")
		w.WriteHeader(http.StatusInternalServerError)
	})
	h := server.New(server.Config{AccessLog: true, RequireRemoteUser: true}, logger,
		server.Deps{DB: fakeDB{}, Share: share})
	for _, path := range []string{"/share/" + token, "/share/" + token + "/wishes", "/share/" + token + "/boom"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("GET %s = %d", path, rec.Code)
		}
	}
	logs := buf.String()
	if strings.Contains(logs, token) {
		t.Fatalf("logs carry the share token:\n%s", logs)
	}
	for _, want := range []string{
		"path=/share/{token}", "path=/share/{token}/wishes", "path=/share/{token}/boom",
		`msg="shared wishes"`, `msg="handler panic"`, `msg=request`,
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("logs lack %q:\n%s", want, logs)
		}
	}
}
