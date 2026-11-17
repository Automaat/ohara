// Ohara API: serves the collection API, the built frontend, /health and
// /metrics, and applies schema migrations on startup.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Automaat/ohara/backend-go/internal/collection"
	"github.com/Automaat/ohara/backend-go/internal/db"
	"github.com/Automaat/ohara/backend-go/internal/deals"
	"github.com/Automaat/ohara/backend-go/internal/lookup"
	"github.com/Automaat/ohara/backend-go/internal/lookup/books"
	"github.com/Automaat/ohara/backend-go/internal/metrics"
	"github.com/Automaat/ohara/backend-go/internal/scanqueue"
	"github.com/Automaat/ohara/backend-go/internal/server"
	"github.com/Automaat/ohara/backend-go/internal/web"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if len(os.Args) >= 2 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck())
	}
	os.Exit(run())
}

// healthcheck probes our own /health for the Docker HEALTHCHECK: the
// distroless image has no shell or curl.
func healthcheck() int {
	host, port, err := net.SplitHostPort(envOr("OHARA_ADDR", ":8000"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)
		return 1
	}
	if host == "" {
		host = "127.0.0.1"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	url := "http://" + net.JoinHostPort(host, port) + "/health"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)
		return 1
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "healthcheck: status", resp.StatusCode)
		return 1
	}
	return 0
}

func run() int {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" && os.Getenv("PGHOST") == "" {
		logger.Error("database not configured: set DATABASE_URL or the PG* variables")
		return 2
	}
	pool, err := openDB(ctx, dsn, logger)
	if err != nil {
		logger.Error("database", "err", err)
		return 2
	}
	defer pool.Close()
	registerPoolMetrics(pool)

	deps := server.Deps{DB: pool}
	lookupHandler, err := newLookup(pool, logger)
	if err != nil {
		logger.Error("lookup providers", "err", err)
		return 2
	}
	hunter, dealStore, err := newHunter(pool, logger)
	if err != nil {
		logger.Error("deal hunter", "err", err)
		return 2
	}
	huntCtx, stopHunt := context.WithCancel(ctx)
	hunter.Start(huntCtx)
	defer hunter.Wait()
	defer stopHunt()
	deps.API = []server.Mounter{
		collection.NewHandler(collection.NewStore(pool), logger),
		lookupHandler,
		scanqueue.NewHandler(scanqueue.NewStore(pool), logger),
		deals.NewHandler(dealStore, hunter, logger),
	}
	if dir := os.Getenv("OHARA_WEB_DIR"); dir != "" {
		webFS := os.DirFS(dir)
		if err := web.Check(webFS); err != nil {
			logger.Error("frontend", "dir", dir, "err", err)
			return 2
		}
		deps.Web = webFS
	}

	metrics.SetBuildInfo(version)
	addr := envOr("OHARA_ADDR", ":8000")
	srv := &http.Server{
		Addr:              addr,
		Handler:           server.New(server.Config{Version: version, AccessLog: envOr("OHARA_ACCESS_LOG", "false") == "true"}, logger, deps),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	logger.Info("ohara listening", "addr", addr, "version", version, "frontend", deps.Web != nil)

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("listen", "err", err)
			return 1
		}
	case <-ctx.Done():
		logger.Info("shutdown signal received")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Error("shutdown", "err", err)
			return 1
		}
	}
	return 0
}

// openDB opens the pool and applies pending migrations. pgx's URL parser
// needs a percent-encoded password, so deployments with arbitrary secrets
// leave DATABASE_URL empty and pass the libpq PG* variables instead.
func openDB(ctx context.Context, dsn string, logger *slog.Logger) (*pgxpool.Pool, error) {
	pool, err := db.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	migrations, err := db.EmbeddedMigrations()
	if err != nil {
		pool.Close()
		return nil, err
	}
	applied, err := db.Migrate(ctx, pool, migrations)
	if err != nil {
		pool.Close()
		return nil, err
	}
	if len(applied) > 0 {
		logger.Info("applied migrations", "versions", strings.Join(applied, ","))
	}
	return pool, nil
}

// bookCacheTTL bounds how long a book answer, a miss included, is reused;
// a week lets a new release reach the catalogs without rescanning daily.
const bookCacheTTL = 7 * 24 * time.Hour

// newLookup registers the barcode metadata providers. Registration order is
// the field priority when book answers are merged: the national library
// first, then Open Library, then Google Books when a key is set.
func newLookup(pool *pgxpool.Pool, logger *slog.Logger) (*lookup.Handler, error) {
	registry := &lookup.Registry{}
	client := &http.Client{}
	providers := []lookup.Provider{
		books.BN{Client: client},
		books.OpenLibrary{Client: client},
	}
	if key := os.Getenv("OHARA_GOOGLE_BOOKS_API_KEY"); key != "" {
		providers = append(providers, books.GoogleBooks{APIKey: key, Client: client})
	}
	for _, p := range providers {
		if err := registry.Register(p, bookCacheTTL); err != nil {
			return nil, err
		}
	}
	if envOr("OHARA_LOOKUP_STUB", "false") == "true" {
		if err := registry.Register(lookup.Stub{}, 0); err != nil {
			return nil, err
		}
	}
	logger.Info("lookup providers", "providers", strings.Join(registry.Names(), ","))
	service := lookup.NewService(registry, lookup.NewPGCache(pool), logger)
	return lookup.NewHandler(service, logger), nil
}

// newHunter configures the deal hunter from the environment. Polling stays
// off until OHARA_DEALS_INTERVAL is set; alerts need both Telegram
// variables. The *_URL variables point the sources at test doubles.
func newHunter(pool *pgxpool.Pool, logger *slog.Logger) (*deals.Hunter, *deals.Store, error) {
	interval, err := time.ParseDuration(envOr("OHARA_DEALS_INTERVAL", "0s"))
	if err != nil || interval < 0 {
		return nil, nil, fmt.Errorf("OHARA_DEALS_INTERVAL: want a duration such as 2h, got %q", os.Getenv("OHARA_DEALS_INTERVAL"))
	}
	if interval > 0 && interval < 10*time.Minute {
		return nil, nil, errors.New("OHARA_DEALS_INTERVAL: polling more often than every 10m is impolite to the sources")
	}
	delay, err := time.ParseDuration(envOr("OHARA_DEALS_DELAY", "5s"))
	if err != nil || delay < time.Second {
		return nil, nil, fmt.Errorf("OHARA_DEALS_DELAY: want a duration of at least 1s, got %q", os.Getenv("OHARA_DEALS_DELAY"))
	}
	client := deals.NewHTTPClient()
	sources := []deals.Source{
		deals.OLX{Client: client, BaseURL: envOr("OHARA_DEALS_OLX_URL", deals.OLXURL)},
		deals.Pepper{Client: client, BaseURL: envOr("OHARA_DEALS_PEPPER_URL", deals.PepperURL)},
		deals.Ceneo{Client: client, BaseURL: envOr("OHARA_DEALS_CENEO_URL", deals.CeneoURL)},
	}
	var notifier deals.Notifier
	token, chat := os.Getenv("OHARA_TELEGRAM_BOT_TOKEN"), os.Getenv("OHARA_TELEGRAM_CHAT_ID")
	switch {
	case token != "" && chat != "":
		notifier = &deals.Telegram{
			Client: client, BaseURL: envOr("OHARA_TELEGRAM_URL", deals.TelegramURL), Token: token, ChatID: chat,
		}
	case token != "" || chat != "":
		return nil, nil, errors.New("set both OHARA_TELEGRAM_BOT_TOKEN and OHARA_TELEGRAM_CHAT_ID, or neither")
	}
	logger.Info("deal hunter", "interval", interval, "delay", delay, "telegram", notifier != nil)
	store := deals.NewStore(pool)
	return deals.NewHunter(store, sources, notifier, deals.Config{Interval: interval, Delay: delay}, logger), store, nil
}

func registerPoolMetrics(pool *pgxpool.Pool) {
	metrics.RegisterPoolCollector(func() metrics.PoolStats {
		s := pool.Stat()
		return metrics.PoolStats{
			Total:             s.TotalConns(),
			Idle:              s.IdleConns(),
			Acquired:          s.AcquiredConns(),
			Max:               s.MaxConns(),
			AcquireCount:      s.AcquireCount(),
			EmptyAcquireCount: s.EmptyAcquireCount(),
		}
	})
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
