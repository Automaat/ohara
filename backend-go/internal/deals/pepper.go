package deals

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"strings"
)

// PepperURL is the feed of every new Pepper.pl deal; /rss/nowe redirects
// here.
const PepperURL = "https://www.pepper.pl/rss/wszystkie"

// Pepper reads the Pepper.pl deal feed once per run and leaves matching to
// the hunter.
type Pepper struct {
	Client  *http.Client
	BaseURL string
}

// Name implements Source.
func (Pepper) Name() string { return "pepper" }

// PerQuery implements Source.
func (Pepper) PerQuery() bool { return false }

// Search implements Source; the query is ignored.
func (p Pepper) Search(ctx context.Context, _ string) ([]Listing, error) {
	body, err := get(ctx, p.Client, p.BaseURL, "application/rss+xml, application/xml, text/xml")
	if err != nil {
		return nil, fmt.Errorf("pepper: %w", err)
	}
	return parsePepper(body)
}

type pepperMerchant struct {
	Name  string `xml:"name,attr"`
	Price string `xml:"price,attr"`
}

type pepperItem struct {
	Title    string          `xml:"title"`
	Link     string          `xml:"link"`
	GUID     string          `xml:"guid"`
	PubDate  string          `xml:"pubDate"`
	Merchant *pepperMerchant `xml:"http://www.pepper.com/rss merchant"`
}

type pepperFeed struct {
	Items []pepperItem `xml:"channel>item"`
}

type pepperRaw struct {
	Title    string `json:"title"`
	Link     string `json:"link"`
	PubDate  string `json:"pub_date"`
	Merchant string `json:"merchant"`
	Price    string `json:"price"`
}

// parsePepper reads the RSS feed. Deals without a price (vouchers, freebies
// described in words) are left out.
func parsePepper(body []byte) ([]Listing, error) {
	var feed pepperFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		return nil, fmt.Errorf("pepper: decode: %w", err)
	}
	out := make([]Listing, 0, len(feed.Items))
	for i := range feed.Items {
		it := &feed.Items[i]
		id := strings.TrimSpace(it.GUID)
		if id == "" {
			id = strings.TrimSpace(it.Link)
		}
		if id == "" || it.Merchant == nil || strings.TrimSpace(it.Title) == "" {
			continue
		}
		cents, ok := parsePrice(it.Merchant.Price)
		if !ok {
			continue
		}
		raw, err := json.Marshal(pepperRaw{
			Title: it.Title, Link: it.Link, PubDate: it.PubDate,
			Merchant: it.Merchant.Name, Price: it.Merchant.Price,
		})
		if err != nil {
			return nil, fmt.Errorf("pepper: encode: %w", err)
		}
		out = append(out, Listing{
			Source:     "pepper",
			ExternalID: id,
			Title:      strings.TrimSpace(it.Title),
			URL:        strings.TrimSpace(it.Link),
			PriceCents: cents,
			Raw:        raw,
		})
	}
	return out, nil
}
