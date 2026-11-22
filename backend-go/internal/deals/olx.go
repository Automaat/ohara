package deals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// OLXURL is the undocumented JSON search the OLX web app uses. It may change
// without notice; a failing parse surfaces as a source error in the run.
const OLXURL = "https://www.olx.pl/api/v1/offers/"

// olxLimit is how many offers one OLX query asks for; the source reads
// only that first page.
const olxLimit = 50

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
	page, err := o.SearchPage(ctx, query)
	return page.Listings, err
}

// SearchPage implements Pager: an answer with fewer offers than asked for
// is complete, unless an offer's ID could not be read, since that offer
// may be a stored listing still offered.
func (o OLX) SearchPage(ctx context.Context, query string) (Page, error) {
	u := o.BaseURL + "?" + url.Values{
		"query":  {query},
		"offset": {"0"},
		"limit":  {strconv.Itoa(olxLimit)},
	}.Encode()
	body, err := get(ctx, o.Client, o.Name(), u, "application/json")
	if err != nil {
		return Page{}, fmt.Errorf("olx: %w", err)
	}
	listings, seen, offers, err := parseOLXPage(body)
	if err != nil {
		return Page{}, err
	}
	for i := range listings {
		listings[i].Query = query
	}
	return Page{Listings: listings, Seen: seen, Complete: offers < olxLimit && len(seen) == offers}, nil
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
	Photos []olxPhoto `json:"photos"`
	Params []olxParam `json:"params"`
}

type olxPhoto struct {
	Link string `json:"link"`
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
	listings, _, _, err := parseOLXPage(body)
	return listings, err
}

// parseOLXPage reads an OLX search response into its priced listings, the
// external IDs of every offer it held, priced or not, and the number of
// offers.
func parseOLXPage(body []byte) ([]Listing, []string, int, error) {
	var resp olxResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, nil, 0, parseError{fmt.Errorf("olx: decode: %w", err)}
	}
	if resp.Data == nil {
		return nil, nil, 0, parseError{errors.New("olx: response has no data")}
	}
	seen := make([]string, 0, len(resp.Data))
	out := make([]Listing, 0, len(resp.Data))
	for _, raw := range resp.Data {
		var o olxOffer
		err := json.Unmarshal(raw, &o)
		if id, ok := olxOfferID(raw); ok {
			seen = append(seen, id)
		}
		if err != nil || o.ID == 0 || o.Title == "" {
			continue
		}
		l := Listing{
			Source:     "olx",
			ExternalID: strconv.FormatInt(o.ID, 10),
			Title:      o.Title,
			URL:        o.URL,
			Raw:        raw,
		}
		for _, photo := range o.Photos {
			// OLX templates require a size and an optional quality suffix.
			candidate := strings.NewReplacer("{width}", "400", "{height}", "300", "{fill}", "").Replace(photo.Link)
			if l.PhotoURL = imageURL(candidate); l.PhotoURL != "" {
				break
			}
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
	return out, seen, len(resp.Data), nil
}

// olxOfferID reads an offer's ID on its own, so an offer whose other
// fields do not decode still counts as seen.
func olxOfferID(raw json.RawMessage) (string, bool) {
	var o struct {
		ID int64 `json:"id"`
	}
	if json.Unmarshal(raw, &o) != nil || o.ID == 0 {
		return "", false
	}
	return strconv.FormatInt(o.ID, 10), true
}
