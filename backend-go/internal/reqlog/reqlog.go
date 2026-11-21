// Package reqlog ties log lines to the HTTP request they were written for.
// The router stores the request's ID, method and path in its context with
// WithRequest; a logger built on NewHandler adds them to every record logged
// with that context, through the *Context logging calls. Records without
// request info, such as startup and background runs, pass through unchanged.
package reqlog

import (
	"context"
	"log/slog"
)

type ctxKey struct{}

type request struct {
	id, method, path string
}

// WithRequest returns ctx carrying the request's ID, method and path.
func WithRequest(ctx context.Context, id, method, path string) context.Context {
	return context.WithValue(ctx, ctxKey{}, request{id: id, method: method, path: path})
}

// Detach returns ctx without request info, for work that outlives the
// request, such as a token refresh run on its own context: its lines keep the
// form of lines logged outside a request.
func Detach(ctx context.Context) context.Context {
	return context.WithValue(ctx, ctxKey{}, nil)
}

// NewHandler wraps next so a record logged with a request's context carries
// request_id, method and path. Like any record attribute, they land inside a
// group opened with WithGroup.
func NewHandler(next slog.Handler) slog.Handler {
	return handler{next: next}
}

type handler struct {
	next slog.Handler
}

func (h handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h handler) Handle(ctx context.Context, r slog.Record) error {
	if req, ok := ctx.Value(ctxKey{}).(request); ok {
		r = r.Clone()
		r.AddAttrs(
			slog.String("request_id", req.id),
			slog.String("method", req.method),
			slog.String("path", req.path),
		)
	}
	return h.next.Handle(ctx, r)
}

func (h handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return handler{next: h.next.WithAttrs(attrs)}
}

func (h handler) WithGroup(name string) slog.Handler {
	return handler{next: h.next.WithGroup(name)}
}
