package valuation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Automaat/ohara/backend-go/internal/discogs"
	"github.com/Automaat/ohara/backend-go/internal/fetch"
	"github.com/Automaat/ohara/backend-go/internal/metrics"
)

// Discogs prices of an album edition in price_observation: the cheapest
// copy for sale (the floor), the suggested price of a Very Good Plus copy
// (the middle) and of a Mint one (the ceiling).
const (
	SourceDiscogsLowest = "discogs_lowest"
	SourceDiscogsVGPlus = "discogs_vg_plus"
	SourceDiscogsMint   = "discogs_mint"
)

// SourceDiscogs names Discogs as the source of an estimate built from any
// of the three Discogs prices.
const SourceDiscogs = "discogs"

// Discogs media condition grades the suggestions are read for.
const (
	gradeVGPlus = "Very Good Plus (VG+)"
	gradeMint   = "Mint (M)"
)

// ReleasePrice is a Discogs release priced in grosze; a nil price is
// unknown (nothing for sale, no suggestion).
type ReleasePrice struct {
	LowestCents *int64
	NumForSale  int
	VGPlusCents *int64
	MintCents   *int64
}

// ReleasePricer prices a Discogs release in PLN. A partly priced release
// comes back with the error of the part that failed.
type ReleasePricer interface {
	Name() string
	Price(ctx context.Context, releaseID int64) (ReleasePrice, error)
}

// Marketplace is the part of the Discogs API the pricer reads;
// *discogs.Client implements it.
type Marketplace interface {
	Stats(ctx context.Context, id int64, currency string) (discogs.Stats, error)
	PriceSuggestions(ctx context.Context, id int64) (discogs.Suggestions, error)
}

// Rates converts a currency to PLN: the złoty value of one unit.
type Rates interface {
	PLN(ctx context.Context, currency string) (float64, error)
}

// Discogs prices releases on the Discogs marketplace. Discogs sells in no
// złoty, so stats are asked in EUR and every amount is converted at the
// NBP rate. Suggestions need the token owner's seller settings; switch
// them off when the account has none.
type Discogs struct {
	Market      Marketplace
	Rates       Rates
	Suggestions bool
}

// statsCurrency is the currency the marketplace stats are asked in.
const statsCurrency = "EUR"

// Name implements ReleasePricer.
func (Discogs) Name() string { return SourceDiscogs }

// Price implements ReleasePricer.
func (d Discogs) Price(ctx context.Context, id int64) (ReleasePrice, error) {
	var p ReleasePrice
	stats, err := d.Market.Stats(ctx, id, statsCurrency)
	if err != nil {
		return p, err
	}
	p.NumForSale = stats.NumForSale
	var errs []error
	set := func(dst **int64, m discogs.Money) {
		cents, err := d.pln(ctx, m)
		if err != nil {
			errs = append(errs, err)
			return
		}
		if cents > 0 {
			*dst = &cents
		}
	}
	if stats.LowestPrice != nil && stats.NumForSale > 0 {
		set(&p.LowestCents, *stats.LowestPrice)
	}
	if d.Suggestions {
		s, err := d.Market.PriceSuggestions(ctx, id)
		if err != nil {
			return p, errors.Join(append(errs, err)...)
		}
		if m, ok := s[gradeVGPlus]; ok {
			set(&p.VGPlusCents, m)
		}
		if m, ok := s[gradeMint]; ok {
			set(&p.MintCents, m)
		}
	}
	return p, errors.Join(errs...)
}

// pln converts m to grosze; 0 is unknown: a non-positive amount or one
// past the price cap.
func (d Discogs) pln(ctx context.Context, m discogs.Money) (int64, error) {
	if m.Value <= 0 {
		return 0, nil
	}
	rate, err := d.Rates.PLN(ctx, m.Currency)
	if err != nil {
		return 0, err
	}
	cents := int64(math.Round(m.Value * rate * 100))
	if cents <= 0 || cents > maxPriceCents {
		return 0, nil
	}
	return cents, nil
}

// SourceNBP names the National Bank of Poland rates in the source health
// metrics.
const SourceNBP = "nbp"

// NBPURL is the National Bank of Poland exchange rate API.
const NBPURL = "https://api.nbp.pl"

// rateTTL is how long a fetched rate is reused; NBP publishes table A once
// a working day.
const rateTTL = 12 * time.Hour

// maxRateBody caps a rate response, which is a few hundred bytes.
const maxRateBody = 1 << 16

// NBP reads mid rates from the National Bank of Poland's table A and keeps
// each for rateTTL.
type NBP struct {
	Client  *http.Client
	BaseURL string

	mu    sync.Mutex
	rates map[string]cachedRate
}

type cachedRate struct {
	rate float64
	at   time.Time
}

// PLN implements Rates.
func (n *NBP) PLN(ctx context.Context, currency string) (float64, error) {
	code := strings.ToUpper(strings.TrimSpace(currency))
	if code == "PLN" {
		return 1, nil
	}
	if len(code) != 3 {
		return 0, fmt.Errorf("nbp: unknown currency %q", currency)
	}
	n.mu.Lock()
	c, ok := n.rates[code]
	n.mu.Unlock()
	if ok && time.Since(c.at) < rateTTL {
		return c.rate, nil
	}
	rate, err := n.fetch(ctx, code)
	if err != nil {
		return 0, err
	}
	n.mu.Lock()
	if n.rates == nil {
		n.rates = map[string]cachedRate{}
	}
	n.rates[code] = cachedRate{rate: rate, at: time.Now()}
	n.mu.Unlock()
	return rate, nil
}

func (n *NBP) fetch(ctx context.Context, code string) (float64, error) {
	base := n.BaseURL
	if base == "" {
		base = NBPURL
	}
	u := base + "/api/exchangerates/rates/a/" + url.PathEscape(strings.ToLower(code)) + "/?format=json"
	resp, err := fetch.Do(ctx, n.Client, fetch.Request{
		Source:  SourceNBP,
		URL:     u,
		Header:  http.Header{"Accept": {"application/json"}},
		MaxBody: maxRateBody,
	})
	if err != nil {
		metrics.ObserveSource(SourceNBP, fetch.Outcome(err))
		return 0, fmt.Errorf("nbp: %s rate: %w", code, err)
	}
	var r struct {
		Rates []struct {
			Mid float64 `json:"mid"`
		} `json:"rates"`
	}
	if err := json.NewDecoder(bytes.NewReader(resp.Body)).Decode(&r); err != nil {
		metrics.ObserveSource(SourceNBP, metrics.SourceParseError)
		return 0, fmt.Errorf("nbp: %s rate: parse: %w", code, err)
	}
	if len(r.Rates) == 0 || r.Rates[len(r.Rates)-1].Mid <= 0 {
		metrics.ObserveSource(SourceNBP, metrics.SourceParseError)
		return 0, fmt.Errorf("nbp: %s rate: no rate", code)
	}
	metrics.ObserveSource(SourceNBP, metrics.SourceOK)
	return r.Rates[len(r.Rates)-1].Mid, nil
}
