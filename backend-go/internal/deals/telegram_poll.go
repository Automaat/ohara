package deals

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/Automaat/ohara/backend-go/internal/recovery"
)

// telegramPollTimeout is how long one getUpdates waits for a press.
const telegramPollTimeout = 50 * time.Second

// telegramPollPause is the wait after a failed getUpdates.
const telegramPollPause = 30 * time.Second

// telegramPressTimeout bounds each step of handling one press: the
// dismissal, the answer and the keyboard edit. It outlives the poller's
// context, so a press begun before shutdown is still answered.
const telegramPressTimeout = 10 * time.Second

// telegramEditAttempts bounds the tries to take a button off a message
// when the Bot API fails for now; they are telegramEditRetryWait times the
// attempt apart.
const telegramEditAttempts = 3

// telegramEditRetryWait is the base wait between two tries of a keyboard
// edit.
const telegramEditRetryWait = time.Second

// telegramBatch is how many updates one getUpdates asks for. Each carries
// its whole alert message, up to 4096 characters, so a batch of 20 stays
// well under telegramUpdatesCap; a reply over it halves the batch.
const telegramBatch = 20

// boughtNote ends an alert's text once its "Kupione" press recorded the
// purchase.
const boughtNote = "\n\n✅ Kupione: zapisano w kolekcji"

// telegramTextLimit is the most a message's text may hold, in UTF-16 code
// units, the measure Telegram applies.
const telegramTextLimit = 4096

// PressStore applies a press of an alert's buttons, once per press
// (ErrPressReplayed): "Ignoruj" is Okazje's dismissal, "Kupione" records
// the purchase of the alert's wish; *Store is one.
type PressStore interface {
	DismissPress(ctx context.Context, callbackQueryID string, id int64) error
	BuyPress(ctx context.Context, callbackQueryID string, id, copyID int64) error
}

// TelegramPoller long-polls the bot's updates and acts on presses of an
// alert's "Ignoruj" and "Kupione" buttons. Ohara sits behind Authelia on the LAN, so
// Telegram cannot call a webhook; the app fetches the presses instead.
// Bot's Client must allow a request to run longer than Timeout
// (NewTelegramPollClient). Timeout is how long one getUpdates waits for an
// update, Pause the wait after a failed one; zero takes the defaults.
type TelegramPoller struct {
	Bot           *Telegram
	Store         PressStore
	Logger        *slog.Logger
	Timeout       time.Duration
	Pause         time.Duration
	pressTimeout  time.Duration
	editRetryWait time.Duration
}

// NewTelegramPollClient is an HTTP client for TelegramPoller's Bot: its
// timeout leaves room for the long poll.
func NewTelegramPollClient() *http.Client {
	return &http.Client{Timeout: telegramPollTimeout + 20*time.Second}
}

type telegramUpdate struct {
	UpdateID      int64          `json:"update_id"`
	CallbackQuery *callbackQuery `json:"callback_query"`
}

type callbackQuery struct {
	ID      string           `json:"id"`
	Data    string           `json:"data"`
	Message *telegramMessage `json:"message"`
}

type telegramMessage struct {
	MessageID int64        `json:"message_id"`
	Chat      telegramChat `json:"chat"`
	Text      string       `json:"text"`
}

type telegramChat struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

// Run polls until ctx ends. Handled updates are confirmed by the next
// getUpdates' offset, and once more on the way out; once ctx ends the rest
// of a batch is left unconfirmed for the next start. A press Telegram
// still holds when polling resumes (it keeps a callback only minutes) is
// handled then, and one it sends again is applied only once
// (Store.DismissPress). A failed getUpdates, such as a webhook set on the
// bot, is logged and asked again after Pause.
func (p *TelegramPoller) Run(ctx context.Context) {
	logger := p.logger()
	timeout, pause := p.Timeout, p.Pause
	if timeout <= 0 {
		timeout = telegramPollTimeout
	}
	if pause <= 0 {
		pause = telegramPollPause
	}
	var offset int64
	confirmed, batch := true, telegramBatch
	for ctx.Err() == nil {
		updates, err := recovery.Call(logger, "telegram poll", func() ([]telegramUpdate, error) {
			return p.Bot.updates(ctx, offset, timeout, batch)
		})
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			if errors.Is(err, errReplyTooLarge) && batch > 1 {
				batch = max(batch/2, 1)
				logger.Warn("telegram poll: reply too large, asking for fewer updates", "limit", batch)
				continue
			}
			logger.Warn("telegram poll failed", "err", err)
			if sleepCtx(ctx, pause) != nil {
				break
			}
			continue
		}
		confirmed, batch = true, telegramBatch
		if !p.handle(ctx, updates, &offset, &confirmed) && sleepCtx(ctx, pause) != nil {
			break
		}
	}
	if confirmed {
		return
	}
	confirmCtx, cancel := p.stepContext(ctx)
	defer cancel()
	if _, err := p.Bot.updates(confirmCtx, offset, 0, 1); err != nil {
		logger.Warn("telegram poll: confirm updates", "err", err)
	}
}

func (p *TelegramPoller) logger() *slog.Logger {
	if p.Logger == nil {
		return slog.Default()
	}
	return p.Logger
}

// handle handles updates in order and moves offset past each one handled.
// Once ctx ends it leaves the rest for the next start. A press whose
// dismissal failed for now, such as a listing row locked past the step's
// timeout, stops the batch unanswered and unconfirmed, so the next poll
// gets it again: handle then reports false and Run waits Pause first.
func (p *TelegramPoller) handle(ctx context.Context, updates []telegramUpdate, offset *int64, confirmed *bool) bool {
	for i := range updates {
		if ctx.Err() != nil {
			return true
		}
		u := &updates[i]
		if u.CallbackQuery != nil && !p.press(ctx, u.CallbackQuery) {
			return false
		}
		*offset = max(*offset, u.UpdateID+1)
		*confirmed = false
	}
	return true
}

// stepContext bounds one step of handling a press or confirming updates,
// detached from ctx so shutdown does not cut it short.
func (p *TelegramPoller) stepContext(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := p.pressTimeout
	if timeout <= 0 {
		timeout = telegramPressTimeout
	}
	return context.WithTimeout(context.WithoutCancel(ctx), timeout)
}

// pressOutcome is what a press did: the answer to show and the message
// whose buttons to take off, with the text to replace its own when set, or
// a failure worth trying again.
type pressOutcome struct {
	answer   string
	edit     *telegramMessage
	editText string
	retry    bool
}

// press handles one button press and reports false when it should be
// tried again. It answers first, so the client stops its progress bar
// however long the keyboard edit takes. A press applied before, whose
// answer or edit may not have gone out, gets both again without a second
// dismissal.
func (p *TelegramPoller) press(ctx context.Context, q *callbackQuery) bool {
	logger := p.logger()
	applyCtx, cancel := p.stepContext(ctx)
	out, err := recovery.Call(logger, "telegram press", func() (pressOutcome, error) {
		return p.apply(applyCtx, q), nil
	})
	cancel()
	if err != nil {
		out = pressOutcome{answer: "Nie udało się, spróbuj ponownie"}
	}
	if out.retry {
		return false
	}
	answerCtx, cancel := p.stepContext(ctx)
	err = p.Bot.answerCallback(answerCtx, q.ID, out.answer)
	cancel()
	if err != nil {
		logger.Warn("telegram: answer press", "err", err)
	}
	if out.edit != nil {
		p.editMessage(ctx, out.edit, out.editText)
	}
	return true
}

// editMessage takes the buttons off m, replacing its text with text unless
// that is empty, trying again after a failure worth retrying
// (telegramEditAttempts in all) unless ctx ends. A button left behind still
// works: a press answers again and retries the edit.
func (p *TelegramPoller) editMessage(ctx context.Context, m *telegramMessage, text string) {
	wait := p.editRetryWait
	if wait <= 0 {
		wait = telegramEditRetryWait
	}
	for attempt := 1; ; attempt++ {
		editCtx, cancel := p.stepContext(ctx)
		var err error
		if text != "" {
			err = p.Bot.editText(editCtx, m, text)
		} else {
			err = p.Bot.removeKeyboard(editCtx, m)
		}
		cancel()
		if err == nil {
			return
		}
		if !errors.Is(err, errTelegramUnavailable) || attempt == telegramEditAttempts ||
			sleepCtx(ctx, time.Duration(attempt)*wait) != nil {
			p.logger().Warn("telegram: edit alert", "attempts", attempt, "err", err)
			return
		}
	}
}

// apply acts on a press in the bot's chat: "Ignoruj" dismisses the
// listing it names, "Kupione" records the purchase.
func (p *TelegramPoller) apply(ctx context.Context, q *callbackQuery) pressOutcome {
	logger := p.logger()
	if q.Message == nil || !p.Bot.isChat(q.Message.Chat) {
		logger.Warn("telegram: press from another chat ignored")
		return pressOutcome{answer: "Ten czat nie obsługuje tego przycisku"}
	}
	if id, copyID, ok := boughtListing(q.Data); ok {
		return p.buy(ctx, q, id, copyID)
	}
	id, ok := ignoredListing(q.Data)
	if !ok {
		return pressOutcome{answer: "Nieznany przycisk"}
	}
	switch err := p.Store.DismissPress(ctx, q.ID, id); {
	case errors.Is(err, ErrPressReplayed):
		logger.Info("telegram: press already applied", "observation", id)
	case errors.Is(err, ErrNotFound):
		return pressOutcome{answer: "Nie ma już tego ogłoszenia"}
	case err != nil:
		logger.Warn("telegram: dismiss listing, trying again", "observation", id, "err", err)
		return pressOutcome{retry: true}
	}
	return pressOutcome{answer: "Zignorowano", edit: q.Message}
}

// buy records the purchase a "Kupione" press for listing id and wish
// copyID names. The message then says so and loses its buttons, also when
// the press finds the purchase already recorded, so a message whose edit
// failed is fixed by the next press. A press for a wish no longer on the
// wishlist that it did not buy changes nothing and keeps the message.
func (p *TelegramPoller) buy(ctx context.Context, q *callbackQuery, id, copyID int64) pressOutcome {
	out := pressOutcome{answer: "Zapisano jako kupione", edit: q.Message, editText: withBoughtNote(q.Message.Text)}
	switch err := p.Store.BuyPress(ctx, q.ID, id, copyID); {
	case err == nil:
	case errors.Is(err, ErrAlreadyBought):
		out.answer = "Już zapisano jako kupione"
	case errors.Is(err, ErrNotFound):
		return pressOutcome{answer: "Nie ma już tego ogłoszenia"}
	case errors.Is(err, ErrNotWished):
		return pressOutcome{answer: "Tej pozycji nie ma już na liście życzeń"}
	default:
		p.logger().Warn("telegram: record purchase, trying again", "observation", id, "err", err)
		return pressOutcome{retry: true}
	}
	return out
}

// withBoughtNote is an alert's text with boughtNote after it, cut short
// with "…" where the whole would pass Telegram's limit. A message Telegram
// no longer shows the bot has no text: it gets none and only loses its
// buttons.
func withBoughtNote(text string) string {
	if text == "" {
		return ""
	}
	room := telegramTextLimit - utf16Len(boughtNote)
	if utf16Len(text) > room {
		room -= utf16Len("…")
		var b strings.Builder
		for _, r := range text {
			if n := utf16.RuneLen(r); n > 0 {
				room -= n
			} else {
				room--
			}
			if room < 0 {
				break
			}
			b.WriteRune(r)
		}
		text = b.String() + "…"
	}
	return text + boughtNote
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if l := utf16.RuneLen(r); l > 0 {
			n += l
		} else {
			n++
		}
	}
	return n
}

// ignoredListing reads the observation id from an "Ignoruj" button's data.
func ignoredListing(data string) (int64, bool) {
	return buttonListing(data, ignoreData)
}

// boughtListing reads the observation and copy ids from a "Kupione"
// button's data.
func boughtListing(data string) (int64, int64, bool) {
	rest, ok := strings.CutPrefix(data, boughtData)
	if !ok {
		return 0, 0, false
	}
	listing, wish, ok := strings.Cut(rest, ":")
	if !ok {
		return 0, 0, false
	}
	id, idErr := strconv.ParseInt(listing, 10, 64)
	copyID, copyErr := strconv.ParseInt(wish, 10, 64)
	return id, copyID, idErr == nil && copyErr == nil && id > 0 && copyID > 0
}

func buttonListing(data, prefix string) (int64, bool) {
	rest, ok := strings.CutPrefix(data, prefix)
	if !ok {
		return 0, false
	}
	id, err := strconv.ParseInt(rest, 10, 64)
	return id, err == nil && id > 0
}

// isChat reports whether c is the chat the bot sends alerts to, named by
// its id or as @username.
func (t *Telegram) isChat(c telegramChat) bool {
	if strconv.FormatInt(c.ID, 10) == t.ChatID {
		return true
	}
	return c.Username != "" && strings.EqualFold("@"+c.Username, t.ChatID)
}

// updates is getUpdates for button presses: it confirms every update
// below offset (none when 0) and waits up to timeout for at most limit new
// ones.
func (t *Telegram) updates(ctx context.Context, offset int64, timeout time.Duration, limit int) ([]telegramUpdate, error) {
	form := map[string][]string{
		"timeout":         {strconv.FormatInt(int64(timeout/time.Second), 10)},
		"limit":           {strconv.Itoa(limit)},
		"allowed_updates": {`["callback_query"]`},
	}
	if offset > 0 {
		form["offset"] = []string{strconv.FormatInt(offset, 10)}
	}
	var updates []telegramUpdate
	if err := t.call(ctx, "getUpdates", form, &updates, telegramUpdatesCap); err != nil {
		return nil, err
	}
	return updates, nil
}

func (t *Telegram) answerCallback(ctx context.Context, queryID, text string) error {
	return t.call(ctx, "answerCallbackQuery",
		map[string][]string{"callback_query_id": {queryID}, "text": {text}}, nil, telegramReplyCap)
}

// removeKeyboard takes the buttons off message m.
func (t *Telegram) removeKeyboard(ctx context.Context, m *telegramMessage) error {
	return t.call(ctx, "editMessageReplyMarkup", map[string][]string{
		"chat_id":    {strconv.FormatInt(m.Chat.ID, 10)},
		"message_id": {strconv.FormatInt(m.MessageID, 10)},
	}, nil, telegramReplyCap)
}

// editText replaces message m's text with text and, sending no keyboard,
// takes its buttons off. A message that already reads text, as after a
// replayed press, counts as edited.
func (t *Telegram) editText(ctx context.Context, m *telegramMessage, text string) error {
	err := t.call(ctx, "editMessageText", map[string][]string{
		"chat_id":    {strconv.FormatInt(m.Chat.ID, 10)},
		"message_id": {strconv.FormatInt(m.MessageID, 10)},
		"text":       {text},
	}, nil, telegramReplyCap)
	if err != nil && strings.Contains(err.Error(), "message is not modified") {
		return nil
	}
	return err
}
