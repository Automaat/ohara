package deals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// TezeuszURL is the Tezeusz used-book shop; its site search is /szukaj.
const TezeuszURL = "https://tezeusz.pl"

// Tezeusz searches the Tezeusz used-book shop, one results page per query
// in its relevance order, offers in stock only. A listing is a product
// card: one book that may have many copies, priced at its cheapest.
type Tezeusz struct {
	Client  *http.Client
	BaseURL string
}

// Name implements Source.
func (Tezeusz) Name() string { return "tezeusz" }

// PerQuery implements Source.
func (Tezeusz) PerQuery() bool { return true }

// Kinds implements KindSource: Tezeusz sells books.
func (Tezeusz) Kinds() []string { return []string{"book"} }

// Search implements Source.
func (t Tezeusz) Search(ctx context.Context, query string) ([]Listing, error) {
	base := strings.TrimSuffix(t.BaseURL, "/")
	u := base + "/szukaj?" + url.Values{"szukaj": {query}, "tylko_dostepne": {"1"}}.Encode()
	body, err := get(ctx, t.Client, t.Name(), u, "text/html,application/xhtml+xml")
	if err != nil {
		return nil, fmt.Errorf("tezeusz: %w", err)
	}
	listings, err := parseTezeusz(body, base)
	if err != nil {
		return nil, err
	}
	for i := range listings {
		listings[i].Query = query
	}
	return listings, nil
}

// tezeuszResults is the results column every search page has, with cards
// or with its "no results" note; a bot check or an error page lacks it.
var tezeuszResults = regexp.MustCompile(`\sid=["']search-products-results-list["']`)

// divTag is one opening or closing div tag.
var divTag = regexp.MustCompile(`<(/?)div\b[^>]*>`)

// tezeuszCard opens a product card: an anchor whose first class is
// tezeusz-product-card, not its "Szczegóły" button.
var tezeuszCard = regexp.MustCompile(`<a\s[^>]*\bclass=["']tezeusz-product-card[\s"'][^>]*>`)

// tezeuszTitle is a card's title with the author in a span.
var tezeuszTitle = regexp.MustCompile(`(?s)<h5\s[^>]*\bclass=["'][^"']*\btezeusz-product-card-title\b[^"']*["'][^>]*>(.*?)</h5>`)

// tezeuszPrice is a card's price: "od 27,12 zł do 89,65 zł" for a book
// with several copies, one price otherwise. The text ends at the first
// nested div, which holds the struck-out lowest price of 30 days.
var tezeuszPrice = regexp.MustCompile(`(?s)\bclass=["']tezeusz-product-card-price(?:-range)?["'][^>]*>(.*?)(?:<div|</div>)`)

// tezeuszImage is the cover inside a card's image box.
var tezeuszImage = regexp.MustCompile(`(?s)\bclass=["'][^"']*\btezeusz-product-card-image\b[^"']*["'][^>]*>.*?(<img\s[^>]*>)`)

// tezeuszAmount is one price in a card's price text.
var tezeuszAmount = regexp.MustCompile(`\d[\d\s.]*(?:,\d{1,2})?`)

// htmlTag is one tag, dropped from text.
var htmlTag = regexp.MustCompile(`<[^>]*>`)

// quotedAttr is one name="value" or name='value' pair inside a tag.
var quotedAttr = regexp.MustCompile(`([\w-]+)=(?:"([^"]*)"|'([^']*)')`)

// quotedAttrs returns a tag's attributes with lowercased names and
// unescaped values.
func quotedAttrs(tag string) map[string]string {
	out := map[string]string{}
	for _, m := range quotedAttr.FindAllStringSubmatch(tag, -1) {
		out[strings.ToLower(m[1])] = html.UnescapeString(m[2] + m[3])
	}
	return out
}

// htmlText is the visible text of an HTML fragment on one line.
func htmlText(fragment string) string {
	return strings.Join(strings.Fields(html.UnescapeString(htmlTag.ReplaceAllString(fragment, " "))), " ")
}

type tezeuszRaw struct {
	ProductID string `json:"product_id"`
	Title     string `json:"title"`
	Price     string `json:"price"`
}

// parseTezeusz reads the product cards of a search results page, resolving
// their links against base. A results page without cards has no listings;
// a page that is not a results page is a parse error. Cards without an ID,
// a title, a link or a price are left out.
func parseTezeusz(page []byte, base string) ([]Listing, error) {
	text, ok := tezeuszColumn(string(page))
	if !ok {
		return nil, parseError{errors.New("tezeusz: not a search results page")}
	}
	root, err := url.Parse(base + "/")
	if err != nil {
		return nil, fmt.Errorf("tezeusz: base url: %w", err)
	}
	var out []Listing
	seen := map[string]bool{}
	for _, loc := range tezeuszCard.FindAllStringIndex(text, -1) {
		a := quotedAttrs(text[loc[0]:loc[1]])
		body := text[loc[1]:]
		if end := strings.Index(body, "</a>"); end >= 0 {
			body = body[:end]
		}
		l, price, ok := tezeuszListing(a, body, root)
		if !ok || seen[l.ExternalID] {
			continue
		}
		raw, err := json.Marshal(tezeuszRaw{ProductID: l.ExternalID, Title: l.Title, Price: price})
		if err != nil {
			return nil, fmt.Errorf("tezeusz: encode: %w", err)
		}
		l.Raw = raw
		seen[l.ExternalID] = true
		out = append(out, l)
	}
	return out, nil
}

// tezeuszColumn is the inside of the page's results column, up to the div
// that closes it or the end of the page, so cards the page shows elsewhere,
// such as recommendations, are not results.
func tezeuszColumn(page string) (string, bool) {
	loc := tezeuszResults.FindStringIndex(page)
	if loc == nil {
		return "", false
	}
	end := strings.IndexByte(page[loc[1]:], '>')
	if end < 0 {
		return "", true
	}
	body := page[loc[1]+end+1:]
	depth := 1
	for _, tag := range divTag.FindAllStringSubmatchIndex(body, -1) {
		if tag[3] > tag[2] {
			depth--
		} else {
			depth++
		}
		if depth == 0 {
			return body[:tag[0]], true
		}
	}
	return body, true
}

// tezeuszListing reads one card from its anchor's attributes and its body,
// returning the listing without Raw and the card's price text.
func tezeuszListing(a map[string]string, body string, root *url.URL) (Listing, string, bool) {
	id := strings.TrimSpace(a["data-ga-product-id"])
	href := strings.TrimSpace(a["href"])
	m := tezeuszTitle.FindStringSubmatch(body)
	p := tezeuszPrice.FindStringSubmatch(body)
	if id == "" || href == "" || m == nil || p == nil {
		return Listing{}, "", false
	}
	title, price := htmlText(m[1]), htmlText(p[1])
	cents, ok := lowestPrice(price)
	link, err := root.Parse(href)
	if title == "" || !ok || err != nil {
		return Listing{}, "", false
	}
	var photo string
	if img := tezeuszImage.FindStringSubmatch(body); img != nil {
		photo = imageURL(quotedAttrs(img[1])["src"])
	}
	return Listing{
		Source:     "tezeusz",
		ExternalID: id,
		Title:      title,
		URL:        link.String(),
		PhotoURL:   photo,
		PriceCents: cents,
	}, price, true
}

// lowestPrice is the lowest of the prices in a card's price text, in
// grosze.
func lowestPrice(text string) (int64, bool) {
	var lowest int64
	found := false
	for _, amount := range tezeuszAmount.FindAllString(text, -1) {
		cents, ok := parsePrice(amount)
		if ok && (!found || cents < lowest) {
			lowest, found = cents, true
		}
	}
	return lowest, found
}
