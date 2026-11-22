package deals

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Automaat/ohara/backend-go/internal/metrics"
	"github.com/Automaat/ohara/backend-go/internal/recovery"
)

// maxRunErrors bounds the source errors a run keeps for the status view.
// A refusal, at most one per source, is kept beyond it.
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
// answers with none is broken. A Canaried source asks its own query
// instead. A feed source's one request is its canary.
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
	coverage *runCoverage
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
	poller   *TelegramPoller
	logger   *slog.Logger
	cfg      Config
	sleep    func(ctx context.Context, d time.Duration) error
	// now reads the clock that paces alert messages.
	now func() time.Time
	// status holds the settings the status view shows; task holds the
	// runs.
	status Status
	task   *Task[RunResult]

	mu sync.Mutex
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
		sleep: sleepCtx, now: time.Now,
	}
	h.task = NewTask[RunResult](h.execute)
	h.status.Scheduled = cfg.Interval > 0
	h.status.IntervalSeconds = int64(cfg.Interval.Seconds())
	h.status.Telegram = notifier != nil
	metrics.InitBackgroundTask(metrics.TaskDeals)
	if notifier != nil {
		metrics.InitDealAlerts()
	}
	for _, src := range sources {
		metrics.InitSource(src.Name())
	}
	return h
}

// PollTelegram makes Start run p, which turns presses of an alert's
// "Ignoruj" button into dismissals, until Start's context ends.
func (h *Hunter) PollTelegram(p *TelegramPoller) { h.poller = p }

// Start restores the last stored run, then runs the schedule until ctx
// ends; manual runs and the Telegram poller also stop with ctx. The first
// scheduled run comes one interval after the last run started, or about
// five minutes after start without one, and never sooner than
// firstRunDelay.
func (h *Hunter) Start(ctx context.Context) {
	h.task.Bind(ctx)
	if h.poller != nil {
		h.task.Go(func() { h.poller.Run(ctx) })
	}
	last, ok := h.restore(ctx)
	if h.cfg.Interval <= 0 {
		return
	}
	due, spread := firstRunDelay, 0.2
	if ok {
		due, spread = time.Until(last.Add(h.cfg.Interval)), 0.1
	}
	h.task.Schedule(ctx, max(jitter(due, spread), firstRunDelay), h.cfg.Interval, h.sleep)
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
	h.task.Restore(TaskRun[RunResult]{StartedAt: &last.StartedAt, FinishedAt: last.FinishedAt, Result: last.Result})
	return last.StartedAt, true
}

// Wait blocks until the schedule and any manual run have stopped.
func (h *Hunter) Wait() { h.task.Wait() }

// Trigger starts a run in the background and reports false when one is
// already running.
func (h *Hunter) Trigger() bool { return h.task.Trigger() }

// Status returns a copy of the current state.
func (h *Hunter) Status() Status {
	last, running := h.task.State()
	s := h.status
	s.Running = running
	s.LastStartedAt, s.LastFinishedAt, s.LastResult = last.StartedAt, last.FinishedAt, last.Result
	return s
}

// execute runs once and records the outcome, in the status and the
// database; h.task calls it holding the one run. Times keep the
// database's microseconds, so a restart restores the same status.
func (h *Hunter) execute(ctx context.Context) {
	started := time.Now().Truncate(time.Microsecond)
	h.task.Started(started)
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
	h.task.Finished(finished, &res)
	h.saveRun(ctx, func(ctx context.Context) error { return h.store.RunFinished(ctx, started, finished, &res) })
	metrics.ObserveBackgroundRun(metrics.TaskDeals, finished.Sub(started), err)
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
	h      *Hunter
	wants  []Want
	rules  Rules
	titles Titles
	// asOf is when the run read wants and titles, for RecordAsOf.
	asOf  time.Time
	res   RunResult
	first bool
	// percent is whether a want has a market percent, whose market price
	// the listings this run stores may move.
	percent bool
	// missed holds the listings this run stored that met no rule, by
	// listingKey, to judge again once a later answer moves the market.
	missed map[string]Listing
	// tried holds the observations whose alert this run sent or failed to
	// send; a failed one waits for the next run.
	tried map[int64]bool
	kinds map[string][]string
	// feeds sums the answers of the feed sources that share a name (one
	// Discogs seller each), by name.
	feeds map[string]feedTally

	queryWants      map[string][]*Want
	coverageRows    map[coverageKey]*wishCoverage
	coverageMatches map[coverageMatch]struct{}
}

// feedTally is what the feed sources of one name answered in a run: how
// many listings, and whether any answered with listings.
type feedTally struct {
	listings int
	ok       bool
}

// Run searches every source for every wishlist copy, records matching
// listings and delivers pending alerts. An alert for a listing an answer
// matched goes out before the run asks a source again; every other pending
// alert, such as one for a listing no answer matched, goes out after the
// last request.
// Source failures are collected in the result; a database failure or
// cancellation stops the run.
func (h *Hunter) Run(ctx context.Context) (RunResult, error) {
	release, err := h.store.HoldRun(ctx)
	if err != nil {
		return RunResult{}, err
	}
	defer release()
	if _, err := h.store.ForgetStale(ctx); err != nil {
		return RunResult{}, err
	}
	asOf, err := h.store.Now(ctx)
	if err != nil {
		return RunResult{}, err
	}
	wants, err := h.store.Wants(ctx)
	if err != nil {
		return RunResult{}, err
	}
	rules, err := h.store.Rules(ctx)
	if err != nil {
		return RunResult{}, err
	}
	titles, err := h.store.Titles(ctx)
	if err != nil {
		return RunResult{}, err
	}
	r := &run{
		h: h, wants: wants, rules: rules, titles: titles, asOf: asOf, first: true, tried: map[int64]bool{},
		missed: map[string]Listing{}, feeds: map[string]feedTally{},
		coverageRows: map[coverageKey]*wishCoverage{}, coverageMatches: map[coverageMatch]struct{}{},
	}
	for i := range wants {
		wants[i].Prepare()
		r.percent = r.percent || wants[i].MaxMarketPercent != nil
	}
	r.res.Wants = len(wants)
	r.res.Errors = []string{}
	r.res.coverage = &runCoverage{Rows: []*wishCoverage{}}
	queries, queryWants := planQueries(wants)
	r.queryWants = queryWants
	r.res.Queries = len(queries)
	if len(wants) > 0 || h.cfg.CanaryQuery != "" {
		for _, src := range h.sources {
			if err := r.search(ctx, src, r.queriesFor(src, queries)); err != nil {
				return r.res, err
			}
		}
	}
	if err := r.deliver(ctx, nil); err != nil {
		return r.res, err
	}
	// A source that saw the cancellation may have answered without an
	// error; a run cut short is not a finished one.
	if err := ctx.Err(); err != nil {
		return r.res, err
	}
	return r.res, nil
}

// queriesFor are the queries src is asked: all of them, or for a
// KindSource only those of the wishes of its kinds, whose kinds the run
// then keeps for matching the source's listings.
func (r *run) queriesFor(src Source, all []string) []string {
	ks, ok := src.(KindSource)
	if !ok {
		return all
	}
	kinds := ks.Kinds()
	if r.kinds == nil {
		r.kinds = map[string][]string{}
	}
	r.kinds[src.Name()] = kinds
	var wants []Want
	for i := range r.wants {
		if slices.Contains(kinds, r.wants[i].Kind) {
			wants = append(wants, r.wants[i])
		}
	}
	return searchQueries(wants)
}

// searchQueries are the distinct queries of every want, in want order.
func searchQueries(wants []Want) []string {
	queries, _ := planQueries(wants)
	return queries
}

func planQueries(wants []Want) ([]string, map[string][]*Want) {
	var out []string
	owners := map[string][]*Want{}
	for i := range wants {
		w := &wants[i]
		for _, q := range w.Queries() {
			key := spelledKey(q)
			previous := owners[key]
			if len(previous) == 0 {
				out = append(out, q)
			}
			if len(previous) == 0 || previous[len(previous)-1] != w {
				owners[key] = append(previous, w)
			}
		}
	}
	return out, owners
}

func (r *run) pause(ctx context.Context) error {
	if r.first {
		r.first = false
		return ctx.Err()
	}
	return r.h.sleep(ctx, jitter(r.h.cfg.Delay, 0.5))
}

// search asks src for the canary query and every wishlist query, then
// publishes the listing count and canary outcome. A wishlist query that is
// the canary query takes the canary's answer, or its failure, instead of
// asking again, and its listings count once. A feed source is asked once:
// that answer is both its canary and, with a wishlist, its listings; feed
// sources of one name publish the sum of their listings and a canary that
// holds once any of them found some.
func (r *run) search(ctx context.Context, src Source, queries []string) error {
	var f Refused
	err := r.searchAll(ctx, src, &f, queries)
	site := src.Name()
	if e, ok := src.(Expiring); ok {
		site += " " + e.Scope()
	}
	if msg := f.Message(site, "queries"); msg != "" {
		r.res.Errors = append(r.res.Errors, msg)
	}
	return err
}

func (r *run) searchAll(ctx context.Context, src Source, f *Refused, queries []string) error {
	canary := canaryQuery(src, r.h.cfg.CanaryQuery)
	if !src.PerQuery() {
		listings, outcome, err := r.ask(ctx, src, f, "")
		if err != nil {
			return err
		}
		targets := r.queryCoverage(src.Name(), "", true, outcome, len(listings))
		ok := outcome == querySucceeded
		t := r.feeds[src.Name()]
		t.listings += len(listings)
		t.ok = t.ok || ok && len(listings) > 0
		r.feeds[src.Name()] = t
		if canary != "" {
			r.canary(src, canary, ok, len(listings))
			metrics.SetSourceCanary(src.Name(), t.ok)
		}
		metrics.SetSourceResults(src.Name(), t.listings)
		if ok && len(r.wants) > 0 {
			return r.record(ctx, src.Name(), listings, targets)
		}
		return nil
	}
	results := 0
	var answered []Listing
	var answeredOutcome queryOutcome
	if canary != "" {
		listings, outcome, err := r.ask(ctx, src, f, canary)
		if err != nil {
			return err
		}
		r.canary(src, canary, outcome == querySucceeded, len(listings))
		results += len(listings)
		answered, answeredOutcome = listings, outcome
	}
	for _, q := range queries {
		listings, outcome := answered, answeredOutcome
		if canary == "" || q != canary {
			var err error
			if listings, outcome, err = r.ask(ctx, src, f, q); err != nil {
				return err
			}
			if outcome == querySucceeded {
				results += len(listings)
			}
		}
		targets := r.queryCoverage(src.Name(), q, false, outcome, len(listings))
		if outcome != querySucceeded {
			continue
		}
		if err := r.record(ctx, src.Name(), listings, targets); err != nil {
			return err
		}
	}
	metrics.SetSourceResults(src.Name(), results)
	return nil
}

// ask sends one request to the source after the pause and counts its
// outcome. A source failure is logged and kept in the result; a refusal
// is kept once, when the source is done. A source that
// refused is not asked again: the query is skipped without a pause. Every
// offer a successful answer holds, priced or not, matching or not, is
// recorded as offered (Store.MarkOffered) before ask returns; for an
// Expiring source, a stored listing of its scope the answer lacks is gone
// (Store.UpdateGone). err is the run's cancellation or a database failure.
func (r *run) ask(ctx context.Context, src Source, f *Refused, query string) ([]Listing, queryOutcome, error) {
	if f.Skip() {
		return nil, querySkipped, ctx.Err()
	}
	if err := r.pause(ctx); err != nil {
		return nil, queryFailed, err
	}
	page, err := searchPage(ctx, src, query)
	if err != nil && ctx.Err() != nil {
		return nil, queryFailed, ctx.Err()
	}
	ObserveSearch(src, err)
	if err != nil {
		r.h.logger.Warn("deal source failed", "source", src.Name(), "query", query, "err", err)
		if f.Note(err) {
			return nil, queryRefused, nil
		}
		r.fail(err.Error())
		return nil, queryFailed, nil
	}
	if e, ok := src.(Expiring); ok {
		if _, err := r.h.store.UpdateGone(ctx, src.Name(), []string{QueryKey(e.Scope())}, page.Seen, r.asOf); err != nil {
			return nil, queryFailed, err
		}
	} else if err := r.h.store.MarkOffered(ctx, src.Name(), page.Seen); err != nil {
		return nil, queryFailed, err
	}
	return page.Listings, querySucceeded, nil
}

// searchPage asks src one query. A Pager's answer names every offer it
// holds in Seen; another source's Seen is its listings' external IDs.
func searchPage(ctx context.Context, src Source, query string) (Page, error) {
	if pager, ok := src.(Pager); ok {
		return pager.SearchPage(ctx, query)
	}
	listings, err := src.Search(ctx, query)
	if err != nil {
		return Page{}, err
	}
	page := Page{Listings: listings}
	for i := range listings {
		if listings[i].ExternalID != "" {
			page.Seen = append(page.Seen, listings[i].ExternalID)
		}
	}
	return page, nil
}

// canary publishes the canary outcome of src; an answer without listings
// is a failure too, since the canary query always has some.
func (r *run) canary(src Source, query string, ok bool, listings int) {
	metrics.SetSourceCanary(src.Name(), ok && listings > 0)
	if ok && listings == 0 {
		r.h.logger.Warn("deal source canary found no listings", "source", src.Name(), "query", query)
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
// the refreshed price against the wish's current limit and market share,
// which may have changed since the run read the wishlist. A listing of
// this run that met no rule is judged again against the market prices read
// once the answer is stored, since its listings may have moved them.
func (r *run) record(ctx context.Context, source string, listings []Listing, targets []coverageTarget) error {
	r.res.Listings += len(listings)
	refreshed := map[string]bool{}
	for i := range listings {
		l := &listings[i]
		text := l.text(r.rules)
		w := pickListing(r.wants, r.kinds[l.Source], l, text, r.titles)
		r.coverListing(source, targets, l, text, w)
		if w == nil {
			continue
		}
		r.res.Matched++
		alert := r.h.notifier != nil && w.Meets(l.PriceCents)
		isNew, alerted, err := r.h.store.RecordAsOf(ctx, l, w, alert, r.asOf)
		if err != nil {
			return err
		}
		if isNew {
			r.res.New++
		}
		if alerted {
			r.res.Alerts++
		}
		key := listingKey(l.Source, l.ExternalID)
		if r.h.notifier != nil && r.percent && !alert {
			r.missed[key] = *l
		} else {
			delete(r.missed, key)
		}
		refreshed[key] = true
	}
	if len(refreshed) == 0 {
		return nil
	}
	if len(r.missed) > 0 {
		if err := r.remeasure(ctx, refreshed); err != nil {
			return err
		}
	}
	return r.deliver(ctx, refreshed)
}

// remeasure reads the market prices again and queues the alerts of the
// missed listings that now meet a want's share of them, adding them to
// refreshed so they go out before the run asks a source again. The new
// prices also judge the rest of the run's answers.
func (r *run) remeasure(ctx context.Context, refreshed map[string]bool) error {
	if err := r.h.store.RefreshMarkets(ctx, r.wants); err != nil {
		return err
	}
	for _, key := range slices.Sorted(maps.Keys(r.missed)) {
		l := r.missed[key]
		w := pickOf(r.wants, r.kinds[l.Source], &l, r.rules, r.titles)
		if w == nil || !w.Meets(l.PriceCents) {
			continue
		}
		_, alerted, err := r.h.store.RecordAsOf(ctx, &l, w, true, r.asOf)
		if err != nil {
			return err
		}
		if alerted {
			r.res.Alerts++
		}
		delete(r.missed, key)
		refreshed[key] = true
	}
	return nil
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
		delivered, err := r.h.store.SendAlert(ctx, a.ObservationID, func(due *Alert) error {
			err := r.h.notify(ctx, due)
			r.h.sent()
			metrics.ObserveDealAlert(err)
			if err != nil {
				r.h.logger.Warn("telegram alert failed", "observation", due.ObservationID, "err", err)
			}
			return err
		})
		if err != nil {
			return err
		}
		if delivered {
			// A later sighting in this run may queue it again at a lower price.
			delete(r.tried, a.ObservationID)
			r.res.Sent++
		}
	}
	return nil
}

// notify sends the message for a, with its "Ignoruj" button when the
// notifier can attach one.
func (h *Hunter) notify(ctx context.Context, a *Alert) error {
	if n, ok := h.notifier.(AlertNotifier); ok {
		return n.SendAlert(ctx, alertText(a), a.ObservationID, a.CopyID)
	}
	return h.notifier.Send(ctx, alertText(a))
}

// sendWait is what is left of telegramPause since the notifier last
// returned.
func (h *Hunter) sendWait() time.Duration {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.lastSend.IsZero() {
		return 0
	}
	return max(telegramPause-h.now().Sub(h.lastSend), 0)
}

// sent starts the pause before the next message.
func (h *Hunter) sent() {
	h.mu.Lock()
	h.lastSend = h.now()
	h.mu.Unlock()
}

var (
	formatLabels = map[string]string{
		"dvd": "DVD", "bluray": "Blu-ray", "uhd": "4K UHD",
		"cd": "CD", "vinyl": "Winyl", "cassette": "Kaseta", FormatAudiobook: "Audiobook",
	}
	sourceLabels = map[string]string{
		"olx": "OLX", "bluedvd": "BlueDVD", "winylownia": "Winylownia", "swiatksiazki": "Świat Książki", "pepper": "Pepper", "tezeusz": "Tezeusz",
		DiscogsSource: "Discogs",
	}
	stateLabels = map[string]string{"new": "nowy", "used": "używany"}
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
	var notes []string
	if a.AlertedPriceCents != nil {
		notes = append(notes, "było "+formatPLN(*a.AlertedPriceCents))
	}
	if a.MaxPriceCents != nil {
		notes = append(notes, "limit "+formatPLN(*a.MaxPriceCents))
	}
	if a.MaxMarketPercent != nil && a.MarketCents != nil {
		notes = append(notes, fmt.Sprintf("%d%% ceny rynkowej %s", *a.MaxMarketPercent, formatPLN(*a.MarketCents)))
	}
	if len(notes) > 0 {
		price += " (" + strings.Join(notes, ", ") + ")"
	}
	price += " · " + sourceLabels[a.Source]
	if a.AcquiredAs != nil {
		price += " · " + stateLabels[*a.AcquiredAs]
	}
	lines := []string{head, price, a.ListingTitle, a.URL}
	if a.Source == DiscogsSource {
		lines = append(lines, discogsNotice)
	}
	return strings.Join(lines, "\n")
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
