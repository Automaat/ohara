package deals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// TelegramURL is the Bot API base.
const TelegramURL = "https://api.telegram.org"

// ignoreData starts the callback data of an alert's "Ignoruj" button; the
// listing's observation id follows, which keeps it far under Telegram's 64
// bytes.
const ignoreData = "ignore:"

// telegramReplyCap bounds a Bot API answer; getUpdates gets more, since
// each update carries the whole alert message.
const (
	telegramReplyCap   = 1 << 16
	telegramUpdatesCap = 1 << 20
)

// errReplyTooLarge reports a Bot API answer over its cap.
var errReplyTooLarge = errors.New("telegram: reply too large")

// errTelegramUnavailable marks a failure worth trying again: no answer, a
// 5xx or a 429.
var errTelegramUnavailable = errors.New("telegram: unavailable")

// transientError is a Bot API failure that also matches
// errTelegramUnavailable, keeping its own text.
type transientError struct{ err error }

func (e *transientError) Error() string { return e.err.Error() }

func (e *transientError) Unwrap() []error { return []error{e.err, errTelegramUnavailable} }

// Notifier delivers one alert message.
type Notifier interface {
	Send(ctx context.Context, text string) error
}

// AlertNotifier is a Notifier that also puts an "Ignoruj" button for the
// listing observationID under the message. The hunter uses it when the
// notifier has it.
type AlertNotifier interface {
	Notifier
	SendAlert(ctx context.Context, text string, observationID int64) error
}

// Telegram sends messages through a bot to one chat.
type Telegram struct {
	Client  *http.Client
	BaseURL string
	Token   string
	ChatID  string
}

type telegramReply struct {
	OK          bool            `json:"ok"`
	Description string          `json:"description"`
	Result      json.RawMessage `json:"result"`
}

type inlineKeyboard struct {
	InlineKeyboard [][]inlineButton `json:"inline_keyboard"`
}

type inlineButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data"`
}

// Send implements Notifier. The bot token is part of the request URL, so
// every error has the token cut out before it is returned.
func (t *Telegram) Send(ctx context.Context, text string) error {
	return t.call(ctx, "sendMessage", url.Values{"chat_id": {t.ChatID}, "text": {text}}, nil, telegramReplyCap)
}

// SendAlert implements AlertNotifier: Send with an inline "Ignoruj"
// button whose press TelegramPoller turns into a dismissal of the listing.
func (t *Telegram) SendAlert(ctx context.Context, text string, observationID int64) error {
	markup, err := json.Marshal(inlineKeyboard{InlineKeyboard: [][]inlineButton{{
		{Text: "Ignoruj", CallbackData: ignoreData + strconv.FormatInt(observationID, 10)},
	}}})
	if err != nil {
		return fmt.Errorf("telegram: keyboard: %w", err)
	}
	form := url.Values{"chat_id": {t.ChatID}, "text": {text}, "reply_markup": {string(markup)}}
	return t.call(ctx, "sendMessage", form, nil, telegramReplyCap)
}

// call posts form to the Bot API method and decodes the answer's result
// into result unless it is nil. Its errors never carry the token.
func (t *Telegram) call(ctx context.Context, method string, form url.Values, result any, limit int64) error {
	err := t.post(ctx, method, form, result, limit)
	if err == nil || t.Token == "" {
		return err
	}
	redacted := &redactedError{msg: strings.ReplaceAll(err.Error(), t.Token, "<token>")}
	for _, kind := range []error{errReplyTooLarge, errTelegramUnavailable} {
		if errors.Is(err, kind) {
			redacted.kind = kind
		}
	}
	return redacted
}

// redactedError is an error with the bot token cut out of its text. It
// unwraps only to a sentinel, errReplyTooLarge or errTelegramUnavailable,
// never to the original error, whose text holds the token.
type redactedError struct {
	msg  string
	kind error
}

func (e *redactedError) Error() string { return e.msg }

func (e *redactedError) Unwrap() error { return e.kind }

func (t *Telegram) post(ctx context.Context, method string, form url.Values, result any, limit int64) error {
	endpoint := strings.TrimSuffix(t.BaseURL, "/") + "/bot" + t.Token + "/" + method
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return errors.New("telegram: invalid request")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := t.Client.Do(req)
	if err != nil {
		if uerr, ok := errors.AsType[*url.Error](err); ok {
			err = uerr.Err
		}
		return &transientError{fmt.Errorf("telegram: %w", err)}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return fmt.Errorf("telegram: read reply: %w", err)
	}
	if int64(len(body)) > limit {
		return fmt.Errorf("%w: %s over %d bytes", errReplyTooLarge, method, limit)
	}
	var reply telegramReply
	if json.Unmarshal(body, &reply) != nil || !reply.OK || resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("telegram: status %d: %s", resp.StatusCode, reply.Description)
		if resp.StatusCode >= http.StatusInternalServerError || resp.StatusCode == http.StatusTooManyRequests {
			return &transientError{err}
		}
		return err
	}
	if result != nil {
		if err := json.Unmarshal(reply.Result, result); err != nil {
			return fmt.Errorf("telegram: %s result: %w", method, err)
		}
	}
	return nil
}
