package main

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

// unsetSettings clears every variable the hunter and the valuation read, so
// each case starts from the defaults and sets only what it tests.
func unsetSettings(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"OHARA_DEALS_INTERVAL",
		"OHARA_DEALS_DELAY",
		"OHARA_TELEGRAM_BOT_TOKEN",
		"OHARA_TELEGRAM_CHAT_ID",
		"OHARA_VALUATION_INTERVAL",
		"OHARA_VALUATION_WINDOW_DAYS",
		"OHARA_DISCOGS_TOKEN",
	} {
		t.Setenv(key, "")
	}
}

// checkErr fails unless err names want, or is nil when want is empty.
func checkErr(t *testing.T, err error, want string) {
	t.Helper()
	switch {
	case want == "" && err != nil:
		t.Fatalf("err = %v, want none", err)
	case want != "" && err == nil:
		t.Fatalf("err = nil, want one naming %s", want)
	case want != "" && !strings.Contains(err.Error(), want):
		t.Fatalf("err = %v, want one naming %s", err, want)
	}
}

func TestSourceDelay(t *testing.T) {
	for _, tc := range []struct {
		value   string
		want    time.Duration
		wantErr string
	}{
		{value: "", want: 5 * time.Second},
		{value: "1s", want: time.Second},
		{value: "500ms", wantErr: "OHARA_DEALS_DELAY"},
		{value: "x", wantErr: "OHARA_DEALS_DELAY"},
	} {
		t.Run("delay="+tc.value, func(t *testing.T) {
			unsetSettings(t)
			t.Setenv("OHARA_DEALS_DELAY", tc.value)
			got, err := sourceDelay()
			checkErr(t, err, tc.wantErr)
			if got != tc.want {
				t.Fatalf("delay = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNewHunterSettings(t *testing.T) {
	for _, tc := range []struct {
		name         string
		env          map[string]string
		wantErr      string
		wantInterval time.Duration
		wantTelegram bool
	}{
		{name: "defaults"},
		{name: "interval off", env: map[string]string{"OHARA_DEALS_INTERVAL": "0s"}},
		{name: "interval at the floor", env: map[string]string{"OHARA_DEALS_INTERVAL": "10m"}, wantInterval: 10 * time.Minute},
		{name: "interval below the floor", env: map[string]string{"OHARA_DEALS_INTERVAL": "5m"}, wantErr: "OHARA_DEALS_INTERVAL"},
		{name: "negative interval", env: map[string]string{"OHARA_DEALS_INTERVAL": "-1h"}, wantErr: "OHARA_DEALS_INTERVAL"},
		{name: "interval not a duration", env: map[string]string{"OHARA_DEALS_INTERVAL": "abc"}, wantErr: "OHARA_DEALS_INTERVAL"},
		{name: "delay below the floor", env: map[string]string{"OHARA_DEALS_DELAY": "500ms"}, wantErr: "OHARA_DEALS_DELAY"},
		{name: "token without chat", env: map[string]string{"OHARA_TELEGRAM_BOT_TOKEN": "token"}, wantErr: "OHARA_TELEGRAM_CHAT_ID"},
		{name: "chat without token", env: map[string]string{"OHARA_TELEGRAM_CHAT_ID": "42"}, wantErr: "OHARA_TELEGRAM_BOT_TOKEN"},
		{
			name:         "telegram",
			env:          map[string]string{"OHARA_TELEGRAM_BOT_TOKEN": "token", "OHARA_TELEGRAM_CHAT_ID": "42"},
			wantTelegram: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			unsetSettings(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			h, _, err := newHunter(nil, slog.New(slog.DiscardHandler))
			checkErr(t, err, tc.wantErr)
			if err != nil {
				return
			}
			s := h.Status()
			if got := time.Duration(s.IntervalSeconds) * time.Second; got != tc.wantInterval {
				t.Errorf("interval = %v, want %v", got, tc.wantInterval)
			}
			if s.Telegram != tc.wantTelegram {
				t.Errorf("telegram = %v, want %v", s.Telegram, tc.wantTelegram)
			}
		})
	}
}

func TestNewValuationSettings(t *testing.T) {
	for _, tc := range []struct {
		name         string
		env          map[string]string
		wantErr      string
		wantInterval time.Duration
	}{
		{name: "defaults"},
		{name: "interval at the floor", env: map[string]string{"OHARA_VALUATION_INTERVAL": "1h"}, wantInterval: time.Hour},
		{name: "interval below the floor", env: map[string]string{"OHARA_VALUATION_INTERVAL": "30m"}, wantErr: "OHARA_VALUATION_INTERVAL"},
		{name: "negative interval", env: map[string]string{"OHARA_VALUATION_INTERVAL": "-1h"}, wantErr: "OHARA_VALUATION_INTERVAL"},
		{name: "interval not a duration", env: map[string]string{"OHARA_VALUATION_INTERVAL": "abc"}, wantErr: "OHARA_VALUATION_INTERVAL"},
		{name: "one day window", env: map[string]string{"OHARA_VALUATION_WINDOW_DAYS": "1"}},
		{name: "ten year window", env: map[string]string{"OHARA_VALUATION_WINDOW_DAYS": "3650"}},
		{name: "empty window", env: map[string]string{"OHARA_VALUATION_WINDOW_DAYS": "0"}, wantErr: "OHARA_VALUATION_WINDOW_DAYS"},
		{name: "window too long", env: map[string]string{"OHARA_VALUATION_WINDOW_DAYS": "3651"}, wantErr: "OHARA_VALUATION_WINDOW_DAYS"},
		{name: "window not a number", env: map[string]string{"OHARA_VALUATION_WINDOW_DAYS": "x"}, wantErr: "OHARA_VALUATION_WINDOW_DAYS"},
		{name: "delay not a duration", env: map[string]string{"OHARA_DEALS_DELAY": "x"}, wantErr: "OHARA_DEALS_DELAY"},
		{name: "discogs", env: map[string]string{"OHARA_DISCOGS_TOKEN": "token"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			unsetSettings(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			r, _, err := newValuation(nil, slog.New(slog.DiscardHandler))
			checkErr(t, err, tc.wantErr)
			if err != nil {
				return
			}
			if got := time.Duration(r.Status().IntervalSeconds) * time.Second; got != tc.wantInterval {
				t.Errorf("interval = %v, want %v", got, tc.wantInterval)
			}
		})
	}
}
