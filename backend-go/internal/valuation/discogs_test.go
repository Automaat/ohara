package valuation

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Automaat/ohara/backend-go/internal/discogs"
)

type fakeMarket struct {
	stats       discogs.Stats
	statsErr    error
	suggestions discogs.Suggestions
	suggestErr  error
	currency    string
}

func (f *fakeMarket) Stats(_ context.Context, _ int64, currency string) (discogs.Stats, error) {
	f.currency = currency
	return f.stats, f.statsErr
}

func (f *fakeMarket) PriceSuggestions(context.Context, int64) (discogs.Suggestions, error) {
	return f.suggestions, f.suggestErr
}

type fixedRates map[string]float64

func (r fixedRates) PLN(_ context.Context, currency string) (float64, error) {
	rate, ok := r[currency]
	if !ok {
		return 0, errors.New("nbp: no rate for " + currency)
	}
	return rate, nil
}

func cents(p *int64) int64 {
	if p == nil {
		return -1
	}
	return *p
}

func TestDiscogsPricesInPLN(t *testing.T) {
	rates := fixedRates{"EUR": 4.25, "USD": 3.9}
	tests := []struct {
		name                 string
		market               fakeMarket
		suggestions          bool
		lowest, vgPlus, mint int64
		forSale              int
		wantErr              bool
	}{
		{
			name: "stats and suggestions",
			market: fakeMarket{
				stats: discogs.Stats{LowestPrice: &discogs.Money{Currency: "EUR", Value: 12.5}, NumForSale: 34},
				suggestions: discogs.Suggestions{
					gradeMint:   {Currency: "USD", Value: 30},
					gradeVGPlus: {Currency: "USD", Value: 18.2},
					"Good (G)":  {Currency: "USD", Value: 4},
				},
			},
			suggestions: true, lowest: 5313, vgPlus: 7098, mint: 11700, forSale: 34,
		},
		{
			name:   "nothing for sale",
			market: fakeMarket{stats: discogs.Stats{LowestPrice: nil, NumForSale: 0}},
			lowest: -1, vgPlus: -1, mint: -1,
		},
		{
			name: "suggestions switched off",
			market: fakeMarket{
				stats:      discogs.Stats{LowestPrice: &discogs.Money{Currency: "EUR", Value: 2}, NumForSale: 1},
				suggestErr: errors.New("must not be asked"),
			},
			lowest: 850, vgPlus: -1, mint: -1, forSale: 1,
		},
		{
			name: "suggestions without seller settings keep the lowest price",
			market: fakeMarket{
				stats:      discogs.Stats{LowestPrice: &discogs.Money{Currency: "EUR", Value: 2}, NumForSale: 1},
				suggestErr: discogs.StatusError{Code: http.StatusUnprocessableEntity},
			},
			suggestions: true, lowest: 850, vgPlus: -1, mint: -1, forSale: 1, wantErr: true,
		},
		{
			name: "unknown currency",
			market: fakeMarket{
				stats:       discogs.Stats{LowestPrice: &discogs.Money{Currency: "EUR", Value: 2}, NumForSale: 1},
				suggestions: discogs.Suggestions{gradeMint: {Currency: "XYZ", Value: 3}},
			},
			suggestions: true, lowest: 850, vgPlus: -1, mint: -1, forSale: 1, wantErr: true,
		},
		{
			name:   "stats down",
			market: fakeMarket{statsErr: errors.New("discogs: unexpected status 503")},
			lowest: -1, vgPlus: -1, mint: -1, wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := tt.market
			d := Discogs{Market: &m, Rates: rates, Suggestions: tt.suggestions}
			p, err := d.Price(context.Background(), 1873013)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, want error %v", err, tt.wantErr)
			}
			got := []int64{cents(p.LowestCents), cents(p.VGPlusCents), cents(p.MintCents), int64(p.NumForSale)}
			want := []int64{tt.lowest, tt.vgPlus, tt.mint, int64(tt.forSale)}
			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("price = %v, want %v", got, want)
				}
			}
			if tt.market.statsErr == nil && m.currency != "EUR" {
				t.Fatalf("stats asked in %q, want EUR", m.currency)
			}
		})
	}
}

func TestNBPRates(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/api/exchangerates/rates/a/eur/":
			_, _ = w.Write([]byte(`{"table":"A","currency":"euro","code":"EUR","rates":[{"no":"222/A/NBP/2026","effectiveDate":"2026-11-17","mid":4.2512}]}`))
		case "/api/exchangerates/rates/a/usd/":
			_, _ = w.Write([]byte(`{"rates":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	n := &NBP{Client: srv.Client(), BaseURL: srv.URL}
	ctx := context.Background()
	for range 2 {
		rate, err := n.PLN(ctx, "eur")
		if err != nil || rate != 4.2512 {
			t.Fatalf("EUR = %v, %v", rate, err)
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("rate fetched %d times, want once", hits.Load())
	}
	if rate, err := n.PLN(ctx, "PLN"); err != nil || rate != 1 || hits.Load() != 1 {
		t.Fatalf("PLN = %v, %v after %d requests", rate, err, hits.Load())
	}
	if _, err := n.PLN(ctx, "USD"); err == nil {
		t.Fatal("empty rate table accepted")
	}
	if _, err := n.PLN(ctx, "GBP"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("missing currency err = %v", err)
	}
}
