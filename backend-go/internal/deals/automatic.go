package deals

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
)

const (
	automaticMinOffers = 5
	automaticDiscount  = 20
)

// AutomaticDeal explains the asking-price comparison for a listing of a wish without limits.
type AutomaticDeal struct {
	ObservationID   int64    `json:"observation_id"`
	SampleCount     int      `json:"sample_count"`
	TypicalCents    *float64 `json:"typical_cents"`
	DiscountPercent *int     `json:"discount_percent"`
	Qualifies       bool     `json:"qualifies"`
}

type askingPrice struct {
	id, cents int64
	sample    bool
	candidate bool
}

type askingPriceGroup struct {
	wishID            int64
	medium, condition string
}

func automaticDeals(ctx context.Context, tx pgx.Tx, observations []Observation) ([]AutomaticDeal, error) {
	results := make([]AutomaticDeal, 0)
	positions := make(map[int64]int)
	var candidates []int64
	for i := range observations {
		o := &observations[i]
		if o.MaxPriceCents != nil || o.MaxMarketPercent != nil {
			continue
		}
		positions[o.ID] = len(results)
		results = append(results, AutomaticDeal{ObservationID: o.ID})
		if o.AcquiredAs != nil {
			candidates = append(candidates, o.ID)
		}
	}
	if len(candidates) == 0 {
		return results, nil
	}

	rows, err := tx.Query(ctx, `WITH targets AS (
			SELECT coalesce(o.wishlist_copy_id, a.copy_id) AS wish_id, array_agg(o.id) AS candidate_ids
			FROM price_observation o LEFT JOIN deal_alert a ON a.observation_id = o.id
			WHERE o.id = ANY ($1)
			GROUP BY 1
		)
		SELECT w.id, e.barcode, w.exact_edition, e.packaging, e.pl_voiceover, e.pl_subtitles, e.binding,
			o.id, o.price_cents, o.acquired_as, o.last_seen_at >= now() - interval '90 days',
			o.id = ANY (x.candidate_ids), o.edition_details, `+storedAlbumFields+`
		FROM targets x
		JOIN copy w ON w.id = x.wish_id
		JOIN edition e ON e.id = w.edition_id
		JOIN title t ON t.id = e.title_id
		JOIN edition pe ON pe.title_id = t.id
		JOIN price_observation o ON o.edition_id = pe.id
		WHERE o.source = ANY ($2) AND nullif(o.external_id, '') IS NOT NULL AND o.acquired_as IS NOT NULL
			AND (o.last_seen_at >= now() - interval '90 days' OR o.id = ANY (x.candidate_ids))
			AND `+freshListing, candidates, sources)
	if err != nil {
		return nil, fmt.Errorf("automatic deal comparisons: %w", err)
	}
	defer rows.Close()

	groups := make(map[askingPriceGroup][]askingPrice)
	for rows.Next() {
		var evidence storedAlbum
		w := &evidence.want
		var wishID int64
		var price askingPrice
		var condition string
		var details []string
		err := rows.Scan(append([]any{
			&wishID, &w.Barcode, &w.ExactEdition, &w.Packaging, &w.PLVoiceover, &w.PLSubtitles, &w.Binding,
			&price.id, &price.cents, &condition, &price.sample, &price.candidate, &details,
		}, evidence.destinations()...)...)
		if err != nil {
			return nil, fmt.Errorf("scan automatic deal comparison: %w", err)
		}
		medium := verifiedMedium(&evidence, details)
		if medium != "" {
			key := askingPriceGroup{wishID, medium, condition}
			groups[key] = append(groups[key], price)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read automatic deal comparisons: %w", err)
	}
	for _, prices := range groups {
		applyAskingPrices(prices, positions, results)
	}
	return results, nil
}

func verifiedMedium(evidence *storedAlbum, details []string) string {
	w, listing := &evidence.want, &evidence.listing
	w.Prepare()
	if !w.VerifiesAlbum(listing) || !hasExactEvidence(w, details) {
		return ""
	}
	text := listing.text(Rules{})
	p := w.text()
	if !text.matches(w, p) {
		return ""
	}
	switch w.Kind {
	case "movie":
		return text.format
	case "music":
		if listing.Medium != "" {
			return listing.Medium
		}
		if medium := albumFormat(text.words, p.owns); medium != "" {
			return medium
		}
		if p.albumFormatMatches("cd", text.words) {
			return "cd"
		}
	case "book":
		if w.Format != nil {
			return *w.Format
		}
		return "print"
	}
	return ""
}

func hasExactEvidence(w *Want, details []string) bool {
	if !w.ExactEdition {
		return true
	}
	if len(requiredDetails(w)) == 0 || !editionNamed(w, details) {
		return false
	}
	for _, detail := range details {
		switch detail {
		case detailVoiceover:
			if w.PLVoiceover == "no" {
				return false
			}
		case detailSubtitles:
			if w.PLSubtitles == "no" {
				return false
			}
		case "hardcover", "paperback":
			if w.Binding != nil && *w.Binding != detail {
				return false
			}
		default:
			if !slices.Contains(w.Packaging, detail) {
				return false
			}
		}
	}
	return true
}

func applyAskingPrices(prices []askingPrice, positions map[int64]int, results []AutomaticDeal) {
	slices.SortFunc(prices, func(a, b askingPrice) int { return cmp.Compare(a.cents, b.cents) })
	samples := make([]int64, 0, len(prices))
	for _, price := range prices {
		if price.sample {
			samples = append(samples, price.cents)
		}
	}
	at := 0
	for _, price := range prices {
		if price.candidate {
			results[positions[price.id]].compare(price, samples, at)
		}
		if price.sample {
			at++
		}
	}
}

func (d *AutomaticDeal) compare(price askingPrice, samples []int64, at int) {
	n := len(samples)
	if price.sample {
		n--
	}
	d.SampleCount = n
	if n < automaticMinOffers {
		return
	}
	low, high := (n-1)/2, n/2
	if price.sample && low >= at {
		low++
	}
	if price.sample && high >= at {
		high++
	}
	// Keep half-grosz medians exact for the qualification comparison.
	twiceMedian := samples[low] + samples[high]
	if twiceMedian == 0 {
		return
	}
	d.TypicalCents = new(float64(twiceMedian) / 2)
	d.DiscountPercent = new(int((twiceMedian - 2*price.cents) * 100 / twiceMedian))
	d.Qualifies = 200*price.cents <= (100-automaticDiscount)*twiceMedian
}
