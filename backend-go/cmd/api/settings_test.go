package main

import (
	"flag"
	"log/slog"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Automaat/ohara/backend-go/internal/deals"
)

// unsetSettings unsets every variable the hunter and the valuation read,
// restoring them when the test ends, so each case starts from the defaults
// and sets only what it tests.
func unsetSettings(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"OHARA_DEALS_INTERVAL",
		"OHARA_DEALS_DELAY",
		"OHARA_TELEGRAM_BOT_TOKEN",
		"OHARA_TELEGRAM_CHAT_ID",
		"OHARA_VALUATION_INTERVAL",
		"OHARA_VALUATION_WINDOW_DAYS",
		"OHARA_PRICE_RETENTION_DAYS",
		"OHARA_DISCOGS_TOKEN",
	} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("unset %s: %v", key, err)
		}
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
		{want: 5 * time.Second},
		{value: "1s", want: time.Second},
		{value: "500ms", wantErr: "OHARA_DEALS_DELAY"},
		{value: "x", wantErr: "OHARA_DEALS_DELAY"},
	} {
		t.Run("delay="+tc.value, func(t *testing.T) {
			unsetSettings(t)
			if tc.value != "" {
				t.Setenv("OHARA_DEALS_DELAY", tc.value)
			}
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
			h, _, err := newHunter(nil, deals.NewPacer(0).Lane(), slog.New(slog.DiscardHandler))
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
		name          string
		env           map[string]string
		wantErr       string
		wantInterval  time.Duration
		wantWindow    time.Duration
		wantRetention time.Duration
	}{
		{name: "defaults", wantWindow: 90 * day},
		{name: "retention at the window", env: map[string]string{"OHARA_PRICE_RETENTION_DAYS": "90"}, wantWindow: 90 * day, wantRetention: 90 * day},
		{name: "retention past the window", env: map[string]string{"OHARA_PRICE_RETENTION_DAYS": "365"}, wantWindow: 90 * day, wantRetention: 365 * day},
		{
			name:          "retention at a shorter window",
			env:           map[string]string{"OHARA_VALUATION_WINDOW_DAYS": "30", "OHARA_PRICE_RETENTION_DAYS": "30"},
			wantWindow:    30 * day,
			wantRetention: 30 * day,
		},
		{name: "retention below the window", env: map[string]string{"OHARA_PRICE_RETENTION_DAYS": "89"}, wantErr: "OHARA_PRICE_RETENTION_DAYS (89) is below OHARA_VALUATION_WINDOW_DAYS (90)"},
		{name: "retention of zero days", env: map[string]string{"OHARA_PRICE_RETENTION_DAYS": "0"}, wantErr: "OHARA_PRICE_RETENTION_DAYS"},
		{name: "negative retention", env: map[string]string{"OHARA_PRICE_RETENTION_DAYS": "-5"}, wantErr: "OHARA_PRICE_RETENTION_DAYS"},
		{name: "retention not a number", env: map[string]string{"OHARA_PRICE_RETENTION_DAYS": "1y"}, wantErr: "OHARA_PRICE_RETENTION_DAYS"},
		{name: "retention too long", env: map[string]string{"OHARA_PRICE_RETENTION_DAYS": "36501"}, wantErr: "OHARA_PRICE_RETENTION_DAYS"},
		{name: "interval at the floor", env: map[string]string{"OHARA_VALUATION_INTERVAL": "1h"}, wantInterval: time.Hour, wantWindow: 90 * day},
		{name: "interval below the floor", env: map[string]string{"OHARA_VALUATION_INTERVAL": "30m"}, wantErr: "OHARA_VALUATION_INTERVAL"},
		{name: "negative interval", env: map[string]string{"OHARA_VALUATION_INTERVAL": "-1h"}, wantErr: "OHARA_VALUATION_INTERVAL"},
		{name: "interval not a duration", env: map[string]string{"OHARA_VALUATION_INTERVAL": "abc"}, wantErr: "OHARA_VALUATION_INTERVAL"},
		{name: "one day window", env: map[string]string{"OHARA_VALUATION_WINDOW_DAYS": "1"}, wantWindow: day},
		{name: "ten year window", env: map[string]string{"OHARA_VALUATION_WINDOW_DAYS": "3650"}, wantWindow: 3650 * day},
		{name: "empty window", env: map[string]string{"OHARA_VALUATION_WINDOW_DAYS": "0"}, wantErr: "OHARA_VALUATION_WINDOW_DAYS"},
		{name: "window too long", env: map[string]string{"OHARA_VALUATION_WINDOW_DAYS": "3651"}, wantErr: "OHARA_VALUATION_WINDOW_DAYS"},
		{name: "window not a number", env: map[string]string{"OHARA_VALUATION_WINDOW_DAYS": "x"}, wantErr: "OHARA_VALUATION_WINDOW_DAYS"},
		{name: "delay not a duration", env: map[string]string{"OHARA_DEALS_DELAY": "x"}, wantErr: "OHARA_DEALS_DELAY"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			unsetSettings(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			r, _, err := newValuation(nil, deals.NewPacer(0).Lane(), slog.New(slog.DiscardHandler))
			checkErr(t, err, tc.wantErr)
			if err != nil {
				return
			}
			if got := time.Duration(r.Status().IntervalSeconds) * time.Second; got != tc.wantInterval {
				t.Errorf("interval = %v, want %v", got, tc.wantInterval)
			}
			if got := r.Window(); got != tc.wantWindow {
				t.Errorf("window = %v, want %v", got, tc.wantWindow)
			}
			if got := r.Retention(); got != tc.wantRetention {
				t.Errorf("retention = %v, want %v", got, tc.wantRetention)
			}
		})
	}
}

const day = 24 * time.Hour

// discogsChild marks the process TestNewValuationDiscogs starts.
const discogsChild = "OHARA_TEST_DISCOGS_CHILD"

// TestNewValuationDiscogs builds the valuation with a Discogs token in a
// child process: the Discogs pricer registers the nbp source in the
// process-wide metrics, which the source checks of the other tests reject.
func TestNewValuationDiscogs(t *testing.T) {
	if os.Getenv(discogsChild) == "1" {
		if _, _, err := newValuation(nil, deals.NewPacer(0).Lane(), slog.New(slog.DiscardHandler)); err != nil {
			t.Fatalf("newValuation: %v", err)
		}
		if got := metricSources(t, "ohara_source_"); !slices.Contains(got, "nbp") {
			t.Fatalf("sources with metrics = %v, want nbp for the Discogs pricer", got)
		}
		return
	}
	unsetSettings(t)
	args := []string{"-test.run=^TestNewValuationDiscogs$"}
	// A coverage run passes its counter directory on, so the child's
	// coverage counts.
	if f := flag.Lookup("test.gocoverdir"); f != nil && f.Value.String() != "" {
		args = append(args, "-test.gocoverdir="+f.Value.String())
	}
	cmd := exec.CommandContext(t.Context(), os.Args[0], args...)
	cmd.Env = append(os.Environ(), discogsChild+"=1", "OHARA_DISCOGS_TOKEN=token")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child: %v\n%s", err, out)
	}
}
