package deals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// wooStorePerPage is how many products one WooCommerce Store API query asks
// for; a source reads only that first page.
const wooStorePerPage = 50

// wooStore reads the public WooCommerce Store API (`/wp-json/wc/store/v1/products`)
// of one shop. It sends no `orderby`, which shops often disallow in robots.txt.
type wooStore struct {
	client   *http.Client
	source   string
	endpoint string
}

// search asks the shop for products matching query and returns the ones
// for sale in PLN.
func (w wooStore) search(ctx context.Context, query string) ([]Listing, error) {
	u := w.endpoint + "?" + url.Values{
		"search":   {query},
		"per_page": {strconv.Itoa(wooStorePerPage)},
	}.Encode()
	body, err := get(ctx, w.client, w.source, u, "application/json")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", w.source, err)
	}
	return parseWooStore(w.source, body)
}

type wooProduct struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Permalink string    `json:"permalink"`
	Prices    wooPrices `json:"prices"`
	Images    []struct {
		Src string `json:"src"`
	} `json:"images"`
	Stock struct {
		Class string `json:"class"`
	} `json:"stock_availability"`
	Categories []struct {
		Slug string `json:"slug"`
	} `json:"categories"`
}

type wooPrices struct {
	Price        string `json:"price"`
	CurrencyCode string `json:"currency_code"`
	MinorUnit    *int   `json:"currency_minor_unit"`
}

// wooRaw is the part of a product kept with its listing.
type wooRaw struct {
	ID         int64    `json:"id"`
	Name       string   `json:"name"`
	Permalink  string   `json:"permalink"`
	Price      string   `json:"price"`
	Currency   string   `json:"currency"`
	Stock      string   `json:"stock"`
	Categories []string `json:"categories,omitempty"`
}

// wooOutOfStock is the stock class of a product the shop cannot sell now;
// "in-stock" and "available-on-backorder" (preorders) are for sale.
const wooOutOfStock = "out-of-stock"

// parseWooStore reads a Store API products answer for source. Every product
// becomes a new-condition listing, except those out of stock or priced in
// another currency than PLN. A body that is not a product array, or a
// non-empty array holding no product (an object with an ID), is a parse
// error.
func parseWooStore(source string, body []byte) ([]Listing, error) {
	var raw []json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, parseError{fmt.Errorf("%s: decode: %w", source, err)}
	}
	if raw == nil {
		return nil, parseError{errors.New(source + ": response is not a product list")}
	}
	out := make([]Listing, 0, len(raw))
	products := 0
	for _, item := range raw {
		var p wooProduct
		if json.Unmarshal(item, &p) != nil || p.ID == 0 {
			continue
		}
		products++
		title := strings.TrimSpace(html.UnescapeString(p.Name))
		if title == "" || p.Stock.Class == wooOutOfStock || p.Prices.CurrencyCode != "PLN" {
			continue
		}
		cents, ok := wooCents(p.Prices)
		if !ok {
			continue
		}
		data := wooRaw{
			ID: p.ID, Name: title, Permalink: p.Permalink,
			Price: p.Prices.Price, Currency: p.Prices.CurrencyCode, Stock: p.Stock.Class,
		}
		for _, c := range p.Categories {
			data.Categories = append(data.Categories, c.Slug)
		}
		rawData, err := json.Marshal(data)
		if err != nil {
			return nil, fmt.Errorf("%s: encode: %w", source, err)
		}
		l := Listing{
			Source:     source,
			ExternalID: strconv.FormatInt(p.ID, 10),
			Title:      title,
			URL:        p.Permalink,
			PriceCents: cents,
			AcquiredAs: new("new"),
			Raw:        rawData,
		}
		if len(p.Images) > 0 {
			l.PhotoURL = imageURL(p.Images[0].Src)
		}
		out = append(out, l)
	}
	if products == 0 && len(raw) > 0 {
		return nil, parseError{errors.New(source + ": response holds no product")}
	}
	return out, nil
}

// wooCents reads a Store API price, an integer string in the currency's
// minor unit ("14990" with minor unit 2 is 149,90 zł), into grosze. A
// missing minor unit means 2.
func wooCents(p wooPrices) (int64, bool) {
	minor := 2
	if p.MinorUnit != nil {
		minor = *p.MinorUnit
	}
	v, err := strconv.ParseInt(strings.TrimSpace(p.Price), 10, 64)
	if err != nil || v < 0 || minor < 0 || minor > 4 {
		return 0, false
	}
	for ; minor < 2; minor++ {
		if v > maxPriceCents {
			return 0, false
		}
		v *= 10
	}
	if minor > 2 {
		d := int64(1)
		for range minor - 2 {
			d *= 10
		}
		v = v/d + (v%d*2)/d
	}
	if v > maxPriceCents {
		return 0, false
	}
	return v, true
}
