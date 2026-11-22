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
func (p Pepper) Search(ctx context.Context, q string) ([]Listing, error) {
	page, err := p.SearchPage(ctx, q)
	return page.Listings, err
}

// SearchPage implements Pager; the query is ignored. Seen names every deal
// in the feed, also those without a price, and a feed is never complete:
// it holds only the newest deals.
func (p Pepper) SearchPage(ctx context.Context, _ string) (Page, error) {
	body, err := get(ctx, p.Client, p.Name(), p.BaseURL, "application/rss+xml, application/xml, text/xml")
	if err != nil {
		return Page{}, fmt.Errorf("pepper: %w", err)
	}
	listings, seen, err := parsePepperPage(body)
	if err != nil {
		return Page{}, err
	}
	return Page{Listings: listings, Seen: seen}, nil
}

type pepperMerchant struct {
	Name  string `xml:"name,attr"`
	Price string `xml:"price,attr"`
}

type pepperImage struct {
	URL string `xml:"url,attr"`
}

type pepperItem struct {
	Thumbnail pepperImage     `xml:"http://search.yahoo.com/mrss/ thumbnail"`
	Title     string          `xml:"title"`
	Link      string          `xml:"link"`
	GUID      string          `xml:"guid"`
	PubDate   string          `xml:"pubDate"`
	Merchant  *pepperMerchant `xml:"http://www.pepper.com/rss merchant"`
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
	listings, _, err := parsePepperPage(body)
	return listings, err
}

// parsePepperPage is parsePepper that also returns the ID of every deal
// the feed holds, those left out included.
func parsePepperPage(body []byte) ([]Listing, []string, error) {
	var feed pepperFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		return nil, nil, parseError{fmt.Errorf("pepper: decode: %w", err)}
	}
	out := make([]Listing, 0, len(feed.Items))
	var seen []string
	for i := range feed.Items {
		it := &feed.Items[i]
		id := strings.TrimSpace(it.GUID)
		if id == "" {
			id = strings.TrimSpace(it.Link)
		}
		if id != "" {
			seen = append(seen, id)
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
			return nil, nil, fmt.Errorf("pepper: encode: %w", err)
		}
		out = append(out, Listing{
			Source:     "pepper",
			PhotoURL:   imageURL(it.Thumbnail.URL),
			ExternalID: id,
			Title:      strings.TrimSpace(it.Title),
			URL:        strings.TrimSpace(it.Link),
			PriceCents: cents,
			Raw:        raw,
		})
	}
	return out, seen, nil
}
