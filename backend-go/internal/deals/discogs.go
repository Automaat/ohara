package deals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Automaat/ohara/backend-go/internal/discogs"
)

// DiscogsSource names the listings of Discogs sellers' inventories. The
// Discogs client counts its own requests under "discogs", and the
// valuation stores Discogs prices under names of its own.
const DiscogsSource = "discogs_inventory"

// Discogs API terms (support.discogs.com, API Terms of Use): seller
// inventory is Restricted Data, which no view may show more than six hours
// older than discogs.com and which is kept no longer than it serves. A
// listing an answer no longer holds is gone at once; one no answer has
// offered for discogsShown leaves the deals view and its alert is not
// sent; one none has offered for discogsKeep is deleted. Keeping it that
// long keeps its dismissal and sent alert while its seller's inventory
// cannot be read. discogsShown is discogsFresh less the 14 minutes a
// page draws an answer's Discogs listings (unconfirmedAfterMs in
// src/lib/deals.ts) and a margin for the page clock's 30-second tick, so
// a drawn card is never older than discogsFresh.
const (
	discogsFresh = 6 * time.Hour
	discogsShown = discogsFresh - 15*time.Minute
	discogsKeep  = 30 * 24 * time.Hour
)

// discogsNotice is the attribution the API terms require next to the
// listing's link.
const discogsNotice = "Data provided by Discogs."

// Rates converts a currency to PLN: the złoty value of one unit, an error
// for a currency without a rate.
type Rates interface {
	PLN(ctx context.Context, currency string) (float64, error)
}

// DiscogsSeller reads the newest listings of one Discogs seller's
// inventory once per run, priced in PLN at Rates. Every seller is a source
// of its own under DiscogsSource, so the run paces, counts and stops each
// request as it does any source's.
type DiscogsSeller struct {
	Client *discogs.Client
	Rates  Rates
	Seller string
}

// Name implements Source.
func (DiscogsSeller) Name() string { return DiscogsSource }

// PerQuery implements Source.
func (DiscogsSeller) PerQuery() bool { return false }

// Scope implements Expiring: the seller, whose inventory the answer is.
func (d DiscogsSeller) Scope() string { return d.Seller }

// Search implements Source; the query is ignored.
func (d DiscogsSeller) Search(ctx context.Context, q string) ([]Listing, error) {
	page, err := d.SearchPage(ctx, q)
	return page.Listings, err
}

// SearchPage implements Pager; the query is ignored. Seen names the
// listings the answer offers at a price: one Listings leaves out (a
// currency without a rate) is not seen, so the run marks it gone and its
// stored price is neither shown nor alerted on.
func (d DiscogsSeller) SearchPage(ctx context.Context, _ string) (Page, error) {
	body, err := d.Client.Inventory(ctx, DiscogsSource, d.Seller)
	if err != nil {
		return Page{}, fmt.Errorf("discogs seller %s: %w", d.Seller, err)
	}
	listings, seen, err := parseDiscogsInventory(ctx, body, d.Rates)
	if err != nil {
		return Page{}, fmt.Errorf("discogs seller %s: %w", d.Seller, err)
	}
	for i := range listings {
		listings[i].Query = d.Seller
	}
	return Page{Listings: listings, Seen: seen}, nil
}

type discogsListing struct {
	ID        int64  `json:"id"`
	Status    string `json:"status"`
	Condition string `json:"condition"`
	Sleeve    string `json:"sleeve_condition"`
	ShipsFrom string `json:"ships_from"`
	Posted    string `json:"posted"`
	Price     struct {
		Value    float64 `json:"value"`
		Currency string  `json:"currency"`
	} `json:"price"`
	Release struct {
		ID          int64  `json:"id"`
		Description string `json:"description"`
		Format      string `json:"format"`
		Thumbnail   string `json:"thumbnail"`
	} `json:"release"`
}

type discogsInventory struct {
	Listings *[]discogsListing `json:"listings"`
}

// discogsRaw is the part of a listing a stored observation keeps.
type discogsRaw struct {
	Price     float64 `json:"price"`
	Currency  string  `json:"currency"`
	Condition string  `json:"condition,omitempty"`
	Sleeve    string  `json:"sleeve_condition,omitempty"`
	ShipsFrom string  `json:"ships_from,omitempty"`
	Posted    string  `json:"posted,omitempty"`
	ReleaseID int64   `json:"release_id,omitempty"`
	Format    string  `json:"format,omitempty"`
	// Medium is the listing's Listing.Medium, which a pending alert and
	// the deals view check against the wish's current format
	// (mediumFits).
	Medium string `json:"medium"`
}

// mintGrade is the Goldmine grade of an unplayed copy, the only one that
// does not make a listing used.
const mintGrade = "Mint (M)"

// parseDiscogsInventory reads an inventory answer into its listings for
// sale, priced in grosze at rates, and their IDs. A listing whose currency
// has no rate is left out of both; a body that is not an inventory, or
// one with a row that has no ID or status, is a parse error, so an answer
// the run cannot read never marks a stored listing gone.
func parseDiscogsInventory(ctx context.Context, body []byte, rates Rates) ([]Listing, []string, error) {
	var inv discogsInventory
	if err := json.Unmarshal(body, &inv); err != nil {
		return nil, nil, parseError{fmt.Errorf("discogs: decode: %w", err)}
	}
	if inv.Listings == nil {
		return nil, nil, parseError{errors.New("discogs: no listings field")}
	}
	pln := map[string]float64{}
	out := make([]Listing, 0, len(*inv.Listings))
	seen := make([]string, 0, len(*inv.Listings))
	for i := range *inv.Listings {
		it := &(*inv.Listings)[i]
		if it.ID <= 0 || strings.TrimSpace(it.Status) == "" {
			return nil, nil, parseError{fmt.Errorf("discogs: listing %d of the answer has no ID or status", i)}
		}
		if it.Status != "For Sale" {
			continue
		}
		id := strconv.FormatInt(it.ID, 10)
		title := strings.TrimSpace(it.Release.Description)
		currency := strings.ToUpper(strings.TrimSpace(it.Price.Currency))
		if title == "" || currency == "" {
			continue
		}
		rate, ok := pln[currency]
		if !ok {
			r, err := rates.PLN(ctx, currency)
			if ctx.Err() != nil {
				return nil, nil, ctx.Err()
			}
			if err == nil && r > 0 {
				rate = r
			}
			pln[currency] = rate
		}
		cents, ok := toCents(it.Price.Value * rate)
		if rate == 0 || !ok || cents == 0 {
			continue
		}
		medium := discogsMedium(it.Release.Format)
		if medium == "" {
			medium = "other"
		}
		raw, err := json.Marshal(discogsRaw{
			Price: it.Price.Value, Currency: currency, Condition: it.Condition, Sleeve: it.Sleeve,
			ShipsFrom: it.ShipsFrom, Posted: it.Posted, ReleaseID: it.Release.ID, Format: it.Release.Format,
			Medium: medium,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("discogs: encode: %w", err)
		}
		var acquired *string
		if c := strings.TrimSpace(it.Condition); c != "" && c != mintGrade {
			acquired = new("used")
		}
		out = append(out, Listing{
			Source:     DiscogsSource,
			ExternalID: id,
			Title:      title,
			URL:        "https://www.discogs.com/sell/item/" + id,
			PhotoURL:   imageURL(it.Release.Thumbnail),
			PriceCents: cents,
			AcquiredAs: acquired,
			Medium:     medium,
			Raw:        raw,
		})
		seen = append(seen, id)
	}
	return out, seen, nil
}

// discogsMedium is the album medium a Discogs format names ("LP, Album,
// Gat", "2xCD, Comp", "Cass, Album", "Box Set, Comp + 4xLP"): the first
// of vinyl, CD and cassette among its parts, else "other" (a DVD, a file),
// or "" for no format.
func discogsMedium(format string) string {
	if strings.TrimSpace(format) == "" {
		return ""
	}
	for _, part := range strings.FieldsFunc(format, func(r rune) bool { return r == ',' || r == '+' }) {
		name := strings.ToLower(strings.TrimSpace(part))
		if n := strings.IndexByte(name, 'x'); n > 0 {
			if _, err := strconv.Atoi(name[:n]); err == nil {
				name = name[n+1:]
			}
		}
		switch name {
		case "lp", "vinyl", `12"`, `10"`, `7"`, "flexi-disc", "lathe cut":
			return "vinyl"
		case "cd", "cdr", "sacd", "hdcd":
			return "cd"
		case "cass", "cassette":
			return "cassette"
		}
	}
	return "other"
}
