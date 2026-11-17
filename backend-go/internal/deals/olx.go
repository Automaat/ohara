package deals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// OLXURL is the undocumented JSON search the OLX web app uses. It may change
// without notice; a failing parse surfaces as a source error in the run.
const OLXURL = "https://www.olx.pl/api/v1/offers/"

// OLX searches olx.pl offers, 50 per query.
type OLX struct {
	Client  *http.Client
	BaseURL string
}

// Name implements Source.
func (OLX) Name() string { return "olx" }

// PerQuery implements Source.
func (OLX) PerQuery() bool { return true }

// Search implements Source.
func (o OLX) Search(ctx context.Context, query string) ([]Listing, error) {
	u := o.BaseURL + "?" + url.Values{
		"query":  {query},
		"offset": {"0"},
		"limit":  {"50"},
	}.Encode()
	body, err := get(ctx, o.Client, u, "application/json")
	if err != nil {
		return nil, fmt.Errorf("olx: %w", err)
	}
	return parseOLX(body)
}

type olxResponse struct {
	Data []json.RawMessage `json:"data"`
}

type olxParam struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

type olxOffer struct {
	ID     int64      `json:"id"`
	URL    string     `json:"url"`
	Title  string     `json:"title"`
	Params []olxParam `json:"params"`
}

type olxPrice struct {
	Value    *float64 `json:"value"`
	Currency string   `json:"currency"`
}

type olxState struct {
	Key string `json:"key"`
}

// parseOLX reads an OLX search response. Offers without a PLN price (free,
// exchange or "price on request") are left out.
func parseOLX(body []byte) ([]Listing, error) {
	var resp olxResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, parseError{fmt.Errorf("olx: decode: %w", err)}
	}
	if resp.Data == nil {
		return nil, parseError{errors.New("olx: response has no data")}
	}
	out := make([]Listing, 0, len(resp.Data))
	for _, raw := range resp.Data {
		var o olxOffer
		if err := json.Unmarshal(raw, &o); err != nil || o.ID == 0 || o.Title == "" {
			continue
		}
		l := Listing{
			Source:     "olx",
			ExternalID: strconv.FormatInt(o.ID, 10),
			Title:      o.Title,
			URL:        o.URL,
			Raw:        raw,
		}
		priced := false
		for _, p := range o.Params {
			switch p.Key {
			case "price":
				var v olxPrice
				if json.Unmarshal(p.Value, &v) == nil && v.Value != nil && v.Currency == "PLN" {
					l.PriceCents, priced = toCents(*v.Value)
				}
			case "state":
				var s olxState
				if json.Unmarshal(p.Value, &s) == nil && (s.Key == "new" || s.Key == "used") {
					l.AcquiredAs = &s.Key
				}
			}
		}
		if priced {
			out = append(out, l)
		}
	}
	return out, nil
}
