package deals

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

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

// telegramBatch is how many updates one getUpdates asks for. Each carries
// its whole alert message, up to 4096 characters, so a batch of 20 stays
// well under telegramUpdatesCap; a reply over it halves the batch.
const telegramBatch = 20

// PressStore applies a press of an alert's "Ignoruj" button: Okazje's
// dismissal, once per press (ErrPressReplayed); *Store is one.
type PressStore interface {
	DismissPress(ctx context.Context, callbackQueryID string, id int64) error
}

// TelegramPoller long-polls the bot's updates and acts on presses of an
// alert's "Ignoruj" button. Ohara sits behind Authelia on the LAN, so
// Telegram cannot call a webhook; the app fetches the presses instead.
// Bot's Client must allow a request to run longer than Timeout
// (NewTelegramPollClient). Timeout is how long one getUpdates waits for an
// update, Pause the wait after a failed one; zero takes the defaults.
type TelegramPoller struct {
	Bot          *Telegram
	Store        PressStore
	Logger       *slog.Logger
	Timeout      time.Duration
	Pause        time.Duration
	pressTimeout time.Duration
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
		for i := range updates {
			if ctx.Err() != nil {
				break
			}
			u := &updates[i]
			offset = max(offset, u.UpdateID+1)
			confirmed = false
			if u.CallbackQuery != nil {
				p.press(ctx, u.CallbackQuery)
			}
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

// stepContext bounds one step of handling a press or confirming updates,
// detached from ctx so shutdown does not cut it short.
func (p *TelegramPoller) stepContext(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := p.pressTimeout
	if timeout <= 0 {
		timeout = telegramPressTimeout
	}
	return context.WithTimeout(context.WithoutCancel(ctx), timeout)
}

// pressOutcome is what a press did: the answer to show, the message whose
// button to take off, or a replay of a press already applied.
type pressOutcome struct {
	answer string
	edit   *telegramMessage
	replay bool
}

// press handles one button press. It answers first, so the client stops
// its progress bar however long the keyboard edit takes; a replayed press
// was answered when it was applied and gets nothing more.
func (p *TelegramPoller) press(ctx context.Context, q *callbackQuery) {
	logger := p.logger()
	applyCtx, cancel := p.stepContext(ctx)
	out, err := recovery.Call(logger, "telegram press", func() (pressOutcome, error) {
		return p.apply(applyCtx, q), nil
	})
	cancel()
	if err != nil {
		out = pressOutcome{answer: "Nie udało się zignorować, spróbuj ponownie"}
	}
	if out.replay {
		return
	}
	answerCtx, cancel := p.stepContext(ctx)
	err = p.Bot.answerCallback(answerCtx, q.ID, out.answer)
	cancel()
	if err != nil {
		logger.Warn("telegram: answer press", "err", err)
	}
	if out.edit == nil {
		return
	}
	editCtx, cancel := p.stepContext(ctx)
	defer cancel()
	if err := p.Bot.removeKeyboard(editCtx, out.edit); err != nil {
		logger.Warn("telegram: remove button", "err", err)
	}
}

// apply dismisses the listing a press in the bot's chat names.
func (p *TelegramPoller) apply(ctx context.Context, q *callbackQuery) pressOutcome {
	logger := p.logger()
	if q.Message == nil || !p.Bot.isChat(q.Message.Chat) {
		logger.Warn("telegram: press from another chat ignored")
		return pressOutcome{answer: "Ten czat nie obsługuje tego przycisku"}
	}
	id, ok := ignoredListing(q.Data)
	if !ok {
		return pressOutcome{answer: "Nieznany przycisk"}
	}
	switch err := p.Store.DismissPress(ctx, q.ID, id); {
	case errors.Is(err, ErrPressReplayed):
		logger.Info("telegram: press already applied", "observation", id)
		return pressOutcome{replay: true}
	case errors.Is(err, ErrNotFound):
		return pressOutcome{answer: "Nie ma już tego ogłoszenia"}
	case err != nil:
		logger.Warn("telegram: dismiss listing", "observation", id, "err", err)
		return pressOutcome{answer: "Nie udało się zignorować, spróbuj ponownie"}
	}
	return pressOutcome{answer: "Zignorowano", edit: q.Message}
}

// ignoredListing reads the observation id from an "Ignoruj" button's data.
func ignoredListing(data string) (int64, bool) {
	rest, ok := strings.CutPrefix(data, ignoreData)
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

// removeKeyboard takes the "Ignoruj" button off message m.
func (t *Telegram) removeKeyboard(ctx context.Context, m *telegramMessage) error {
	return t.call(ctx, "editMessageReplyMarkup", map[string][]string{
		"chat_id":    {strconv.FormatInt(m.Chat.ID, 10)},
		"message_id": {strconv.FormatInt(m.MessageID, 10)},
	}, nil, telegramReplyCap)
}
