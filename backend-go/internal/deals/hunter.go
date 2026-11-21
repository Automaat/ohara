package deals

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Automaat/ohara/backend-go/internal/metrics"
	"github.com/Automaat/ohara/backend-go/internal/recovery"
)

// maxRunErrors bounds the source errors a run keeps for the status view.
const maxRunErrors = 20

// telegramPause keeps alerts under Telegram's one message per second per
// chat.
const telegramPause = time.Second

// markSentTimeout bounds recording a delivered alert after the run's
// context is gone.
const markSentTimeout = 10 * time.Second

// saveRunTimeout bounds storing a run's start or end, which outlives the
// run's context so a run cut short by shutdown still counts.
const saveRunTimeout = 10 * time.Second

// firstRunDelay is the least a scheduled run waits after a start, so a
// restart loop does not hammer the sources.
const firstRunDelay = 5 * time.Minute

// Config tunes the hunter. Interval 0 disables the schedule; a run can still
// be started by hand. Delay separates two source requests; both are
// jittered. CanaryQuery, when set, is asked of every query source on every
// run, wishlist or not: a query that always has listings, so a source that
// answers with none is broken. A feed source's one request is its canary.
type Config struct {
	Interval    time.Duration
	Delay       time.Duration
	CanaryQuery string
}

// RunResult counts what one run did. Errors holds source failures, which do
// not stop the run.
type RunResult struct {
	Wants    int      `json:"wants"`
	Queries  int      `json:"queries"`
	Listings int      `json:"listings"`
	Matched  int      `json:"matched"`
	New      int      `json:"new"`
	Alerts   int      `json:"alerts"`
	Sent     int      `json:"sent"`
	Errors   []string `json:"errors"`
	Failure  string   `json:"failure,omitempty"`
}

// Status is the hunter state the deals view shows.
type Status struct {
	Running         bool       `json:"running"`
	Scheduled       bool       `json:"scheduled"`
	IntervalSeconds int64      `json:"interval_seconds"`
	Telegram        bool       `json:"telegram"`
	LastStartedAt   *time.Time `json:"last_started_at"`
	LastFinishedAt  *time.Time `json:"last_finished_at"`
	LastResult      *RunResult `json:"last_result"`
}

// Hunter runs deal searches on a schedule or on demand, one at a time.
type Hunter struct {
	store    *Store
	sources  []Source
	notifier Notifier
	logger   *slog.Logger
	cfg      Config
	sleep    func(ctx context.Context, d time.Duration) error

	running atomic.Bool
	wg      sync.WaitGroup
	base    context.Context

	mu     sync.Mutex
	status Status
	// lastSend is when the notifier last returned, kept across runs so
	// messages stay telegramPause apart; guarded by mu.
	lastSend time.Time
}

// NewHunter wires the hunter. A nil notifier records listings without
// alerting.
func NewHunter(store *Store, sources []Source, notifier Notifier, cfg Config, logger *slog.Logger) *Hunter {
	if logger == nil {
		logger = slog.Default()
	}
	h := &Hunter{
		store: store, sources: sources, notifier: notifier, logger: logger, cfg: cfg,
		sleep: sleepCtx, base: context.Background(),
	}
	h.status.Scheduled = cfg.Interval > 0
	h.status.IntervalSeconds = int64(cfg.Interval.Seconds())
	h.status.Telegram = notifier != nil
	for _, src := range sources {
		metrics.InitSource(src.Name())
	}
	return h
}

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

// Start restores the last stored run, then runs the schedule until ctx
// ends; manual runs also stop with ctx. The first scheduled run comes one
// interval after the last run started, or about five minutes after start
// without one, and never sooner than firstRunDelay.
func (h *Hunter) Start(ctx context.Context) {
	h.base = ctx
	last, ok := h.restore(ctx)
	if h.cfg.Interval <= 0 {
		return
	}
	due, spread := firstRunDelay, 0.2
	if ok {
		due, spread = time.Until(last.Add(h.cfg.Interval)), 0.1
	}
	h.wg.Go(func() {
		wait := max(jitter(due, spread), firstRunDelay)
		for {
			if h.sleep(ctx, wait) != nil {
				return
			}
			if h.running.CompareAndSwap(false, true) {
				h.execute(ctx)
			}
			wait = jitter(h.cfg.Interval, 0.1)
		}
	})
}

// restore puts the last stored run into the status and returns its start;
// false when there is none or it cannot be read.
func (h *Hunter) restore(ctx context.Context) (time.Time, bool) {
	last, ok, err := h.store.LastRun(ctx)
	if err != nil {
		h.logger.Warn("deal search: read last run", "err", err)
		return time.Time{}, false
	}
	if !ok {
		return time.Time{}, false
	}
	h.mu.Lock()
	if h.status.LastStartedAt == nil {
		h.status.LastStartedAt = &last.StartedAt
		h.status.LastFinishedAt = last.FinishedAt
		h.status.LastResult = last.Result
	}
	h.mu.Unlock()
	return last.StartedAt, true
}

// Wait blocks until the schedule and any manual run have stopped.
func (h *Hunter) Wait() { h.wg.Wait() }

// Trigger starts a run in the background and reports false when one is
// already running.
func (h *Hunter) Trigger() bool {
	if !h.running.CompareAndSwap(false, true) {
		return false
	}
	h.wg.Go(func() { h.execute(h.base) })
	return true
}

// Status returns a copy of the current state.
func (h *Hunter) Status() Status {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.status
	s.Running = h.running.Load()
	return s
}

// execute runs once and records the outcome, in the status and the
// database; the caller holds running. Times keep the database's
// microseconds, so a restart restores the same status.
func (h *Hunter) execute(ctx context.Context) {
	defer h.running.Store(false)
	started := time.Now().Truncate(time.Microsecond)
	h.mu.Lock()
	h.status.LastStartedAt = &started
	h.mu.Unlock()
	h.saveRun(ctx, func(ctx context.Context) error { return h.store.RunStarted(ctx, started) })

	res, err := recovery.Call(h.logger, "deal search panicked", func() (RunResult, error) { return h.Run(ctx) })
	if err != nil {
		res.Failure = err.Error()
		h.logger.Error("deal search failed", "err", err)
	} else {
		h.logger.Info("deal search finished", "wants", res.Wants, "queries", res.Queries,
			"listings", res.Listings, "matched", res.Matched, "new", res.New,
			"alerts", res.Alerts, "sent", res.Sent, "errors", len(res.Errors))
	}
	finished := time.Now().Truncate(time.Microsecond)
	h.mu.Lock()
	h.status.LastFinishedAt = &finished
	h.status.LastResult = &res
	h.mu.Unlock()
	h.saveRun(ctx, func(ctx context.Context) error { return h.store.RunFinished(ctx, started, finished, &res) })
}

// saveRun stores part of a run even after ctx is canceled, so a run cut
// short by shutdown still counts as the last one. A failure only loses
// the record.
func (h *Hunter) saveRun(ctx context.Context, save func(context.Context) error) {
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), saveRunTimeout)
	defer cancel()
	if err := save(saveCtx); err != nil {
		h.logger.Warn("deal search: save run", "err", err)
	}
}

// run is one search: its counters, pacing between requests and the alerts
// it has already tried to send.
type run struct {
	h     *Hunter
	wants []Want
	rules Rules
	res   RunResult
	first bool
	// tried holds the observations whose alert this run sent or failed to
	// send; a failed one waits for the next run.
	tried map[int64]bool
}

// Run searches every source for every wishlist copy, records matching
// listings and delivers pending alerts. An alert for a listing an answer
// matched goes out before the run asks a source again; every other pending
// alert, such as one for a listing no answer matched, goes out after the
// last request.
// Source failures are collected in the result; a database failure or
// cancellation stops the run.
func (h *Hunter) Run(ctx context.Context) (RunResult, error) {
	wants, err := h.store.Wants(ctx)
	if err != nil {
		return RunResult{}, err
	}
	rules, err := h.store.Rules(ctx)
	if err != nil {
		return RunResult{}, err
	}
	r := &run{h: h, wants: wants, rules: rules, first: true, tried: map[int64]bool{}}
	r.res.Wants = len(wants)
	r.res.Errors = []string{}
	var queries []string
	if len(wants) > 0 {
		queries = searchQueries(wants)
		r.res.Queries = len(queries)
	}
	if len(wants) > 0 || h.cfg.CanaryQuery != "" {
		for _, src := range h.sources {
			if err := r.search(ctx, src, queries); err != nil {
				return r.res, err
			}
		}
	}
	if err := r.deliver(ctx, nil); err != nil {
		return r.res, err
	}
	return r.res, nil
}

// searchQueries are the distinct queries of every want, in want order.
func searchQueries(wants []Want) []string {
	var out []string
	seen := map[string]bool{}
	for i := range wants {
		for _, q := range wants[i].Queries() {
			key := strings.Join(words(q), " ")
			if !seen[key] {
				seen[key] = true
				out = append(out, q)
			}
		}
	}
	return out
}

func (r *run) pause(ctx context.Context) error {
	if r.first {
		r.first = false
		return ctx.Err()
	}
	return r.h.sleep(ctx, jitter(r.h.cfg.Delay, 0.5))
}

// search asks src for the canary query and every wishlist query, then
// publishes the listing count and canary outcome. A feed source is asked
// once: that answer is both its canary and, with a wishlist, its listings.
func (r *run) search(ctx context.Context, src Source, queries []string) error {
	canary := r.h.cfg.CanaryQuery
	if !src.PerQuery() {
		listings, ok, err := r.ask(ctx, src, "")
		if err != nil {
			return err
		}
		if canary != "" {
			r.canary(src, ok, len(listings))
		}
		metrics.SetSourceResults(src.Name(), len(listings))
		if ok && len(r.wants) > 0 {
			return r.record(ctx, listings)
		}
		return nil
	}
	results := 0
	if canary != "" {
		listings, ok, err := r.ask(ctx, src, canary)
		if err != nil {
			return err
		}
		r.canary(src, ok, len(listings))
		results += len(listings)
	}
	for _, q := range queries {
		listings, ok, err := r.ask(ctx, src, q)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		results += len(listings)
		if err := r.record(ctx, listings); err != nil {
			return err
		}
	}
	metrics.SetSourceResults(src.Name(), results)
	return nil
}

// ask sends one request to src after the pause and counts its outcome. A
// source failure is logged and kept in the result with ok false; err is
// only the run's cancellation.
func (r *run) ask(ctx context.Context, src Source, query string) ([]Listing, bool, error) {
	if err := r.pause(ctx); err != nil {
		return nil, false, err
	}
	listings, err := src.Search(ctx, query)
	if err != nil && ctx.Err() != nil {
		return nil, false, ctx.Err()
	}
	ObserveSearch(src, err)
	if err != nil {
		r.h.logger.Warn("deal source failed", "source", src.Name(), "query", query, "err", err)
		r.fail(err.Error())
		return nil, false, nil
	}
	return listings, true, nil
}

// canary publishes the canary outcome of src; an answer without listings
// is a failure too, since the canary query always has some.
func (r *run) canary(src Source, ok bool, listings int) {
	metrics.SetSourceCanary(src.Name(), ok && listings > 0)
	if ok && listings == 0 {
		r.h.logger.Warn("deal source canary found no listings", "source", src.Name(), "query", r.h.cfg.CanaryQuery)
		r.fail(src.Name() + ": canary query found no listings")
	}
}

// fail keeps a source failure for the status view.
func (r *run) fail(msg string) {
	if len(r.res.Errors) < maxRunErrors {
		r.res.Errors = append(r.res.Errors, msg)
	}
}

// record stores the matching listings of one answer, then sends their
// pending alerts before the run asks a source again. PendingAlerts checks
// the refreshed price against the wish's current limit, which may have
// changed since the run read the wishlist.
func (r *run) record(ctx context.Context, listings []Listing) error {
	r.res.Listings += len(listings)
	refreshed := map[string]bool{}
	for i := range listings {
		l := &listings[i]
		w := pick(r.wants, l, r.rules)
		if w == nil {
			continue
		}
		r.res.Matched++
		alert := r.h.notifier != nil && w.MaxPriceCents != nil && l.PriceCents <= *w.MaxPriceCents
		isNew, alerted, err := r.h.store.Record(ctx, l, w, alert)
		if err != nil {
			return err
		}
		if isNew {
			r.res.New++
		}
		if alerted {
			r.res.Alerts++
		}
		refreshed[listingKey(l.Source, l.ExternalID)] = true
	}
	if len(refreshed) == 0 {
		return nil
	}
	return r.deliver(ctx, refreshed)
}

func listingKey(source, externalID string) string { return source + "\x00" + externalID }

// deliver sends the pending alerts this run has not tried yet, at least
// telegramPause apart; a failed send is retried next run. A non-nil only
// limits it to the listings it names.
func (r *run) deliver(ctx context.Context, only map[string]bool) error {
	if r.h.notifier == nil {
		return nil
	}
	alerts, err := r.h.store.PendingAlerts(ctx)
	if err != nil {
		return err
	}
	for i := range alerts {
		a := &alerts[i]
		if r.tried[a.ObservationID] || only != nil && !only[listingKey(a.Source, a.ExternalID)] {
			continue
		}
		if wait := r.h.sendWait(); wait > 0 {
			if err := r.h.sleep(ctx, wait); err != nil {
				return err
			}
		}
		r.tried[a.ObservationID] = true
		err := r.h.notifier.Send(ctx, alertText(a))
		r.h.sent()
		if err != nil {
			r.h.logger.Warn("telegram alert failed", "observation", a.ObservationID, "err", err)
			if err := r.h.store.MarkFailed(ctx, a.ObservationID, err.Error()); err != nil {
				return err
			}
			continue
		}
		// The message is out: record it even when ctx was canceled during
		// the send, or the next run sends it again.
		markCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), markSentTimeout)
		err = r.h.store.MarkSent(markCtx, a.ObservationID)
		cancel()
		if err != nil {
			return err
		}
		r.res.Sent++
	}
	return nil
}

// sendWait is what is left of telegramPause since the notifier last
// returned.
func (h *Hunter) sendWait() time.Duration {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.lastSend.IsZero() {
		return 0
	}
	return max(telegramPause-time.Since(h.lastSend), 0)
}

// sent starts the pause before the next message.
func (h *Hunter) sent() {
	h.mu.Lock()
	h.lastSend = time.Now()
	h.mu.Unlock()
}

var (
	formatLabels = map[string]string{
		"dvd": "DVD", "bluray": "Blu-ray", "uhd": "4K UHD",
		"cd": "CD", "vinyl": "Winyl", "cassette": "Kaseta",
	}
	sourceLabels = map[string]string{"olx": "OLX", "pepper": "Pepper"}
	stateLabels  = map[string]string{"new": "nowy", "used": "używany"}
)

// alertText is the Telegram message for a: plain text, so listing titles
// need no escaping.
func alertText(a *Alert) string {
	head := "🎯 Okazja: " + a.TitleName
	if a.Format != nil {
		if label, ok := formatLabels[*a.Format]; ok {
			head += " · " + label
		}
	}
	price := formatPLN(a.PriceCents)
	if a.MaxPriceCents != nil {
		price += " (limit " + formatPLN(*a.MaxPriceCents) + ")"
	}
	price += " · " + sourceLabels[a.Source]
	if a.AcquiredAs != nil {
		price += " · " + stateLabels[*a.AcquiredAs]
	}
	return strings.Join([]string{head, price, a.ListingTitle, a.URL}, "\n")
}

// formatPLN writes grosze as "1 234,50 zł".
func formatPLN(cents int64) string {
	zl := strconv.FormatInt(cents/100, 10)
	var b strings.Builder
	for i, d := range zl {
		if i > 0 && (len(zl)-i)%3 == 0 {
			b.WriteString(" ")
		}
		b.WriteRune(d)
	}
	return fmt.Sprintf("%s,%02d zł", b.String(), cents%100)
}
