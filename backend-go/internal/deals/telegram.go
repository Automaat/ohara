package deals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// TelegramURL is the Bot API base.
const TelegramURL = "https://api.telegram.org"

// Notifier delivers one alert message.
type Notifier interface {
	Send(ctx context.Context, text string) error
}

// Telegram sends messages through a bot to one chat.
type Telegram struct {
	Client  *http.Client
	BaseURL string
	Token   string
	ChatID  string
}

type telegramReply struct {
	OK          bool   `json:"ok"`
	Description string `json:"description"`
}

// Send implements Notifier. The bot token is part of the request URL, so
// every error has the token cut out before it is returned.
func (t *Telegram) Send(ctx context.Context, text string) error {
	if err := t.send(ctx, text); err != nil {
		if t.Token == "" {
			return err
		}
		return errors.New(strings.ReplaceAll(err.Error(), t.Token, "<token>"))
	}
	return nil
}

func (t *Telegram) send(ctx context.Context, text string) error {
	form := url.Values{"chat_id": {t.ChatID}, "text": {text}}
	endpoint := strings.TrimSuffix(t.BaseURL, "/") + "/bot" + t.Token + "/sendMessage"
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
		return fmt.Errorf("telegram: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return fmt.Errorf("telegram: read reply: %w", err)
	}
	var reply telegramReply
	if json.Unmarshal(body, &reply) != nil || !reply.OK || resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram: status %d: %s", resp.StatusCode, reply.Description)
	}
	return nil
}
