package reqlog_test

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/Automaat/ohara/backend-go/internal/reqlog"
)

// textLogger returns a logger writing key=value lines into buf, without the
// time so lines compare exactly; wrap adds the reqlog handler.
func textLogger(buf *bytes.Buffer, wrap bool) *slog.Logger {
	var h slog.Handler = slog.NewTextHandler(buf, &slog.HandlerOptions{
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) == 0 && a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	})
	if wrap {
		h = reqlog.NewHandler(h)
	}
	return slog.New(h)
}

func TestHandlerAddsRequestAttrs(t *testing.T) {
	ctx := reqlog.WithRequest(t.Context(), "host/abc-000001", "GET", "/api/titles")
	tests := []struct {
		name string
		log  func(*slog.Logger)
		want string
	}{
		{
			"context call",
			func(l *slog.Logger) { l.WarnContext(ctx, "lookup failed", "err", "boom") },
			`level=WARN msg="lookup failed" err=boom request_id=host/abc-000001 method=GET path=/api/titles` + "\n",
		},
		{
			"logger with attrs",
			func(l *slog.Logger) { l.With("op", "save").ErrorContext(ctx, "failed") },
			`level=ERROR msg=failed op=save request_id=host/abc-000001 method=GET path=/api/titles` + "\n",
		},
		{
			"logger with a group",
			func(l *slog.Logger) { l.WithGroup("g").InfoContext(ctx, "x", "k", 1) },
			`level=INFO msg=x g.k=1 g.request_id=host/abc-000001 g.method=GET g.path=/api/titles` + "\n",
		},
		{
			"call without a context",
			func(l *slog.Logger) { l.Warn("lookup failed", "err", "boom") },
			`level=WARN msg="lookup failed" err=boom` + "\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			tt.log(textLogger(&buf, true))
			if got := buf.String(); got != tt.want {
				t.Errorf("log line\n got %q\nwant %q", got, tt.want)
			}
		})
	}
}

// Lines logged without a request, such as startup and background runs, look
// the same as without the wrapper.
func TestHandlerKeepsLinesOutsideRequests(t *testing.T) {
	logs := func(l *slog.Logger) {
		l.Info("Remote-User check", "enabled", false)
		l.InfoContext(context.Background(), "deal run finished", "listings", 3)
		l.With("task", "valuation").ErrorContext(t.Context(), "run failed", "err", "db down")
	}
	var plain, wrapped bytes.Buffer
	logs(textLogger(&plain, false))
	logs(textLogger(&wrapped, true))
	if plain.String() != wrapped.String() {
		t.Errorf("wrapped output differs\n got %q\nwant %q", wrapped.String(), plain.String())
	}
}

func TestHandlerKeepsLevel(t *testing.T) {
	h := reqlog.NewHandler(slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelWarn}))
	if h.Enabled(t.Context(), slog.LevelInfo) {
		t.Error("INFO enabled below a WARN level")
	}
	if !h.Enabled(t.Context(), slog.LevelError) {
		t.Error("ERROR disabled at a WARN level")
	}
}
