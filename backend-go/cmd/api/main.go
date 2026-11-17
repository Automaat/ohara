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
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Automaat/ohara/backend-go/internal/collection"
	"github.com/Automaat/ohara/backend-go/internal/db"
	"github.com/Automaat/ohara/backend-go/internal/deals"
	"github.com/Automaat/ohara/backend-go/internal/discogs"
	"github.com/Automaat/ohara/backend-go/internal/lookup"
	"github.com/Automaat/ohara/backend-go/internal/lookup/books"
	"github.com/Automaat/ohara/backend-go/internal/lookup/movie"
	"github.com/Automaat/ohara/backend-go/internal/lookup/music"
	"github.com/Automaat/ohara/backend-go/internal/metrics"
	"github.com/Automaat/ohara/backend-go/internal/scanqueue"
	"github.com/Automaat/ohara/backend-go/internal/server"
	"github.com/Automaat/ohara/backend-go/internal/valuation"
	"github.com/Automaat/ohara/backend-go/internal/web"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if len(os.Args) >= 2 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck())
	}
	if len(os.Args) >= 2 && os.Args[1] == "allegro-login" {
		os.Exit(allegroLogin())
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
	valuer, valueStore, err := newValuation(pool, logger)
	if err != nil {
		logger.Error("valuation", "err", err)
		return 2
	}
	huntCtx, stopHunt := context.WithCancel(ctx)
	hunter.Start(huntCtx)
	valuer.Start(huntCtx)
	defer valuer.Wait()
	defer hunter.Wait()
	defer stopHunt()
	queue := scanqueue.NewStore(pool)
	metrics.RegisterScanQueueCollector(queue.Count)
	deps.API = []server.Mounter{
		collection.NewHandler(collection.NewStore(pool), logger),
		lookupHandler,
		scanqueue.NewHandler(queue, logger),
		deals.NewHandler(dealStore, hunter, logger),
		valuation.NewHandler(valueStore, valuer, logger),
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

// movieCacheTTL keeps a movie chain answer, "no match" included, for a week:
// catalogs add new releases, but a rescan should not cost requests.
const movieCacheTTL = 7 * 24 * time.Hour

// musicCacheTTL keeps a music answer for a week, like the movie chain's.
const musicCacheTTL = 7 * 24 * time.Hour

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
		metrics.InitSource(p.Name())
	}
	if p := newMovieProvider(pool, logger); p != nil {
		if err := registry.Register(p, movieCacheTTL); err != nil {
			return nil, err
		}
	}
	if p := newMusicProvider(logger); p != nil {
		if err := registry.Register(p, musicCacheTTL); err != nil {
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

// sourceClient is shared by the external sources; the lookup service bounds
// each provider call too.
var sourceClient = &http.Client{Timeout: 8 * time.Second}

// newMovieProvider builds the movie chain, or nil without a TMDB key: the
// chain ends in a TMDB search. Allegro joins when its app credentials are
// set; Ceneo and UPCitemdb can be switched off.
func newMovieProvider(pool *pgxpool.Pool, logger *slog.Logger) *movie.Provider {
	key := os.Getenv("OHARA_TMDB_API_KEY")
	if key == "" {
		logger.Info("movie lookup disabled: OHARA_TMDB_API_KEY not set")
		return nil
	}
	p := &movie.Provider{TMDB: movie.TMDB{Client: sourceClient, Key: key}}
	if a := newAllegro(pool); a != nil {
		p.Sources = append(p.Sources, a)
	}
	if envOr("OHARA_CENEO", "true") == "true" {
		p.Sources = append(p.Sources, movie.Ceneo{Client: sourceClient})
	}
	if envOr("OHARA_UPCITEMDB", "true") == "true" {
		p.Sources = append(p.Sources, movie.UPCitemdb{Client: sourceClient})
	}
	names := make([]string, 0, len(p.Sources))
	for _, s := range p.Sources {
		names = append(names, s.Name())
		metrics.InitSource(s.Name())
	}
	metrics.InitSource("tmdb")
	logger.Info("movie lookup sources", "sources", strings.Join(names, ","))
	return p
}

// newDiscogs is the Discogs client, or nil without OHARA_DISCOGS_TOKEN:
// every Discogs endpoint needs a token.
func newDiscogs() *discogs.Client {
	token := os.Getenv("OHARA_DISCOGS_TOKEN")
	if token == "" {
		return nil
	}
	return &discogs.Client{HTTP: sourceClient, BaseURL: envOr("OHARA_DISCOGS_URL", discogs.BaseURL), Token: token}
}

// newMusicProvider builds the music chain: Discogs when a token is set,
// then MusicBrainz with Cover Art Archive covers unless switched off; nil
// when neither is on.
func newMusicProvider(logger *slog.Logger) *music.Provider {
	p := &music.Provider{}
	var names []string
	if d := newDiscogs(); d != nil {
		p.Discogs = d
		names = append(names, discogs.Source)
	} else {
		logger.Info("discogs lookup disabled: OHARA_DISCOGS_TOKEN not set")
	}
	if envOr("OHARA_MUSICBRAINZ", "true") == "true" {
		p.MusicBrainz = music.MusicBrainz{Client: sourceClient, BaseURL: envOr("OHARA_MUSICBRAINZ_URL", music.MusicBrainzURL)}
		p.Covers = music.CoverArt{Client: sourceClient, BaseURL: envOr("OHARA_COVERART_URL", music.CoverArtURL)}
		names = append(names, music.SourceMusicBrainz, music.SourceCoverArt)
	}
	if len(names) == 0 {
		logger.Info("music lookup disabled: no Discogs token and MusicBrainz switched off")
		return nil
	}
	for _, n := range names {
		metrics.InitSource(n)
	}
	logger.Info("music lookup sources", "sources", strings.Join(names, ","))
	return p
}

func newAllegro(pool *pgxpool.Pool) *movie.Allegro {
	id, secret := os.Getenv("OHARA_ALLEGRO_CLIENT_ID"), os.Getenv("OHARA_ALLEGRO_CLIENT_SECRET")
	if id == "" || secret == "" {
		return nil
	}
	return &movie.Allegro{ClientID: id, ClientSecret: secret, Store: movie.PGTokenStore{Pool: pool}, Client: sourceClient}
}

// allegroLogin runs the one-time Allegro device flow and stores the token
// pair, for example `docker exec -it ohara api allegro-login`.
func allegroLogin() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	pool, err := openDB(ctx, os.Getenv("DATABASE_URL"), logger)
	if err != nil {
		fmt.Fprintln(os.Stderr, "allegro-login: database:", err)
		return 2
	}
	defer pool.Close()
	a := newAllegro(pool)
	if a == nil {
		fmt.Fprintln(os.Stderr, "allegro-login: set OHARA_ALLEGRO_CLIENT_ID and OHARA_ALLEGRO_CLIENT_SECRET")
		return 2
	}
	if err := a.Login(ctx, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "allegro-login:", err)
		return 1
	}
	return 0
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
	delay, err := sourceDelay()
	if err != nil {
		return nil, nil, err
	}
	client := deals.NewHTTPClient()
	sources := []deals.Source{
		olxSource(client),
		deals.Pepper{Client: client, BaseURL: envOr("OHARA_DEALS_PEPPER_URL", deals.PepperURL)},
		ceneoSource(client),
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
	cfg := deals.Config{Interval: interval, Delay: delay, CanaryQuery: envOr("OHARA_DEALS_CANARY_QUERY", "dvd")}
	return deals.NewHunter(store, sources, notifier, cfg, logger), store, nil
}

// sourceDelay is the pause between two requests to the deal and valuation
// sources.
func sourceDelay() (time.Duration, error) {
	delay, err := time.ParseDuration(envOr("OHARA_DEALS_DELAY", "5s"))
	if err != nil || delay < time.Second {
		return 0, fmt.Errorf("OHARA_DEALS_DELAY: want a duration of at least 1s, got %q", os.Getenv("OHARA_DEALS_DELAY"))
	}
	return delay, nil
}

func olxSource(client *http.Client) deals.OLX {
	return deals.OLX{Client: client, BaseURL: envOr("OHARA_DEALS_OLX_URL", deals.OLXURL)}
}

func ceneoSource(client *http.Client) deals.Ceneo {
	return deals.Ceneo{Client: client, BaseURL: envOr("OHARA_DEALS_CENEO_URL", deals.CeneoURL)}
}

// newValuation configures the valuation runner from the environment. The
// daily schedule stays off until OHARA_VALUATION_INTERVAL is set; it asks
// the deal hunter's OLX and Ceneo, with the same pause, SkupSzop and, with
// a Discogs token, the Discogs marketplace.
func newValuation(pool *pgxpool.Pool, logger *slog.Logger) (*valuation.Runner, *valuation.Store, error) {
	interval, err := time.ParseDuration(envOr("OHARA_VALUATION_INTERVAL", "0s"))
	if err != nil || interval < 0 {
		return nil, nil, fmt.Errorf("OHARA_VALUATION_INTERVAL: want a duration such as 24h, got %q", os.Getenv("OHARA_VALUATION_INTERVAL"))
	}
	if interval > 0 && interval < time.Hour {
		return nil, nil, errors.New("OHARA_VALUATION_INTERVAL: valuing more often than every 1h is impolite to the sources")
	}
	days, err := strconv.Atoi(envOr("OHARA_VALUATION_WINDOW_DAYS", "90"))
	if err != nil || days < 1 || days > 3650 {
		return nil, nil, fmt.Errorf("OHARA_VALUATION_WINDOW_DAYS: want a number of days from 1 to 3650, got %q", os.Getenv("OHARA_VALUATION_WINDOW_DAYS"))
	}
	delay, err := sourceDelay()
	if err != nil {
		return nil, nil, err
	}
	client := deals.NewHTTPClient()
	sources := []deals.Source{olxSource(client), ceneoSource(client)}
	buybacks := []valuation.BuyBack{
		valuation.SkupSzop{Client: client, BaseURL: envOr("OHARA_VALUATION_SKUPSZOP_URL", valuation.SkupSzopURL)},
	}
	window := time.Duration(days) * 24 * time.Hour
	var pricer valuation.ReleasePricer
	if d := newDiscogs(); d != nil {
		pricer = valuation.Discogs{
			Market:      d,
			Rates:       &valuation.NBP{Client: client, BaseURL: envOr("OHARA_VALUATION_NBP_URL", valuation.NBPURL)},
			Suggestions: envOr("OHARA_DISCOGS_PRICE_SUGGESTIONS", "true") == "true",
		}
		metrics.InitSource(valuation.SourceNBP)
	}
	logger.Info("valuation", "interval", interval, "window_days", days, "discogs", pricer != nil)
	store := valuation.NewStore(pool)
	cfg := valuation.Config{Interval: interval, Delay: delay, Window: window}
	return valuation.NewRunner(store, sources, buybacks, pricer, cfg, logger), store, nil
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
