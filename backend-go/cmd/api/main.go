// Ohara API: serves the collection API, the built frontend, /health and
// /metrics, and applies schema migrations on startup.
package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Automaat/ohara/backend-go/internal/collection"
	"github.com/Automaat/ohara/backend-go/internal/db"
	"github.com/Automaat/ohara/backend-go/internal/deals"
	"github.com/Automaat/ohara/backend-go/internal/discogs"
	"github.com/Automaat/ohara/backend-go/internal/discogscollection"
	"github.com/Automaat/ohara/backend-go/internal/export"
	"github.com/Automaat/ohara/backend-go/internal/lookup"
	"github.com/Automaat/ohara/backend-go/internal/lookup/books"
	"github.com/Automaat/ohara/backend-go/internal/lookup/movie"
	"github.com/Automaat/ohara/backend-go/internal/lookup/music"
	"github.com/Automaat/ohara/backend-go/internal/metrics"
	"github.com/Automaat/ohara/backend-go/internal/reqlog"
	"github.com/Automaat/ohara/backend-go/internal/scanqueue"
	"github.com/Automaat/ohara/backend-go/internal/seed"
	"github.com/Automaat/ohara/backend-go/internal/server"
	"github.com/Automaat/ohara/backend-go/internal/valuation"
	"github.com/Automaat/ohara/backend-go/internal/wantlist"
	"github.com/Automaat/ohara/backend-go/internal/web"
	"github.com/Automaat/ohara/backend-go/internal/wishshare"
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
	if len(os.Args) >= 2 && os.Args[1] == "seed" {
		os.Exit(seedDemo())
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
	logger := slog.New(reqlog.NewHandler(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
	slog.SetDefault(logger)
	requireRemoteUser := envOr("OHARA_REQUIRE_REMOTE_USER", "false") == "true"
	logger.Info("Remote-User check", "enabled", requireRemoteUser)

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
	lookupService, err := newLookup(pool, logger)
	if err != nil {
		logger.Error("lookup providers", "err", err)
		return 2
	}
	hunter, dealStore, valuer, valueStore, err := newRuns(pool, logger)
	if err != nil {
		logger.Error("deal hunter and valuation", "err", err)
		return 2
	}
	huntCtx, stopHunt := context.WithCancel(ctx)
	hunter.Start(huntCtx)
	valuer.Start(huntCtx)
	defer valuer.Wait()
	defer hunter.Wait()
	defer stopHunt()
	syncer := newWantlistSyncer(pool, logger)
	syncer.Start(huntCtx)
	defer syncer.Wait()
	discogsShelf, err := newDiscogsCollection(pool, logger)
	if err != nil {
		logger.Error("discogs collection", "err", err)
		return 2
	}
	queue := scanqueue.NewStore(pool)
	metrics.RegisterScanQueueCollector(queue.Count)
	searchers := titleSearchers()
	shares := wishshare.NewHandler(wishshare.NewStore(pool), logger)
	deps.Share = shares.Public()
	deps.API = []server.Mounter{
		newCollectionHandler(pool, searchers, lookupService, logger),
		lookup.NewHandler(lookupService, logger),
		&lookup.SearchHandler{Searchers: searchers, Logger: logger},
		scanqueue.NewHandler(queue, logger),
		deals.NewHandler(dealStore, hunter, logger),
		valuation.NewHandler(valueStore, valuer, logger),
		wantlist.NewHandler(syncer, logger),
		export.NewHandler(pool, valuer.Window(), logger),
		shares,
		discogsShelf,
	}
	if dir := os.Getenv("OHARA_WEB_DIR"); dir != "" {
		if deps.Web, err = frontend(dir); err != nil {
			logger.Error("frontend", "dir", dir, "err", err)
			return 2
		}
	}

	metrics.SetBuildInfo(version)
	addr := envOr("OHARA_ADDR", ":8000")
	srvCfg := server.Config{
		Version:           version,
		AccessLog:         envOr("OHARA_ACCESS_LOG", "false") == "true",
		RequireRemoteUser: requireRemoteUser,
	}
	srv := newHTTPServer(addr, server.New(srvCfg, logger, deps))

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
// newHTTPServer serves h on addr. It leaves "OPTIONS *" to h, so its answer
// carries the router's security headers like every other response.
func newHTTPServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:                         addr,
		Handler:                      h,
		DisableGeneralOptionsHandler: true,
		ReadHeaderTimeout:            10 * time.Second,
		ReadTimeout:                  30 * time.Second,
		WriteTimeout:                 60 * time.Second,
		IdleTimeout:                  120 * time.Second,
	}
}

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
	res, err := db.ApplyMigrations(ctx, pool, migrations)
	if err != nil {
		pool.Close()
		return nil, err
	}
	if len(res.Applied) > 0 {
		logger.Info("applied migrations", "versions", strings.Join(res.Applied, ","))
	}
	db.WarnUnknown(logger, migrations, res.Unknown)
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
func newLookup(pool *pgxpool.Pool, logger *slog.Logger) (*lookup.Service, error) {
	registry := &lookup.Registry{}
	client := &http.Client{}
	providers := []lookup.Provider{
		books.BN{Client: client, BaseURL: envOr("OHARA_BN_URL", books.BNBaseURL)},
		books.OpenLibrary{Client: client, BaseURL: openLibraryURL()},
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
	// One Allegro client serves both chains: it holds the token lock.
	allegro := newAllegro(pool)
	if p := newMovieProvider(allegro, logger); p != nil {
		if err := registry.Register(p, movieCacheTTL); err != nil {
			return nil, err
		}
	}
	if p := newMusicProvider(allegro, logger); p != nil {
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
	return lookup.NewService(registry, lookup.NewPGCache(pool), logger), nil
}

// openLibraryURL is where the barcode lookup and the title search ask
// Open Library.
func openLibraryURL() string { return envOr("OHARA_OPENLIBRARY_URL", books.OpenLibraryBaseURL) }

// sourceClient is shared by the external sources; the lookup service bounds
// each provider call too.
var sourceClient = &http.Client{Timeout: 8 * time.Second}

// newMovieProvider builds the movie chain, or nil without a TMDB key: the
// chain ends in a TMDB search. Allegro joins when its app credentials are
// set; UPCitemdb, which can be switched off, is a fallback asked only when
// Allegro names nothing, since its free quota is small.
func newMovieProvider(allegro *movie.Allegro, logger *slog.Logger) *movie.Provider {
	key := os.Getenv("OHARA_TMDB_API_KEY")
	if key == "" {
		logger.Info("movie lookup disabled: OHARA_TMDB_API_KEY not set")
		return nil
	}
	p := &movie.Provider{TMDB: movie.TMDB{Client: sourceClient, Key: key}, Logger: logger}
	if allegro != nil {
		p.Sources = append(p.Sources, allegro)
	}
	if envOr("OHARA_UPCITEMDB", "true") == "true" {
		p.Fallbacks = append(p.Fallbacks, movie.UPCitemdb{Client: sourceClient})
	}
	names := make([]string, 0, len(p.Sources)+len(p.Fallbacks))
	for _, s := range slices.Concat(p.Sources, p.Fallbacks) {
		names = append(names, s.Name())
		metrics.InitSource(s.Name())
	}
	metrics.InitSource("tmdb")
	logger.Info("movie lookup sources", "sources", strings.Join(names, ","))
	return p
}

// newCollectionHandler serves the collection API, filling a new title's
// cover from the title searchers and, with a TMDB key, a new film's series
// from its TMDB collection.
func newCollectionHandler(pool *pgxpool.Pool, searchers map[string]lookup.TextSearcher,
	service *lookup.Service, logger *slog.Logger,
) *collection.Handler {
	h := collection.NewHandler(collection.NewStore(pool), logger).WithCatalogCovers(searchers, service)
	if key := os.Getenv("OHARA_TMDB_API_KEY"); key != "" {
		h.WithFilmSeries(movie.TMDB{Client: sourceClient, Key: key})
	}
	return h
}

// titleSearchers back the search by title for a barcode no source knows:
// TMDB for films and TV series and Discogs for albums when their keys are
// set, Google Books for books with a key and Open Library without one.
func titleSearchers() map[string]lookup.TextSearcher {
	s := map[string]lookup.TextSearcher{"book": books.OpenLibrary{Client: sourceClient, BaseURL: openLibraryURL()}}
	if key := os.Getenv("OHARA_GOOGLE_BOOKS_API_KEY"); key != "" {
		s["book"] = books.GoogleBooks{APIKey: key, Client: sourceClient}
	}
	if key := os.Getenv("OHARA_TMDB_API_KEY"); key != "" {
		s["movie"] = movie.TextSearch{TMDB: movie.TMDB{Client: sourceClient, Key: key}}
		s["tv"] = movie.TVSearch{TMDB: movie.TMDB{Client: sourceClient, Key: key}}
	}
	if d := newDiscogs(); d != nil {
		s["music"] = music.TextSearch{Discogs: d}
	}
	return s
}

// discogsBudget is the rate budget of OHARA_DISCOGS_TOKEN, which every
// Discogs client of the process sends: the music lookup, the title search
// and the valuation draw on it together.
var discogsBudget = discogs.NewLimiter()

// discogsHTTP sends the Discogs requests, one connection each, so that no
// resend by net/http escapes discogsBudget.
var discogsHTTP = discogs.NewHTTPClient()

// newDiscogs is the Discogs client, or nil without OHARA_DISCOGS_TOKEN:
// every Discogs endpoint needs a token.
func newDiscogs() *discogs.Client {
	token := os.Getenv("OHARA_DISCOGS_TOKEN")
	if token == "" {
		return nil
	}
	return &discogs.Client{
		HTTP: discogsHTTP, BaseURL: envOr("OHARA_DISCOGS_URL", discogs.BaseURL), Token: token, Limiter: discogsBudget,
	}
}

// newWantlistSyncer mirrors the album wishes to the Discogs wantlist of
// the OHARA_DISCOGS_TOKEN owner, behind the lookup's requests in the
// token's budget; disabled without the token.
func newWantlistSyncer(pool *pgxpool.Pool, logger *slog.Logger) *wantlist.Syncer {
	store := wantlist.NewStore(pool)
	d := newDiscogs()
	if d == nil {
		logger.Info("discogs wantlist sync disabled: OHARA_DISCOGS_TOKEN not set")
		return wantlist.NewSyncer(store, nil, logger)
	}
	d.Priority = discogs.Background
	return wantlist.NewSyncer(store, d, logger)
}

// frontend is the built SPA in dir, checked to be one.
func frontend(dir string) (fs.FS, error) {
	webFS := os.DirFS(dir)
	if err := web.Check(webFS); err != nil {
		return nil, err
	}
	return webFS, nil
}

// newDiscogsCollection adds owned album copies to the Discogs collection
// of the OHARA_DISCOGS_TOKEN owner, in folder OHARA_DISCOGS_FOLDER_ID
// (Uncategorized unless set); without the token its push answers 503.
func newDiscogsCollection(pool *pgxpool.Pool, logger *slog.Logger) (*discogscollection.Handler, error) {
	folder := int64(discogscollection.DefaultFolder)
	if v := os.Getenv("OHARA_DISCOGS_FOLDER_ID"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 {
			return nil, fmt.Errorf("OHARA_DISCOGS_FOLDER_ID: want a positive folder ID, got %q", v)
		}
		folder = n
	}
	d := newDiscogs()
	if d == nil {
		logger.Info("discogs collection disabled: OHARA_DISCOGS_TOKEN not set")
	}
	store := discogscollection.NewStore(pool)
	return discogscollection.NewHandler(discogscollection.NewPusher(store, d, folder), logger), nil
}

// newMusicProvider builds the music chain: Discogs when a token is set,
// then MusicBrainz with Cover Art Archive covers unless switched off, then
// a Discogs search for Allegro's product name; nil when neither Discogs
// nor MusicBrainz is on.
func newMusicProvider(allegro *movie.Allegro, logger *slog.Logger) *music.Provider {
	p := &music.Provider{}
	var names []string
	if d := newDiscogs(); d != nil {
		p.Discogs = d
		names = append(names, discogs.Source)
		if allegro != nil {
			p.Names = allegro
			names = append(names, allegro.Name())
		}
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
	return &movie.Allegro{
		ClientID: id, ClientSecret: secret, Store: movie.PGTokenStore{Pool: pool}, Client: sourceClient,
		UserAgent: os.Getenv("OHARA_ALLEGRO_USER_AGENT"),
	}
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

// seedDemo fills an empty, migrated database with demo data for local
// work. It exits 1 without a change when the database holds data or lacks
// a migration; it never migrates, so start the app once first.
func seedDemo() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" && os.Getenv("PGHOST") == "" {
		fmt.Fprintln(os.Stderr, "seed: set DATABASE_URL or the PG* variables")
		return 2
	}
	n, err := seedDB(ctx, dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, "seed:", err)
		return 1
	}
	fmt.Printf("seed: added %d titles, %d editions, %d copies, %d listings, %d deal alerts, "+
		"%d days of value history and %d scan queue entries\n",
		n.Titles, n.Editions, n.Copies, n.Observations, n.Alerts, n.HistoryDays, n.ScanQueue)
	return 0
}

func seedDB(ctx context.Context, dsn string) (seed.Counts, error) {
	pool, err := db.New(ctx, dsn)
	if err != nil {
		return seed.Counts{}, fmt.Errorf("database: %w", err)
	}
	defer pool.Close()
	migrations, err := db.EmbeddedMigrations()
	if err != nil {
		return seed.Counts{}, err
	}
	versions := make([]string, len(migrations))
	for i, m := range migrations {
		versions[i] = m.Version
	}
	return seed.Run(ctx, pool, versions)
}

// newRuns configures the deal hunter and the valuation. One pacer spaces
// the requests they send to a site they share while their runs overlap:
// at least half the source delay apart, the shortest pause one run makes.
// The deals read the valuation's market prices for a wish's market percent.
func newRuns(pool *pgxpool.Pool, logger *slog.Logger) (*deals.Hunter, *deals.Store, *valuation.Runner, *valuation.Store, error) {
	delay, err := sourceDelay()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	pacer := deals.NewPacer(delay / 2)
	hunter, dealStore, err := newHunter(pool, pacer.Lane(), logger)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("deal hunter: %w", err)
	}
	valuer, valueStore, err := newValuation(pool, pacer.Lane(), logger)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("valuation: %w", err)
	}
	dealStore.UseMarkets(valuer.DealMarkets)
	return hunter, dealStore, valuer, valueStore, nil
}

// newHunter configures the deal hunter from the environment. Polling stays
// off until OHARA_DEALS_INTERVAL is set; alerts, and the poller for their
// "Ignoruj" button, need both Telegram variables. The *_URL variables
// point the sources at test doubles; OHARA_BLUEDVD=false leaves the BlueDVD
// shop out and OHARA_SWIATKSIAZKI=false the Świat Książki shop. Tezeusz, a used-book shop the valuation does not ask, is
// searched unless OHARA_TEZEUSZ is false. lane spaces its requests from the
// valuation's.
func newHunter(pool *pgxpool.Pool, lane *deals.Lane, logger *slog.Logger) (*deals.Hunter, *deals.Store, error) {
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
	client := pacedClient(lane)
	sources := usedSources(client)
	if envOr("OHARA_BLUEDVD", "true") == "true" {
		sources = append(sources, deals.BlueDVD{Client: client, BaseURL: envOr("OHARA_DEALS_BLUEDVD_URL", deals.BlueDVDURL)})
	}
	if envOr("OHARA_SWIATKSIAZKI", "true") == "true" {
		sources = append(sources, deals.SwiatKsiazki{
			Client: client, BaseURL: envOr("OHARA_DEALS_SWIATKSIAZKI_URL", deals.SwiatKsiazkiURL),
		})
	}
	sources = append(sources, deals.Pepper{Client: client, BaseURL: envOr("OHARA_DEALS_PEPPER_URL", deals.PepperURL)})
	if envOr("OHARA_TEZEUSZ", "true") == "true" {
		sources = append(sources, deals.Tezeusz{Client: client, BaseURL: envOr("OHARA_DEALS_TEZEUSZ_URL", deals.TezeuszURL)})
	}
	sellers, err := discogsSellers(client)
	if err != nil {
		return nil, nil, err
	}
	sources = append(sources, sellers...)
	store := deals.NewStore(pool)
	var notifier deals.Notifier
	var poller *deals.TelegramPoller
	token, chat := os.Getenv("OHARA_TELEGRAM_BOT_TOKEN"), os.Getenv("OHARA_TELEGRAM_CHAT_ID")
	switch {
	case token != "" && chat != "":
		baseURL := envOr("OHARA_TELEGRAM_URL", deals.TelegramURL)
		notifier = &deals.Telegram{Client: client, BaseURL: baseURL, Token: token, ChatID: chat}
		poller = &deals.TelegramPoller{
			Bot:   &deals.Telegram{Client: deals.NewTelegramPollClient(), BaseURL: baseURL, Token: token, ChatID: chat},
			Store: store, Logger: logger,
		}
	case token != "" || chat != "":
		return nil, nil, errors.New("set both OHARA_TELEGRAM_BOT_TOKEN and OHARA_TELEGRAM_CHAT_ID, or neither")
	}
	logger.Info("deal hunter", "interval", interval, "delay", delay, "telegram", notifier != nil,
		"discogs_sellers", len(sellers))
	cfg := deals.Config{Interval: interval, Delay: delay, CanaryQuery: envOr("OHARA_DEALS_CANARY_QUERY", "dvd")}
	hunter := deals.NewHunter(store, sources, notifier, cfg, logger)
	if poller != nil {
		hunter.PollTelegram(poller)
	}
	return hunter, store, nil
}

// discogsSeller is a Discogs username.
var discogsSeller = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// discogsSellers are the deal sources of the Discogs sellers in
// OHARA_DISCOGS_SELLERS, a comma-separated list of usernames, each once:
// none without the list or without OHARA_DISCOGS_TOKEN. Their prices turn
// into złoty at the NBP rates of OHARA_VALUATION_NBP_URL, fetched through
// client, and their requests yield the token's budget to scans and title
// searches.
func discogsSellers(client *http.Client) ([]deals.Source, error) {
	var names []string
	for name := range strings.SplitSeq(os.Getenv("OHARA_DISCOGS_SELLERS"), ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if !discogsSeller.MatchString(name) {
			return nil, fmt.Errorf("OHARA_DISCOGS_SELLERS: %q is not a Discogs username", name)
		}
		if !slices.ContainsFunc(names, func(n string) bool { return strings.EqualFold(n, name) }) {
			names = append(names, name)
		}
	}
	d := newDiscogs()
	if d == nil || len(names) == 0 {
		return nil, nil
	}
	d.Priority = discogs.Background
	metrics.InitSource(valuation.SourceNBP)
	rates := &valuation.NBP{Client: client, BaseURL: envOr("OHARA_VALUATION_NBP_URL", valuation.NBPURL)}
	sources := make([]deals.Source, 0, len(names))
	for _, name := range names {
		sources = append(sources, deals.DiscogsSeller{Client: d, Rates: rates, Seller: name})
	}
	return sources, nil
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

// pacedClient is the HTTP client of one runner's sources: every request
// waits its turn in lane.
func pacedClient(lane *deals.Lane) *http.Client {
	client := deals.NewHTTPClient()
	client.Transport = lane.Transport(nil)
	return client
}

// usedSources are the marketplaces of used copies: OLX.
func usedSources(client *http.Client) []deals.Source {
	return []deals.Source{deals.OLX{Client: client, BaseURL: envOr("OHARA_DEALS_OLX_URL", deals.OLXURL)}}
}

// newValuation configures the valuation runner from the environment. The
// daily schedule stays off until OHARA_VALUATION_INTERVAL is set; it asks
// the deal hunter's OLX, with the same pause, SkupSzop and, with
// a Discogs token, the Discogs marketplace. lane spaces its requests from
// the hunter's.
func newValuation(pool *pgxpool.Pool, lane *deals.Lane, logger *slog.Logger) (*valuation.Runner, *valuation.Store, error) {
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
	retention, err := retentionDays(days)
	if err != nil {
		return nil, nil, err
	}
	delay, err := sourceDelay()
	if err != nil {
		return nil, nil, err
	}
	client := pacedClient(lane)
	sources := usedSources(client)
	buybacks := []valuation.BuyBack{
		valuation.SkupSzop{Client: client, BaseURL: envOr("OHARA_VALUATION_SKUPSZOP_URL", valuation.SkupSzopURL)},
	}
	for _, b := range buybacks {
		metrics.InitSource(b.Name())
	}
	window := time.Duration(days) * 24 * time.Hour
	pricer := newReleasePricer(client)
	logger.Info("valuation", "interval", interval, "window_days", days, "retention_days", retention,
		"discogs", pricer != nil)
	store := valuation.NewStore(pool)
	cfg := valuation.Config{
		Interval: interval, Delay: delay, Window: window,
		Retention: time.Duration(retention) * 24 * time.Hour,
	}
	return valuation.NewRunner(store, sources, buybacks, pricer, cfg, logger), store, nil
}

// maxRetentionDays bounds OHARA_PRICE_RETENTION_DAYS at about a century.
const maxRetentionDays = 36500

// retentionDays reads OHARA_PRICE_RETENTION_DAYS: 0, keeping every stored
// listing, when unset, else whole days from windowDays, the valuation
// window, so pruning never drops a price the valuation still reads.
func retentionDays(windowDays int) (int, error) {
	v := os.Getenv("OHARA_PRICE_RETENTION_DAYS")
	if v == "" {
		return 0, nil
	}
	days, err := strconv.Atoi(v)
	if err != nil || days < 1 || days > maxRetentionDays {
		return 0, fmt.Errorf("OHARA_PRICE_RETENTION_DAYS: want a number of days from OHARA_VALUATION_WINDOW_DAYS (%d) to %d, got %q",
			windowDays, maxRetentionDays, v)
	}
	if days < windowDays {
		return 0, fmt.Errorf("OHARA_PRICE_RETENTION_DAYS (%d) is below OHARA_VALUATION_WINDOW_DAYS (%d): pruning would delete prices the valuation still reads",
			days, windowDays)
	}
	return days, nil
}

// newReleasePricer prices album releases on the Discogs marketplace, or is
// nil without OHARA_DISCOGS_TOKEN. Its requests yield the shared budget to
// scans and title searches.
func newReleasePricer(client *http.Client) valuation.ReleasePricer {
	d := newDiscogs()
	if d == nil {
		return nil
	}
	d.Priority = discogs.Background
	metrics.InitSource(valuation.SourceNBP)
	return valuation.Discogs{
		Market:      d,
		Rates:       &valuation.NBP{Client: client, BaseURL: envOr("OHARA_VALUATION_NBP_URL", valuation.NBPURL)},
		Suggestions: envOr("OHARA_DISCOGS_PRICE_SUGGESTIONS", "true") == "true",
	}
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
