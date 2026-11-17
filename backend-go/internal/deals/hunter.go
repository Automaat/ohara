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
)

// maxRunErrors bounds the source errors a run keeps for the status view.
const maxRunErrors = 20

// telegramPause keeps alerts under Telegram's one message per second per
// chat.
const telegramPause = time.Second

// markSentTimeout bounds recording a delivered alert after the run's
// context is gone.
const markSentTimeout = 10 * time.Second

// Config tunes the hunter. Interval 0 disables the schedule; a run can still
// be started by hand. Delay separates two source requests; both are
// jittered.
type Config struct {
	Interval time.Duration
	Delay    time.Duration
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

// Start runs the schedule until ctx ends; manual runs also stop with ctx.
// The first scheduled run waits up to five minutes so a restart loop does
// not hammer the sources.
func (h *Hunter) Start(ctx context.Context) {
	h.base = ctx
	if h.cfg.Interval <= 0 {
		return
	}
	h.wg.Go(func() {
		wait := jitter(min(h.cfg.Interval, 5*time.Minute), 0.2)
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

// execute runs once and records the outcome; the caller holds running.
func (h *Hunter) execute(ctx context.Context) {
	defer h.running.Store(false)
	started := time.Now()
	h.mu.Lock()
	h.status.LastStartedAt = &started
	h.mu.Unlock()

	res, err := h.Run(ctx)
	if err != nil {
		res.Failure = err.Error()
		h.logger.Error("deal search failed", "err", err)
	} else {
		h.logger.Info("deal search finished", "wants", res.Wants, "queries", res.Queries,
			"listings", res.Listings, "matched", res.Matched, "new", res.New,
			"alerts", res.Alerts, "sent", res.Sent, "errors", len(res.Errors))
	}
	finished := time.Now()
	h.mu.Lock()
	h.status.LastFinishedAt = &finished
	h.status.LastResult = &res
	h.mu.Unlock()
}

// run is one search: its counters and pacing between requests.
type run struct {
	h     *Hunter
	wants []Want
	rules Rules
	res   RunResult
	first bool
}

// Run searches every source for every wishlist copy, records matching
// listings and delivers pending alerts. Source failures are collected in
// the result; a database failure or cancellation stops the run.
func (h *Hunter) Run(ctx context.Context) (RunResult, error) {
	wants, err := h.store.Wants(ctx)
	if err != nil {
		return RunResult{}, err
	}
	rules, err := h.store.Rules(ctx)
	if err != nil {
		return RunResult{}, err
	}
	r := &run{h: h, wants: wants, rules: rules, first: true}
	r.res.Wants = len(wants)
	r.res.Errors = []string{}
	if len(wants) > 0 {
		queries := searchQueries(wants)
		r.res.Queries = len(queries)
		for _, src := range h.sources {
			if err := r.search(ctx, src, queries); err != nil {
				return r.res, err
			}
		}
	}
	if err := r.deliver(ctx); err != nil {
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

func (r *run) search(ctx context.Context, src Source, queries []string) error {
	if !src.PerQuery() {
		queries = []string{""}
	}
	for _, q := range queries {
		if err := r.pause(ctx); err != nil {
			return err
		}
		listings, err := src.Search(ctx, q)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			r.h.logger.Warn("deal source failed", "source", src.Name(), "query", q, "err", err)
			if len(r.res.Errors) < maxRunErrors {
				r.res.Errors = append(r.res.Errors, err.Error())
			}
			continue
		}
		if err := r.record(ctx, listings); err != nil {
			return err
		}
	}
	return nil
}

func (r *run) record(ctx context.Context, listings []Listing) error {
	r.res.Listings += len(listings)
	for i := range listings {
		l := &listings[i]
		w := pick(r.wants, l, r.rules)
		if w == nil {
			continue
		}
		r.res.Matched++
		alert := r.h.notifier != nil && w.MaxPriceCents != nil && l.PriceCents <= *w.MaxPriceCents
		isNew, err := r.h.store.Record(ctx, l, w, alert)
		if err != nil {
			return err
		}
		if isNew {
			r.res.New++
			if alert {
				r.res.Alerts++
			}
		}
	}
	return nil
}

// deliver sends every pending alert; a failed send is retried next run.
func (r *run) deliver(ctx context.Context) error {
	if r.h.notifier == nil {
		return nil
	}
	alerts, err := r.h.store.PendingAlerts(ctx)
	if err != nil {
		return err
	}
	for i := range alerts {
		a := &alerts[i]
		if i > 0 {
			if err := r.h.sleep(ctx, telegramPause); err != nil {
				return err
			}
		}
		if err := r.h.notifier.Send(ctx, alertText(a)); err != nil {
			r.h.logger.Warn("telegram alert failed", "observation", a.ObservationID, "err", err)
			if err := r.h.store.MarkFailed(ctx, a.ObservationID, err.Error()); err != nil {
				return err
			}
			continue
		}
		// The message is out: record it even when ctx was canceled during
		// the send, or the next run sends it again.
		markCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), markSentTimeout)
		err := r.h.store.MarkSent(markCtx, a.ObservationID)
		cancel()
		if err != nil {
			return err
		}
		r.res.Sent++
	}
	return nil
}

var (
	formatLabels = map[string]string{"dvd": "DVD", "bluray": "Blu-ray", "uhd": "4K UHD"}
	sourceLabels = map[string]string{"olx": "OLX", "pepper": "Pepper", "ceneo": "Ceneo"}
	stateLabels  = map[string]string{"new": "nowy", "used": "używany"}
)

// alertText is the Telegram message for a: plain text, so listing titles
// need no escaping.
func alertText(a *Alert) string {
	head := "🎯 Okazja: " + a.TitleName
	if a.Format != nil {
		head += " · " + formatLabels[*a.Format]
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
