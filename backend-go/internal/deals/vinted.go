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

// VintedURL is Vinted's catalog search page. Vinted retired the JSON
// /api/v2/catalog/items endpoint (it answers 404); the page renders the
// first 96 results, newest first, into its Next.js payload, which needs no
// session cookie. A changed page surfaces as a parse error in the run.
const VintedURL = "https://www.vinted.pl/catalog"

// Vinted searches vinted.pl listings, the first 96 per query.
type Vinted struct {
	Client  *http.Client
	BaseURL string
}

// Name implements Source.
func (Vinted) Name() string { return "vinted" }

// PerQuery implements Source.
func (Vinted) PerQuery() bool { return true }

// Search implements Source.
func (v Vinted) Search(ctx context.Context, query string) ([]Listing, error) {
	u := v.BaseURL + "?" + url.Values{
		"search_text": {query},
		"order":       {"newest_first"},
	}.Encode()
	body, err := get(ctx, v.Client, u, "text/html,application/xhtml+xml")
	if err != nil {
		return nil, fmt.Errorf("vinted: %w", err)
	}
	return parseVinted(body, origin(v.BaseURL))
}

// origin is the scheme and host of base, which listing paths are relative
// to.
func origin(base string) string {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// nextPush starts one Next.js flight chunk in a page script; type 1 chunks
// carry the payload text as a JSON string.
const nextPush = "self.__next_f.push([1,"

// catalogItems starts the search results in the flight payload.
const catalogItems = `"items":{"items":`

// flight joins the payload text of every type 1 chunk on page.
func flight(page string) string {
	var b strings.Builder
	rest := page
	for {
		i := strings.Index(rest, nextPush)
		if i < 0 {
			return b.String()
		}
		rest = rest[i+len(nextPush):]
		dec := json.NewDecoder(strings.NewReader(rest))
		var chunk string
		if err := dec.Decode(&chunk); err != nil {
			continue
		}
		b.WriteString(chunk)
		rest = rest[dec.InputOffset():]
	}
}

type vintedMoney struct {
	Amount       string `json:"amount"`
	CurrencyCode string `json:"currencyCode"`
}

type vintedItem struct {
	ProductItem struct {
		ID             int64        `json:"id"`
		Title          string       `json:"title"`
		URL            string       `json:"url"`
		Price          *vintedMoney `json:"price"`
		TotalItemPrice *vintedMoney `json:"totalItemPrice"`
		User           struct {
			ID         int64 `json:"id"`
			IsBusiness bool  `json:"isBusiness"`
		} `json:"user"`
		ItemBox struct {
			SecondLine string `json:"secondLine"`
		} `json:"itemBox"`
	} `json:"productItem"`
}

// vintedRaw is the part of a Vinted item kept with the observation; the
// page's own record carries a dozen photo URLs per item.
type vintedRaw struct {
	ID         int64        `json:"id"`
	Title      string       `json:"title"`
	URL        string       `json:"url"`
	Price      vintedMoney  `json:"price"`
	Total      *vintedMoney `json:"total_item_price,omitempty"`
	Condition  string       `json:"condition"`
	SellerID   int64        `json:"seller_id"`
	IsBusiness bool         `json:"is_business"`
}

// vintedUsed are the condition labels of used items; both new labels start
// with "Nowy".
var vintedUsed = map[string]bool{"Bardzo dobry": true, "Dobry": true, "Zadowalający": true}

// vintedCondition reads "new" or "used" from an item's second line, which
// holds the condition, after the size or brand when the item has them.
func vintedCondition(line string) *string {
	for part := range strings.SplitSeq(line, "·") {
		part = strings.TrimSpace(part)
		var state string
		switch {
		case strings.HasPrefix(part, "Nowy"):
			state = "new"
		case vintedUsed[part]:
			state = "used"
		default:
			continue
		}
		return &state
	}
	return nil
}

// parseVinted reads the listings out of a Vinted catalog page. Items not
// priced in PLN (Vinted converts most foreign ones) are left out. The price
// is the seller's, without the buyer protection fee or shipping.
func parseVinted(body []byte, base string) ([]Listing, error) {
	_, results, found := strings.Cut(flight(string(body)), catalogItems)
	if !found {
		return nil, parseError{errors.New("vinted: page has no catalog items")}
	}
	var items []json.RawMessage
	if err := json.NewDecoder(strings.NewReader(results)).Decode(&items); err != nil {
		return nil, parseError{fmt.Errorf("vinted: decode items: %w", err)}
	}
	if items == nil {
		return nil, parseError{errors.New("vinted: catalog items are not a list")}
	}
	out := make([]Listing, 0, len(items))
	for _, raw := range items {
		var it vintedItem
		if err := json.Unmarshal(raw, &it); err != nil {
			continue
		}
		p := &it.ProductItem
		if p.ID == 0 || p.Title == "" || p.Price == nil || p.Price.CurrencyCode != "PLN" {
			continue
		}
		amount, err := strconv.ParseFloat(p.Price.Amount, 64)
		if err != nil {
			continue
		}
		cents, ok := toCents(amount)
		if !ok {
			continue
		}
		link := p.URL
		if strings.HasPrefix(link, "/") {
			link = base + link
		}
		rec, err := json.Marshal(vintedRaw{
			ID: p.ID, Title: p.Title, URL: p.URL, Price: *p.Price, Total: p.TotalItemPrice,
			Condition: p.ItemBox.SecondLine, SellerID: p.User.ID, IsBusiness: p.User.IsBusiness,
		})
		if err != nil {
			continue
		}
		out = append(out, Listing{
			Source:     "vinted",
			ExternalID: strconv.FormatInt(p.ID, 10),
			Title:      p.Title,
			URL:        link,
			PriceCents: cents,
			AcquiredAs: vintedCondition(p.ItemBox.SecondLine),
			Raw:        rec,
		})
	}
	return out, nil
}
