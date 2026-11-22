package valuation

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Automaat/ohara/backend-go/internal/deals"
	"github.com/Automaat/ohara/backend-go/internal/metrics"
	"github.com/Automaat/ohara/backend-go/internal/recovery"
)

// maxRunErrors bounds the source errors a run keeps for the status view.
// A refusal, at most one per site, is kept beyond it.
const maxRunErrors = 20

// firstRunDelay keeps a restart loop from hammering the sources.
const firstRunDelay = 5 * time.Minute

// Config tunes the runner. Interval 0 disables the schedule; a run can
// still be started by hand. Delay separates two source requests, jittered.
// Window is how far back a price still counts.
type Config struct {
	Interval time.Duration
	Delay    time.Duration
	Window   time.Duration
}

// RunResult counts what one run did. Errors holds source failures, which
// do not stop the run.
type RunResult struct {
	Editions int      `json:"editions"`
	Queries  int      `json:"queries"`
	Listings int      `json:"listings"`
	Matched  int      `json:"matched"`
	New      int      `json:"new"`
	Quotes   int      `json:"quotes"`
	Releases int      `json:"releases"`
	Errors   []string `json:"errors"`
	Failure  string   `json:"failure,omitempty"`
}

// Status is the runner state the valuation view shows.
type Status struct {
	Running         bool       `json:"running"`
	Scheduled       bool       `json:"scheduled"`
	IntervalSeconds int64      `json:"interval_seconds"`
	LastStartedAt   *time.Time `json:"last_started_at"`
	LastFinishedAt  *time.Time `json:"last_finished_at"`
	LastResult      *RunResult `json:"last_result"`
}

// Runner prices the owned collection on a schedule or on demand, one run
// at a time.
type Runner struct {
	store    *Store
	sources  []deals.Source
	buybacks []BuyBack
	pricer   ReleasePricer
	logger   *slog.Logger
	cfg      Config
	sleep    func(ctx context.Context, d time.Duration) error

	running atomic.Bool
	wg      sync.WaitGroup
	base    context.Context

	mu     sync.Mutex
	status Status
}

// NewRunner wires the runner. sources are asked for used and new listings
// of every owned edition, buybacks for an offer on every owned book and
// pricer, when not nil, for the Discogs prices of every owned album edition
// that names its release.
func NewRunner(store *Store, sources []deals.Source, buybacks []BuyBack, pricer ReleasePricer, cfg Config, logger *slog.Logger) *Runner {
	if logger == nil {
		logger = slog.Default()
	}
	r := &Runner{
		store: store, sources: sources, buybacks: buybacks, pricer: pricer, logger: logger, cfg: cfg,
		sleep: sleepCtx, base: context.Background(),
	}
	r.status.Scheduled = cfg.Interval > 0
	r.status.IntervalSeconds = int64(cfg.Interval.Seconds())
	metrics.InitBackgroundTask(metrics.TaskValuation)
	return r
}

// Window is how far back a price still counts.
func (r *Runner) Window() time.Duration { return r.cfg.Window }

// DealMarkets is the market price a wish's market percent applies to: the
// used price GET /api/valuation/market gives every wished title and
// format, kept only where it rests on at least MinObservations prices.
func (r *Runner) DealMarkets(ctx context.Context) ([]deals.Market, error) {
	markets, err := r.store.Markets(ctx, r.cfg.Window, 0)
	if err != nil {
		return nil, err
	}
	out := make([]deals.Market, 0, len(markets))
	for _, m := range markets {
		if m.Count >= MinObservations {
			out = append(out, deals.Market{TitleID: m.TitleID, Format: m.Format, Cents: m.Cents})
		}
	}
	return out, nil
}

// Pricer prices the owned album releases; nil without one.
func (r *Runner) Pricer() ReleasePricer { return r.pricer }

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// jitter spreads d uniformly over d*(1-spread) to d*(1+spread).
func jitter(d time.Duration, spread float64) time.Duration {
	var b [8]byte
	_, _ = rand.Read(b[:])
	u := float64(binary.LittleEndian.Uint64(b[:])>>11) / (1 << 53)
	return time.Duration(float64(d) * (1 - spread + 2*spread*u))
}

// Start publishes the last stored value and runs the schedule until ctx
// ends; manual runs also stop with ctx. The first scheduled run comes one
// interval after the last stored one, and no sooner than five minutes
// after start, so a restart neither reruns a fresh valuation nor skips a
// due one.
func (r *Runner) Start(ctx context.Context) {
	r.base = ctx
	latest, ok, err := r.store.Latest(ctx)
	if err != nil {
		r.logger.Warn("valuation: read last value", "err", err)
	}
	if ok {
		publish(latest)
	}
	if r.cfg.Interval <= 0 {
		return
	}
	wait := firstRunDelay
	if ok {
		wait = max(time.Until(latest.ComputedAt.Add(r.cfg.Interval)), firstRunDelay)
	}
	r.wg.Go(func() {
		wait = jitter(wait, 0.1)
		for {
			if r.sleep(ctx, wait) != nil {
				return
			}
			if r.running.CompareAndSwap(false, true) {
				r.execute(ctx)
			}
			wait = jitter(r.cfg.Interval, 0.1)
		}
	})
}

// Wait blocks until the schedule and any manual run have stopped.
func (r *Runner) Wait() { r.wg.Wait() }

// Trigger starts a run in the background and reports false when one is
// already running.
func (r *Runner) Trigger() bool {
	if !r.running.CompareAndSwap(false, true) {
		return false
	}
	r.wg.Go(func() { r.execute(r.base) })
	return true
}

// Status returns a copy of the current state.
func (r *Runner) Status() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.status
	s.Running = r.running.Load()
	return s
}

// execute runs once and records the outcome; the caller holds running.
func (r *Runner) execute(ctx context.Context) {
	defer r.running.Store(false)
	started := time.Now()
	r.mu.Lock()
	r.status.LastStartedAt = &started
	r.mu.Unlock()

	res, err := recovery.Call(r.logger, "valuation panicked", func() (RunResult, error) { return r.Run(ctx) })
	if err != nil {
		res.Failure = err.Error()
		r.logger.Error("valuation failed", "err", err)
	} else {
		r.logger.Info("valuation finished", "editions", res.Editions, "queries", res.Queries,
			"listings", res.Listings, "matched", res.Matched, "new", res.New, "quotes", res.Quotes,
			"releases", res.Releases, "errors", len(res.Errors))
	}
	finished := time.Now()
	metrics.ObserveBackgroundRun(metrics.TaskValuation, finished.Sub(started), err)
	r.mu.Lock()
	r.status.LastFinishedAt = &finished
	r.status.LastResult = &res
	r.mu.Unlock()
}

// run is one valuation: its counters and pacing between requests.
type run struct {
	r        *Runner
	holdings []Holding
	rules    deals.Rules
	titles   deals.Titles
	// asOf is when the run read holdings and titles, for RecordAsOf, on
	// the database clock, which writes last_seen_at; it is also the cutoff
	// for gone listings.
	asOf  time.Time
	res   RunResult
	first bool
}

// Run asks every source about every owned edition, every buy-back shop
// about every owned book and Discogs about every owned album release,
// records the prices and stores today's value.
// Source failures are collected in the result; a database failure or
// cancellation stops the run.
func (r *Runner) Run(ctx context.Context) (RunResult, error) {
	asOf, err := r.store.Now(ctx)
	if err != nil {
		return RunResult{}, err
	}
	holdings, err := r.store.Holdings(ctx)
	if err != nil {
		return RunResult{}, err
	}
	rules, err := r.store.Rules(ctx)
	if err != nil {
		return RunResult{}, err
	}
	titles, err := r.store.Titles(ctx)
	if err != nil {
		return RunResult{}, err
	}
	for i := range holdings {
		holdings[i].Want.Prepare()
	}
	x := &run{r: r, holdings: holdings, rules: rules, titles: titles, asOf: asOf, first: true}
	x.res.Editions = len(holdings)
	x.res.Errors = []string{}
	queries := searchQueries(holdings)
	x.res.Queries = len(queries)
	for _, src := range r.sources {
		if err := x.search(ctx, src, queries); err != nil {
			return x.res, err
		}
	}
	for _, b := range r.buybacks {
		if err := x.quote(ctx, b); err != nil {
			return x.res, err
		}
	}
	if r.pricer != nil {
		if err := x.priceReleases(ctx, r.pricer); err != nil {
			return x.res, err
		}
	}
	owned, sum, err := r.store.Valued(ctx, r.cfg.Window)
	if err != nil {
		return x.res, err
	}
	point, err := r.store.SaveDay(ctx, &sum)
	if err != nil {
		return x.res, err
	}
	if err := r.store.SaveCopyDays(ctx, point.Day, owned); err != nil {
		return x.res, err
	}
	publish(point)
	return x.res, nil
}

// searchQueries are the distinct queries of every holding, in holding
// order.
func searchQueries(holdings []Holding) []string {
	var out []string
	seen := map[string]bool{}
	for i := range holdings {
		for _, q := range holdings[i].Want.Queries() {
			key := strings.Join(strings.Fields(strings.ToLower(q)), " ")
			if !seen[key] {
				seen[key] = true
				out = append(out, q)
			}
		}
	}
	return out
}

func (x *run) pause(ctx context.Context) error {
	if x.first {
		x.first = false
		return ctx.Err()
	}
	return x.r.sleep(ctx, jitter(x.r.cfg.Delay, 0.5))
}

func (x *run) fail(source string, err error) {
	x.r.logger.Warn("valuation source failed", "source", source, "err", err)
	if len(x.res.Errors) < maxRunErrors {
		x.res.Errors = append(x.res.Errors, err.Error())
	}
}

// report keeps a site's refusal, if any, in the run's errors.
func (x *run) report(site string, f *deals.Refused, unit string) {
	if msg := f.Message(site, unit); msg != "" {
		x.res.Errors = append(x.res.Errors, msg)
	}
}

// search asks src every query and records the listings that match a
// holding. For a deals.Pager it then updates which stored listings are
// gone (gone).
func (x *run) search(ctx context.Context, src deals.Source, queries []string) error {
	if !src.PerQuery() {
		queries = []string{""}
	}
	var f deals.Refused
	defer x.report(src.Name(), &f, "queries")
	pager, paged := src.(deals.Pager)
	var complete, seen []string
	for _, q := range queries {
		if f.Skip() {
			continue
		}
		if err := x.pause(ctx); err != nil {
			return err
		}
		var page deals.Page
		var err error
		if paged {
			page, err = pager.SearchPage(ctx, q)
		} else {
			page.Listings, err = src.Search(ctx, q)
		}
		if err != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		deals.ObserveSearch(src, err)
		if err != nil {
			if f.Note(err) {
				x.r.logger.Warn("valuation source refused", "source", src.Name(), "err", err)
				continue
			}
			x.fail(src.Name(), err)
			continue
		}
		listings := page.Listings
		if paged && page.Complete {
			complete = append(complete, deals.QueryKey(q))
		}
		x.res.Listings += len(listings)
		seen = append(seen, page.Seen...)
		for i := range listings {
			l := &listings[i]
			h := match(x.holdings, l, x.rules, x.titles)
			if h == nil {
				continue
			}
			x.res.Matched++
			isNew, err := x.r.store.RecordAsOf(ctx, l, h, x.asOf)
			if err != nil {
				return err
			}
			if isNew {
				x.res.New++
			}
		}
	}
	if !paged {
		return nil
	}
	return x.gone(ctx, src.Name(), complete, seen)
}

// gone clears the gone mark of every stored listing of source an answer
// of this run held, matching or not, and marks gone those last found by
// one of the complete queries that no answer held. Only an answer that is
// every offer the site has for its query tells a listing is gone: one
// missing from a first page, or from a query that failed or was skipped,
// may still be offered.
func (x *run) gone(ctx context.Context, source string, complete, seen []string) error {
	n, err := x.r.store.UpdateGone(ctx, source, complete, seen, x.asOf)
	if err != nil {
		return err
	}
	if n > 0 {
		x.r.logger.Info("valuation found listings gone", "source", source, "listings", n)
	}
	return nil
}

// match returns the first holding a listing is a copy of, or nil. A
// listing that names another known title more specifically than a
// holding's (deals.Titles.Outranks) is not a copy of that holding.
func match(holdings []Holding, l *deals.Listing, rules deals.Rules, titles deals.Titles) *Holding {
	text := deals.NewListingText(l.Title, rules)
	for i := range holdings {
		if text.Belongs(&holdings[i].Want, titles) {
			return &holdings[i]
		}
	}
	return nil
}

// isISBN reports a 13-digit barcode in the Bookland ranges.
func isISBN(barcode *string) bool {
	if barcode == nil || len(*barcode) != 13 {
		return false
	}
	return strings.HasPrefix(*barcode, "978") || strings.HasPrefix(*barcode, "979")
}

type quoteRaw struct {
	Offered bool `json:"offered"`
}

func (x *run) quote(ctx context.Context, b BuyBack) error {
	var f deals.Refused
	defer x.report(b.Name(), &f, "quotes")
	for i := range x.holdings {
		h := &x.holdings[i]
		if h.Want.Kind != "book" || !isISBN(h.Barcode) {
			continue
		}
		if f.Skip() {
			continue
		}
		if err := x.pause(ctx); err != nil {
			return err
		}
		isbn := *h.Barcode
		q, err := b.Quote(ctx, isbn)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if f.Note(err) {
				x.r.logger.Warn("valuation source refused", "source", b.Name(), "err", err)
				continue
			}
			x.fail(b.Name(), err)
			continue
		}
		raw, err := json.Marshal(quoteRaw{Offered: q.Offered})
		if err != nil {
			return err
		}
		l := deals.Listing{
			Source: b.Name(), ExternalID: isbn, Title: q.Title, URL: q.URL, PriceCents: q.Cents, Raw: raw,
		}
		if _, err := x.r.store.RecordAsOf(ctx, &l, h, x.asOf); err != nil {
			return err
		}
		x.res.Quotes++
	}
	return nil
}

// priceReleases records the Discogs prices of every owned album edition
// that names its release. A release priced only in part keeps the prices
// it got; a price it lacks ages out of the window.
func (x *run) priceReleases(ctx context.Context, p ReleasePricer) error {
	for i := range x.holdings {
		h := &x.holdings[i]
		if h.Want.Kind != "music" || h.DiscogsReleaseID == nil {
			continue
		}
		if err := x.pause(ctx); err != nil {
			return err
		}
		id := *h.DiscogsReleaseID
		price, err := p.Price(ctx, id)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			x.fail(p.Name(), err)
		}
		recorded, err := x.recordRelease(ctx, h, id, price)
		if err != nil {
			return err
		}
		if recorded {
			x.res.Releases++
		}
	}
	return nil
}

type releaseRaw struct {
	ReleaseID  int64 `json:"release_id"`
	NumForSale int   `json:"num_for_sale"`
}

// recordRelease stores each known price of release id as an observation
// of h's edition, keyed by release and edition so two editions naming the
// same release both get it. They are stored in one transaction, so the
// suggestions of one answer share their last seen time and a grade the
// answer left out is older than the rest.
func (x *run) recordRelease(ctx context.Context, h *Holding, id int64, p ReleasePrice) (bool, error) {
	raw, err := json.Marshal(releaseRaw{ReleaseID: id, NumForSale: p.NumForSale})
	if err != nil {
		return false, err
	}
	key := strconv.FormatInt(id, 10) + "/" + strconv.FormatInt(h.Want.EditionID, 10)
	release := "https://www.discogs.com/release/" + strconv.FormatInt(id, 10)
	type price struct {
		source string
		cents  *int64
		url    string
	}
	prices := []price{
		{SourceDiscogsLowest, p.LowestCents, "https://www.discogs.com/sell/release/" + strconv.FormatInt(id, 10)},
	}
	for _, s := range p.suggested() {
		prices = append(prices, price{s.source, *s.cents, release})
	}
	var ls []deals.Listing
	for _, pr := range prices {
		if pr.cents == nil {
			continue
		}
		ls = append(ls, deals.Listing{
			Source: pr.source, ExternalID: key, Title: h.Want.Name, URL: pr.url, PriceCents: *pr.cents, Raw: raw,
		})
	}
	if len(ls) == 0 {
		return false, nil
	}
	if err := x.r.store.RecordAllAsOf(ctx, ls, h, x.asOf); err != nil {
		return false, err
	}
	return true, nil
}

// publish exports a stored day as the collection value gauges.
func publish(p HistoryPoint) {
	metrics.SetCollectionValue(metrics.CollectionValue{
		Copies:        p.Copies,
		SpentCents:    p.SpentCents,
		FloorCents:    p.Floor.Cents,
		FloorCopies:   p.Floor.Copies,
		MidCents:      p.Mid.Cents,
		MidCopies:     p.Mid.Copies,
		CeilingCents:  p.Ceiling.Cents,
		CeilingCopies: p.Ceiling.Copies,
		LowConfidence: p.LowConfidence,
		ComputedAt:    p.ComputedAt,
	})
}
