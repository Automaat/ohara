// Package server wires the HTTP router and middleware.
package server

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"runtime/debug"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/Automaat/ohara/backend-go/internal/httpx"
	"github.com/Automaat/ohara/backend-go/internal/metrics"
	"github.com/Automaat/ohara/backend-go/internal/web"
)

const requestTimeout = 30 * time.Second

// Pinger is the part of *pgxpool.Pool the health probe needs.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Config holds the resolved runtime settings; cmd/api owns env loading.
// Version names the running build in /api/status. AccessLog adds one log line
// per request; metrics are recorded either way.
type Config struct {
	Version   string
	AccessLog bool
}

// Mounter adds a domain's routes to the /api router.
type Mounter interface {
	Mount(r chi.Router)
}

// Deps are the runtime dependencies. A nil DB reports the database as
// unconfigured; a nil Web serves no frontend. API holds the domain handlers
// mounted under /api.
type Deps struct {
	DB  Pinger
	Web fs.FS
	API []Mounter
}

// New returns the router: probes, metrics, the JSON API and the frontend.
func New(cfg Config, logger *slog.Logger, deps Deps) http.Handler {
	return newRouter(cfg, logger, deps, requestTimeout)
}

// newRouter is New with the request timeout as a parameter, so tests can
// shorten it.
func newRouter(cfg Config, logger *slog.Logger, deps Deps, timeout time.Duration) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	r := chi.NewRouter()
	r.Use(securityHeaders)
	r.Use(generalOptions)
	r.Use(middleware.RequestID)
	r.Use(requestObserver(logger, cfg.AccessLog))
	r.Use(recoverer(logger))
	r.Use(timeoutAfter(timeout, logger))
	r.Use(crossOriginGuard(logger))
	r.MethodNotAllowed(methodNotAllowed(r, logger))

	r.Get("/health", healthHandler(logger, deps.DB))
	r.Handle("/metrics", metrics.Handler())

	r.Route("/api", func(r chi.Router) {
		r.Get("/status", statusHandler(logger, cfg.Version, deps.DB))
		for _, m := range deps.API {
			m.Mount(r)
		}
		r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
			httpx.WriteError(w, logger, http.StatusNotFound, "not found")
		})
	})

	if deps.Web != nil {
		r.Handle("/*", web.Handler(deps.Web))
	}
	return r
}

// allowCandidates are the methods a 405 can list in its Allow header.
var allowCandidates = []string{
	http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch,
	http.MethodDelete, http.MethodConnect, http.MethodOptions, http.MethodTrace,
}

// methodNotAllowed answers a known path asked with a method it has no route
// for, with the API's JSON error. chi does not pass the allowed methods to a
// custom handler, so the Allow header its own answer carries is rebuilt from
// the routes.
func methodNotAllowed(routes chi.Routes, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.RawPath
		if path == "" {
			path = r.URL.Path
		}
		for _, m := range allowCandidates {
			if routes.Match(chi.NewRouteContext(), m, path) {
				w.Header().Add("Allow", m)
			}
		}
		httpx.WriteError(w, logger, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// recoverer turns a handler panic into a logged error, with its stack, and a
// JSON 500 when nothing was written yet. Like chi's Recoverer it lets
// http.ErrAbortHandler through and sends no 500 on a protocol upgrade.
func recoverer(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			defer func() {
				rvr := recover()
				if rvr == nil {
					return
				}
				if err, ok := rvr.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(rvr)
				}
				logger.Error("handler panic",
					"request_id", middleware.GetReqID(r.Context()),
					"method", r.Method,
					"path", r.URL.Path,
					"panic", rvr,
					"stack", string(debug.Stack()),
				)
				if ww.Status() == 0 && r.Header.Get("Connection") != "Upgrade" {
					httpx.WriteError(ww, logger, http.StatusInternalServerError, "internal server error")
				}
			}()
			next.ServeHTTP(ww, r)
		})
	}
}

// timeoutAfter cancels the request context after d. A handler that returns
// past the deadline without writing gets a JSON 504; one that already wrote
// keeps its answer.
func timeoutAfter(d time.Duration, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			defer func() {
				cancel()
				if errors.Is(ctx.Err(), context.DeadlineExceeded) && ww.Status() == 0 {
					httpx.WriteError(ww, logger, http.StatusGatewayTimeout, "gateway timeout")
				}
			}()
			next.ServeHTTP(ww, r.WithContext(ctx))
		})
	}
}

// permissionsPolicy lets only Ohara's own pages open the camera, for the
// scanner, and denies the other features the SPA never uses.
const permissionsPolicy = "camera=(self), microphone=(), geolocation=()"

// securityHeaders adds the browser security headers to every response. It
// runs first, so the recoverer's 500, the timeout's 504 and the router's own
// 404 and 405 answers carry them too.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Permissions-Policy", permissionsPolicy)
		next.ServeHTTP(w, r)
	})
}

// generalOptions answers "OPTIONS *" with an empty 200, as net/http does when
// the server's DisableGeneralOptionsHandler is false. cmd/api sets it, so the
// request reaches the router and its answer carries the security headers.
func generalOptions(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodOptions || r.RequestURI != "*" {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Length", "0")
		if r.ContentLength != 0 {
			_, _ = io.Copy(io.Discard, http.MaxBytesReader(w, r.Body, 4<<10))
		}
		w.WriteHeader(http.StatusOK)
	})
}

// crossOriginGuard answers 403 to a cross-origin write. Authelia's session
// cookie covers every sibling site of the parent domain, so their pages could
// otherwise write here.
func crossOriginGuard(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if crossOriginWrite(r) {
				httpx.WriteError(w, logger, http.StatusForbidden, "cross-origin request rejected")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// crossOriginWrite reports a request other than GET, HEAD and OPTIONS that the
// browser marks as cross-origin: a Sec-Fetch-Site other than same-origin or
// none, or an Origin whose host is not Host. Each header decides on its own,
// unlike http.CrossOriginProtection, which ignores Origin once Sec-Fetch-Site
// is set. Clients that send neither header, like curl, pass.
func crossOriginWrite(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "same-origin", "none":
	default:
		return true
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil {
		return true
	}
	return u.Host != r.Host
}

func requestObserver(logger *slog.Logger, accessLog bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			dur := time.Since(start)
			route := chi.RouteContext(r.Context()).RoutePattern()
			status := ww.Status()
			if status == 0 {
				status = http.StatusOK
			}
			metrics.ObserveRequest(r.Method, route, status, dur)
			if !accessLog {
				return
			}
			logger.Info("request",
				"request_id", middleware.GetReqID(r.Context()),
				"method", r.Method,
				"path", r.URL.Path,
				"route", route,
				"status", status,
				"bytes", ww.BytesWritten(),
				"latency_ms", float64(dur.Microseconds())/1000.0,
			)
		})
	}
}

// dbState is "ok", "unavailable" or "unconfigured".
func dbState(ctx context.Context, logger *slog.Logger, db Pinger) string {
	if db == nil {
		return "unconfigured"
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := db.Ping(ctx); err != nil {
		logger.Error("database ping failed", "err", err)
		return "unavailable"
	}
	return "ok"
}

func healthHandler(logger *slog.Logger, db Pinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if dbState(r.Context(), logger, db) == "unavailable" {
			writeJSON(w, logger, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
			return
		}
		writeJSON(w, logger, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// Status is the /api/status body the frontend shows on its home page.
type Status struct {
	Status   string `json:"status"`
	Version  string `json:"version"`
	Database string `json:"database"`
}

func statusHandler(logger *slog.Logger, version string, db Pinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		st := Status{Status: "ok", Version: version, Database: dbState(r.Context(), logger, db)}
		code := http.StatusOK
		if st.Database == "unavailable" {
			st.Status = "degraded"
			code = http.StatusServiceUnavailable
		}
		writeJSON(w, logger, code, st)
	}
}

func writeJSON(w http.ResponseWriter, logger *slog.Logger, code int, body any) {
	httpx.WriteJSON(w, logger, code, body)
}
