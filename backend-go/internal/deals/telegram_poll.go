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

// telegramPressTimeout bounds handling one press, which outlives the
// poller's context so a press read before shutdown is still answered.
const telegramPressTimeout = 10 * time.Second

// Dismisser dismisses a deal listing as Okazje's "Ignoruj" does; *Store is
// one.
type Dismisser interface {
	Dismiss(ctx context.Context, id int64) error
}

// TelegramPoller long-polls the bot's updates and acts on presses of an
// alert's "Ignoruj" button. Ohara sits behind Authelia on the LAN, so
// Telegram cannot call a webhook; the app fetches the presses instead.
// Bot's Client must allow a request to run longer than Timeout
// (NewTelegramPollClient). Timeout is how long one getUpdates waits for an
// update, Pause the wait after a failed one; zero takes the defaults.
type TelegramPoller struct {
	Bot     *Telegram
	Store   Dismisser
	Logger  *slog.Logger
	Timeout time.Duration
	Pause   time.Duration
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

// Run polls until ctx ends. Each batch of updates is confirmed by the next
// getUpdates' offset, and once more on the way out, so a restart neither
// repeats a press nor loses one Telegram still holds (up to 24 hours). A
// failed getUpdates, such as a webhook set on the bot, is logged and asked
// again after Pause.
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
	confirmed := true
	for ctx.Err() == nil {
		updates, err := recovery.Call(logger, "telegram poll", func() ([]telegramUpdate, error) {
			return p.Bot.updates(ctx, offset, timeout)
		})
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			logger.Warn("telegram poll failed", "err", err)
			if sleepCtx(ctx, pause) != nil {
				break
			}
			continue
		}
		confirmed = true
		for i := range updates {
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
	confirmCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), telegramPressTimeout)
	defer cancel()
	if _, err := p.Bot.updates(confirmCtx, offset, 0); err != nil {
		logger.Warn("telegram poll: confirm updates", "err", err)
	}
}

func (p *TelegramPoller) logger() *slog.Logger {
	if p.Logger == nil {
		return slog.Default()
	}
	return p.Logger
}

// press handles one button press and always answers it, so the client
// stops its progress bar.
func (p *TelegramPoller) press(ctx context.Context, q *callbackQuery) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), telegramPressTimeout)
	defer cancel()
	logger := p.logger()
	answer, err := recovery.Call(logger, "telegram press", func() (string, error) {
		return p.dismiss(ctx, q), nil
	})
	if err != nil {
		answer = "Nie udało się zignorować, spróbuj ponownie"
	}
	if err := p.Bot.answerCallback(ctx, q.ID, answer); err != nil {
		logger.Warn("telegram: answer press", "err", err)
	}
}

// dismiss dismisses the listing a press in the bot's chat names, removes
// the message's button and returns the answer to show.
func (p *TelegramPoller) dismiss(ctx context.Context, q *callbackQuery) string {
	logger := p.logger()
	if q.Message == nil || !p.Bot.isChat(q.Message.Chat) {
		logger.Warn("telegram: press from another chat ignored")
		return "Ten czat nie obsługuje tego przycisku"
	}
	id, ok := ignoredListing(q.Data)
	if !ok {
		return "Nieznany przycisk"
	}
	switch err := p.Store.Dismiss(ctx, id); {
	case errors.Is(err, ErrNotFound):
		return "Nie ma już tego ogłoszenia"
	case err != nil:
		logger.Warn("telegram: dismiss listing", "observation", id, "err", err)
		return "Nie udało się zignorować, spróbuj ponownie"
	}
	if err := p.Bot.removeKeyboard(ctx, q.Message); err != nil {
		logger.Warn("telegram: remove button", "observation", id, "err", err)
	}
	return "Zignorowano"
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
// below offset (none when 0) and waits up to timeout for new ones.
func (t *Telegram) updates(ctx context.Context, offset int64, timeout time.Duration) ([]telegramUpdate, error) {
	form := map[string][]string{
		"timeout":         {strconv.FormatInt(int64(timeout/time.Second), 10)},
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
