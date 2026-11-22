package deals

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Automaat/ohara/backend-go/internal/dbtest"
)

const botToken = "123:SECRET"

// formValues is one request's form, a single value per key.
type formValues = map[string]string

// botDouble is a Bot API double. getUpdates serves the queued updates as
// Telegram does: an offset confirms and drops every update below it, and
// an empty answer waits a moment, like a short long poll. With forget set
// it confirms nothing, as when every confirmation is lost; oversizeAbove
// answers a getUpdates asking for more updates than it with a reply over
// telegramUpdatesCap; editDelay holds each keyboard edit.
type botDouble struct {
	mu            sync.Mutex
	queue         []map[string]any
	polls         []formValues
	sent          []formValues
	answers       []formValues
	edits         []formValues
	methods       []string
	forget        bool
	oversizeAbove int
	editDelay     time.Duration
	failPoll      func(n int, w http.ResponseWriter) bool
	onAnswer      func(form formValues)
}

func newBotDouble(t *testing.T) (*botDouble, *httptest.Server) {
	t.Helper()
	d := &botDouble{}
	srv := httptest.NewServer(http.HandlerFunc(d.serve))
	t.Cleanup(srv.Close)
	return d, srv
}

func (d *botDouble) serve(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	form := formValues{}
	for k := range r.PostForm {
		form[k] = r.PostForm.Get(k)
	}
	method, ok := strings.CutPrefix(r.URL.Path, "/bot"+botToken+"/")
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"ok":false,"description":"Not Found"}`)
		return
	}
	d.mu.Lock()
	d.methods = append(d.methods, method)
	switch method {
	case "getUpdates":
		d.polls = append(d.polls, form)
		if d.failPoll != nil && d.failPoll(len(d.polls), w) {
			d.mu.Unlock()
			return
		}
		if limit, _ := strconv.Atoi(form["limit"]); d.oversizeAbove > 0 && limit > d.oversizeAbove {
			d.mu.Unlock()
			writeResult(w, strings.Repeat("x", telegramUpdatesCap))
			return
		}
		offset, _ := strconv.ParseInt(form["offset"], 10, 64)
		var pending []map[string]any
		for _, u := range d.queue {
			if id, _ := u["update_id"].(int64); id >= offset || d.forget {
				pending = append(pending, u)
			}
		}
		d.queue = pending
		if limit, _ := strconv.Atoi(form["limit"]); limit > 0 && len(pending) > limit {
			pending = pending[:limit]
		}
		d.mu.Unlock()
		if len(pending) == 0 {
			select {
			case <-r.Context().Done():
			case <-time.After(20 * time.Millisecond):
			}
		}
		writeResult(w, pending)
		return
	case "sendMessage":
		d.sent = append(d.sent, form)
	case "answerCallbackQuery":
		d.answers = append(d.answers, form)
		if d.onAnswer != nil {
			d.onAnswer(form)
		}
	case "editMessageReplyMarkup":
		d.edits = append(d.edits, form)
		delay := d.editDelay
		d.mu.Unlock()
		time.Sleep(delay)
		writeResult(w, true)
		return
	}
	d.mu.Unlock()
	writeResult(w, true)
}

func writeResult(w http.ResponseWriter, result any) {
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
}

// press queues update id: a press of the "Ignoruj" button with data, on
// message 100+id in chat.
func (d *botDouble) press(id, chat int64, data string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.queue = append(d.queue, map[string]any{
		"update_id": id,
		"callback_query": map[string]any{
			"id":   "q" + strconv.FormatInt(id, 10),
			"data": data,
			"from": map[string]any{"id": 7, "is_bot": false, "first_name": "M"},
			"message": map[string]any{
				"message_id": 100 + id, "date": 1, "text": "🎯 Okazja",
				"chat": map[string]any{"id": chat, "type": "private"},
			},
		},
	})
}

// botCalls is what the double has seen so far.
type botCalls struct {
	polls, sent, answers, edits []formValues
	methods                     []string
	queued                      int
}

func (d *botDouble) snapshot() botCalls {
	d.mu.Lock()
	defer d.mu.Unlock()
	return botCalls{
		polls: append([]formValues(nil), d.polls...), sent: append([]formValues(nil), d.sent...),
		answers: append([]formValues(nil), d.answers...), edits: append([]formValues(nil), d.edits...),
		methods: append([]string(nil), d.methods...), queued: len(d.queue),
	}
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// lockedBuffer collects log output written from several goroutines.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func bot(srv *httptest.Server) *Telegram {
	return &Telegram{Client: srv.Client(), BaseURL: srv.URL, Token: botToken, ChatID: "42"}
}

// observationIDs maps each stored listing's external id to its id.
func observationIDs(t *testing.T, pool *pgxpool.Pool) map[string]int64 {
	t.Helper()
	ids := map[string]int64{}
	rows, err := pool.Query(context.Background(), `SELECT external_id, id FROM price_observation`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var ext string
		var id int64
		if err := rows.Scan(&ext, &id); err != nil {
			t.Fatal(err)
		}
		ids[ext] = id
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

func bladeRunnerListing(id string, price int64) Listing {
	return Listing{Source: "olx", ExternalID: id, Title: "Blade Runner Blu-ray", URL: "https://www.olx.pl/d/oferta/" + id, PriceCents: price}
}

func TestTelegramAlertButtons(t *testing.T) {
	pool := dbtest.Migrated(t)
	wishlist(t, pool, wantBluray(new(int64(5000))))
	double, srv := newBotDouble(t)
	src := &fakeSource{name: "olx", listings: []Listing{bladeRunnerListing("cheap", 4000)}}
	if res := mustRun(t, newTestHunter(pool, bot(srv), src)); res.Sent != 1 {
		t.Fatalf("run = %+v, want 1 sent", res)
	}
	sent := double.snapshot().sent
	if len(sent) != 1 {
		t.Fatalf("messages = %v, want 1", sent)
	}
	msg := sent[0]
	if msg["chat_id"] != "42" || !strings.HasPrefix(msg["text"], "🎯 Okazja: Blade Runner") {
		t.Fatalf("message = %v", msg)
	}
	var markup struct {
		InlineKeyboard [][]struct {
			Text         string `json:"text"`
			CallbackData string `json:"callback_data"`
		} `json:"inline_keyboard"`
	}
	if err := json.Unmarshal([]byte(msg["reply_markup"]), &markup); err != nil {
		t.Fatalf("reply_markup %q: %v", msg["reply_markup"], err)
	}
	if len(markup.InlineKeyboard) != 1 || len(markup.InlineKeyboard[0]) != 1 {
		t.Fatalf("keyboard = %+v, want one button", markup)
	}
	button := markup.InlineKeyboard[0][0]
	if button.Text != "Ignoruj" || button.CallbackData == "" || len(button.CallbackData) > 64 {
		t.Fatalf("button = %+v, want Ignoruj with 1-64 bytes of data", button)
	}
	want := observationIDs(t, pool)["cheap"]
	if id, ok := ignoredListing(button.CallbackData); !ok || id != want {
		t.Fatalf("callback data %q names %d, want listing %d", button.CallbackData, id, want)
	}
	if longest := ignoreData + strconv.FormatInt(1<<63-1, 10); len(longest) > 64 {
		t.Fatalf("callback data for the largest id is %d bytes", len(longest))
	}

	if err := bot(srv).Send(context.Background(), "x"); err != nil {
		t.Fatalf("plain send: %v", err)
	}
	if sent = double.snapshot().sent; len(sent) != 2 || sent[1]["reply_markup"] != "" {
		t.Fatalf("plain message = %v, want no keyboard", sent)
	}
}

func TestTelegramIgnore(t *testing.T) {
	pool := dbtest.Migrated(t)
	ctx := context.Background()
	store := NewStore(pool)
	wishlist(t, pool, wantBluray(new(int64(5000))))
	src := &fakeSource{name: "olx", listings: []Listing{
		bladeRunnerListing("pressed", 4000), bladeRunnerListing("other-chat", 4100),
		bladeRunnerListing("dismissed", 4200), bladeRunnerListing("while-down", 4300),
	}}
	if res := mustRun(t, newTestHunter(pool, &fakeNotifier{}, src)); res.Alerts != 4 {
		t.Fatalf("run = %+v, want 4 alerts", res)
	}
	ids := observationIDs(t, pool)
	if err := store.Dismiss(ctx, ids["dismissed"]); err != nil {
		t.Fatal(err)
	}
	dismissedAt := func(ext string) *time.Time {
		t.Helper()
		var at *time.Time
		if err := pool.QueryRow(ctx, `SELECT dismissed_at FROM price_observation WHERE id = $1`, ids[ext]).Scan(&at); err != nil {
			t.Fatal(err)
		}
		return at
	}
	firstDismissal := dismissedAt("dismissed")
	data := func(ext string) string { return ignoreData + strconv.FormatInt(ids[ext], 10) }

	double, srv := newBotDouble(t)
	double.press(10, 42, data("pressed"))
	double.press(11, 99, data("other-chat"))
	double.press(12, 42, data("dismissed"))
	start := func() (context.CancelFunc, <-chan struct{}) {
		pollCtx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		p := &TelegramPoller{Bot: bot(srv), Store: store, Logger: slog.New(slog.DiscardHandler)}
		go func() {
			defer close(done)
			p.Run(pollCtx)
		}()
		return cancel, done
	}

	cancel, done := start()
	waitUntil(t, "a poll confirming the first batch", func() bool {
		for _, p := range double.snapshot().polls {
			if p["offset"] == "13" {
				return true
			}
		}
		return false
	})
	cancel()
	<-done

	calls := double.snapshot()
	polls, answers, edits, queued := calls.polls, calls.answers, calls.edits, calls.queued
	if polls[0]["offset"] != "" || polls[0]["allowed_updates"] != `["callback_query"]` {
		t.Fatalf("first poll = %v, want no offset and only callback queries", polls[0])
	}
	if queued != 0 {
		t.Fatalf("%d updates left unconfirmed", queued)
	}
	got := map[string]string{}
	for _, a := range answers {
		if _, seen := got[a["callback_query_id"]]; seen {
			t.Fatalf("press %s answered twice", a["callback_query_id"])
		}
		got[a["callback_query_id"]] = a["text"]
	}
	if len(got) != 3 || got["q10"] != "Zignorowano" || got["q11"] == "" || got["q11"] == "Zignorowano" || got["q12"] == "" {
		t.Fatalf("answers = %v, want every press answered and only q10 and q12 as dismissals", got)
	}
	if dismissedAt("pressed") == nil {
		t.Fatal("pressed listing not dismissed")
	}
	if dismissedAt("other-chat") != nil {
		t.Fatal("press from another chat dismissed the listing")
	}
	if at := dismissedAt("dismissed"); at == nil || !at.Equal(*firstDismissal) {
		t.Fatalf("already dismissed listing changed: %v, want %v", at, firstDismissal)
	}
	if len(edits) == 0 || edits[0]["chat_id"] != "42" || edits[0]["message_id"] != "110" || edits[0]["reply_markup"] != "" {
		t.Fatalf("edits = %v, want the button taken off message 110", edits)
	}
	for _, e := range edits {
		if e["chat_id"] != "42" {
			t.Fatalf("edited a message in chat %s", e["chat_id"])
		}
	}

	double.press(13, 42, data("while-down"))
	stop := make(chan struct{})
	var once sync.Once
	double.mu.Lock()
	double.onAnswer = func(formValues) { once.Do(func() { close(stop) }) }
	double.mu.Unlock()
	cancel, done = start()
	<-stop
	cancel()
	<-done
	if dismissedAt("while-down") == nil {
		t.Fatal("press made while down not dismissed")
	}
	calls = double.snapshot()
	polls, answers, queued = calls.polls, calls.answers, calls.queued
	if queued != 0 {
		t.Fatalf("%d updates left unconfirmed after stopping, last poll %v", queued, polls[len(polls)-1])
	}
	cancel, done = start()
	waitUntil(t, "two more polls", func() bool {
		return len(double.snapshot().polls) >= len(polls)+2
	})
	cancel()
	<-done
	if again := double.snapshot().answers; len(again) != len(answers) || len(answers) != 4 {
		t.Fatalf("answers = %v after restarts, want each of the 4 presses once", again)
	}
}

func TestTelegramPollerRetries(t *testing.T) {
	pool := dbtest.Migrated(t)
	wishlist(t, pool, wantBluray(new(int64(5000))))
	double, srv := newBotDouble(t)
	var pollTimes []time.Time
	double.failPoll = func(n int, w http.ResponseWriter) bool {
		pollTimes = append(pollTimes, time.Now())
		if n%2 == 1 {
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `{"ok":false,"error_code":409,"description":"Conflict: can't use getUpdates method while webhook is active; use deleteWebhook to delete the webhook first"}`)
		} else if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
			_ = conn.Close()
		}
		return true
	}
	logs := &lockedBuffer{}
	const pause = 50 * time.Millisecond
	h := newTestHunter(pool, bot(srv), &fakeSource{name: "olx", listings: []Listing{bladeRunnerListing("cheap", 4000)}})
	h.PollTelegram(&TelegramPoller{Bot: bot(srv), Store: h.store, Logger: slog.New(slog.NewTextHandler(logs, nil)), Pause: pause})
	ctx, cancel := context.WithCancel(context.Background())
	h.Start(ctx)
	waitUntil(t, "a third poll", func() bool {
		return len(double.snapshot().polls) >= 3
	})
	if res := mustRun(t, h); res.Sent != 1 {
		t.Fatalf("run = %+v, want the alert sent while polling fails", res)
	}
	cancel()
	h.Wait()

	double.mu.Lock()
	times := append([]time.Time(nil), pollTimes...)
	double.mu.Unlock()
	for i := 1; i < len(times); i++ {
		if gap := times[i].Sub(times[i-1]); gap < pause {
			t.Fatalf("poll %d came %v after a failure, want at least %v", i+1, gap, pause)
		}
	}
	out := logs.String()
	if strings.Count(out, "telegram poll failed") < 3 || !strings.Contains(out, "webhook is active") ||
		!strings.Contains(out, "EOF") {
		t.Fatalf("logs = %q, want each failure logged", out)
	}
	if strings.Contains(out, "SECRET") {
		t.Fatalf("logs carry the bot token: %q", out)
	}
}

// pressListings stores one alerted Blade Runner listing per external id
// and returns the store and the listings' ids.
func pressListings(t *testing.T, exts ...string) (*pgxpool.Pool, *Store, map[string]int64) {
	t.Helper()
	pool := dbtest.Migrated(t)
	wishlist(t, pool, wantBluray(new(int64(5000))))
	src := &fakeSource{name: "olx"}
	for i, ext := range exts {
		src.listings = append(src.listings, bladeRunnerListing(ext, 4000+int64(i)))
	}
	if res := mustRun(t, newTestHunter(pool, &fakeNotifier{}, src)); res.Alerts != len(exts) {
		t.Fatalf("run = %+v, want %d alerts", res, len(exts))
	}
	return pool, NewStore(pool), observationIDs(t, pool)
}

func isDismissed(t *testing.T, pool *pgxpool.Pool, id int64) bool {
	t.Helper()
	var dismissed bool
	if err := pool.QueryRow(context.Background(),
		`SELECT dismissed_at IS NOT NULL FROM price_observation WHERE id = $1`, id).Scan(&dismissed); err != nil {
		t.Fatal(err)
	}
	return dismissed
}

// runPoller runs p until the returned func is called, which waits for Run
// to return.
func runPoller(p *TelegramPoller) func() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.Run(ctx)
	}()
	return func() {
		cancel()
		<-done
	}
}

func pressData(id int64) string { return ignoreData + strconv.FormatInt(id, 10) }

func TestTelegramReplayedPressKeepsUndo(t *testing.T) {
	pool, store, ids := pressListings(t, "a")
	double, srv := newBotDouble(t)
	double.forget = true
	double.press(10, 42, pressData(ids["a"]))
	poller := func() *TelegramPoller {
		return &TelegramPoller{Bot: bot(srv), Store: store, Logger: slog.New(slog.DiscardHandler)}
	}
	stop := runPoller(poller())
	waitUntil(t, "the press served again", func() bool {
		calls := double.snapshot()
		return len(calls.answers) == 1 && len(calls.polls) >= 3
	})
	stop()
	if !isDismissed(t, pool, ids["a"]) {
		t.Fatal("press did not dismiss the listing")
	}
	if err := store.Undismiss(context.Background(), ids["a"]); err != nil {
		t.Fatal(err)
	}
	before := len(double.snapshot().polls)
	stop = runPoller(poller())
	waitUntil(t, "the press served after a restart", func() bool { return len(double.snapshot().polls) >= before+2 })
	stop()
	if isDismissed(t, pool, ids["a"]) {
		t.Fatal("replayed press undid Cofnij")
	}
	if calls := double.snapshot(); len(calls.answers) != 1 || len(calls.edits) != 1 {
		t.Fatalf("answers %v, edits %v, want the press answered and its button removed once", calls.answers, calls.edits)
	}
}

func TestTelegramAnswersBeforeSlowEdit(t *testing.T) {
	pool, store, ids := pressListings(t, "a")
	double, srv := newBotDouble(t)
	double.editDelay = 300 * time.Millisecond
	double.press(10, 42, pressData(ids["a"]))
	stop := runPoller(&TelegramPoller{Bot: bot(srv), Store: store, Logger: slog.New(slog.DiscardHandler), pressTimeout: 100 * time.Millisecond})
	waitUntil(t, "the edit", func() bool { return len(double.snapshot().edits) == 1 })
	stop()
	calls := double.snapshot()
	if len(calls.answers) != 1 || calls.answers[0]["text"] != "Zignorowano" {
		t.Fatalf("answers = %v, want the press answered", calls.answers)
	}
	answered := slices.Index(calls.methods, "answerCallbackQuery")
	if edited := slices.Index(calls.methods, "editMessageReplyMarkup"); answered < 0 || answered > edited {
		t.Fatalf("calls = %v, want the answer before the edit", calls.methods)
	}
	if !isDismissed(t, pool, ids["a"]) {
		t.Fatal("press did not dismiss the listing")
	}
}

func TestTelegramOversizedBatch(t *testing.T) {
	pool, store, ids := pressListings(t, "a", "b", "c")
	double, srv := newBotDouble(t)
	double.oversizeAbove = 5
	for i, ext := range []string{"a", "b", "c"} {
		double.press(int64(10+i), 42, pressData(ids[ext]))
	}
	stop := runPoller(&TelegramPoller{Bot: bot(srv), Store: store, Logger: slog.New(slog.DiscardHandler), Pause: time.Hour})
	waitUntil(t, "every press answered", func() bool { return len(double.snapshot().answers) == 3 })
	stop()
	var limits []string
	for _, p := range double.snapshot().polls[:3] {
		limits = append(limits, p["limit"])
	}
	if !slices.Equal(limits, []string{"20", "10", "5"}) {
		t.Fatalf("limits = %v, want the batch halved after each oversized reply", limits)
	}
	for _, ext := range []string{"a", "b", "c"} {
		if !isDismissed(t, pool, ids[ext]) {
			t.Fatalf("listing %s not dismissed", ext)
		}
	}
}

func TestTelegramShutdownLeavesRestOfBatch(t *testing.T) {
	pool, store, ids := pressListings(t, "a", "b", "c")
	double, srv := newBotDouble(t)
	for i, ext := range []string{"a", "b", "c"} {
		double.press(int64(10+i), 42, pressData(ids[ext]))
	}
	ctx, cancel := context.WithCancel(context.Background())
	double.onAnswer = func(formValues) { cancel() }
	(&TelegramPoller{Bot: bot(srv), Store: store, Logger: slog.New(slog.DiscardHandler)}).Run(ctx)
	if calls := double.snapshot(); len(calls.answers) != 1 || calls.queued != 2 {
		t.Fatalf("answers %v, %d updates queued, want only the first press handled and confirmed", calls.answers, calls.queued)
	}
	if !isDismissed(t, pool, ids["a"]) || isDismissed(t, pool, ids["b"]) {
		t.Fatal("want only the first listing dismissed before the restart")
	}
	double.mu.Lock()
	double.onAnswer = nil
	double.mu.Unlock()
	stop := runPoller(&TelegramPoller{Bot: bot(srv), Store: store, Logger: slog.New(slog.DiscardHandler)})
	waitUntil(t, "the rest answered after a restart", func() bool { return len(double.snapshot().answers) == 3 })
	stop()
	for _, ext := range []string{"b", "c"} {
		if !isDismissed(t, pool, ids[ext]) {
			t.Fatalf("listing %s not dismissed after the restart", ext)
		}
	}
}
