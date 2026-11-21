package server_test

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Automaat/ohara/backend-go/internal/httpx"
	"github.com/Automaat/ohara/backend-go/internal/reqlog"
	"github.com/Automaat/ohara/backend-go/internal/server"
)

// loggingAPI logs through the request's context, fails and panics.
type loggingAPI struct{ logger *slog.Logger }

func (a loggingAPI) Mount(r chi.Router) {
	r.Get("/fail", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteInternal(w, r, a.logger, "load shelf", errors.New("db down"))
	})
	r.Get("/warn", func(w http.ResponseWriter, r *http.Request) {
		a.logger.WarnContext(r.Context(), "slow source", "source", "olx")
		w.WriteHeader(http.StatusNoContent)
	})
	r.Get("/panic", func(http.ResponseWriter, *http.Request) {
		panic("shelf collapsed")
	})
}

// logLines decodes the JSON log lines written so far.
func logLines(t *testing.T, logs *lockedBuffer) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for line := range strings.Lines(logs.String()) {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		lines = append(lines, m)
	}
	return lines
}

// findLine returns the one line with msg and request_id id.
func findLine(t *testing.T, lines []map[string]any, msg, id string) map[string]any {
	t.Helper()
	var found []map[string]any
	for _, l := range lines {
		if l["msg"] == msg && l["request_id"] == id {
			found = append(found, l)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%d lines with msg %q and request_id %q, want 1:\n%v", len(found), msg, id, lines)
	}
	return found[0]
}

func TestRequestID(t *testing.T) {
	logs := &lockedBuffer{}
	logger := slog.New(reqlog.NewHandler(slog.NewJSONHandler(logs, nil)))
	deps := server.Deps{API: []server.Mounter{loggingAPI{logger: logger}}}
	srv := httptest.NewServer(server.New(server.Config{AccessLog: true}, logger, deps))
	t.Cleanup(srv.Close)
	guarded := httptest.NewServer(server.New(server.Config{AccessLog: true, RequireRemoteUser: true}, logger, deps))
	t.Cleanup(guarded.Close)

	tests := []struct {
		name   string
		url    string
		method string
		header map[string]string
		path   string
		code   int
	}{
		{"health", srv.URL, http.MethodGet, nil, "/health", http.StatusOK},
		{"handler error", srv.URL, http.MethodGet, nil, "/api/fail", http.StatusInternalServerError},
		{"context log", srv.URL, http.MethodGet, nil, "/api/warn", http.StatusNoContent},
		{"handler panic", srv.URL, http.MethodGet, nil, "/api/panic", http.StatusInternalServerError},
		{"unknown API path", srv.URL, http.MethodGet, nil, "/api/nope", http.StatusNotFound},
		{"wrong method", srv.URL, http.MethodPatch, nil, "/api/status", http.StatusMethodNotAllowed},
		{"cross-origin write", srv.URL, http.MethodPost, map[string]string{"Sec-Fetch-Site": "cross-site"}, "/api/fail", http.StatusForbidden},
		{"client-sent ID", srv.URL, http.MethodGet, map[string]string{"X-Request-Id": "forged-id"}, "/health", http.StatusOK},
		{"missing Remote-User", guarded.URL, http.MethodGet, nil, "/api/status", http.StatusUnauthorized},
	}
	ids := map[string]int{}
	for i, tt := range tests {
		req, err := http.NewRequestWithContext(t.Context(), tt.method, tt.url+tt.path, http.NoBody)
		if err != nil {
			t.Fatalf("%s: new request: %v", tt.name, err)
		}
		for k, v := range tt.header {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		resp.Body.Close()
		if resp.StatusCode != tt.code {
			t.Fatalf("%s: status = %d, want %d", tt.name, resp.StatusCode, tt.code)
		}
		id := resp.Header.Get("X-Request-Id")
		if id == "" || strings.Contains(id, "forged") {
			t.Fatalf("%s: X-Request-Id = %q, want a server-made ID", tt.name, id)
		}
		if other, dup := ids[id]; dup {
			t.Fatalf("%s and %s share X-Request-Id %q", tests[other].name, tt.name, id)
		}
		ids[id] = i
	}

	lines := logLines(t, logs)
	for id, i := range ids {
		tt := tests[i]
		access := findLine(t, lines, "request", id)
		if access["path"] != tt.path || access["method"] != tt.method {
			t.Errorf("%s: access log %v, want %s %s", tt.name, access, tt.method, tt.path)
		}

		switch tt.name {
		case "handler error":
			l := findLine(t, lines, "load shelf", id)
			if l["level"] != "ERROR" || l["err"] != "db down" || l["method"] != "GET" || l["path"] != "/api/fail" {
				t.Errorf("WriteInternal line = %v", l)
			}
		case "context log":
			l := findLine(t, lines, "slow source", id)
			if l["level"] != "WARN" || l["source"] != "olx" || l["method"] != "GET" || l["path"] != "/api/warn" {
				t.Errorf("WarnContext line = %v", l)
			}
		case "handler panic":
			l := findLine(t, lines, "handler panic", id)
			stack, _ := l["stack"].(string)
			if l["level"] != "ERROR" || l["panic"] != "shelf collapsed" || l["method"] != "GET" ||
				l["path"] != "/api/panic" || !strings.Contains(stack, "goroutine ") {
				t.Errorf("panic line = %v", l)
			}
		}
	}
}
