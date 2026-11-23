package deals

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

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
	var candidates []int64
	for i := range observations {
		o := &observations[i]
		if o.MaxPriceCents == nil && o.MaxMarketPercent == nil {
			candidates = append(candidates, o.ID)
		}
	}
	return compareAskingPrices(ctx, tx, candidates)
}

func compareAskingPrices(ctx context.Context, q querier, candidates []int64) ([]AutomaticDeal, error) {
	results := make([]AutomaticDeal, len(candidates))
	if len(candidates) == 0 {
		return results, nil
	}
	positions := make(map[int64]int, len(candidates))
	for i, id := range candidates {
		positions[id] = i
		results[i].ObservationID = id
	}

	rows, err := q.Query(ctx, `WITH targets AS (
			SELECT coalesce(o.wishlist_copy_id, a.copy_id) AS wish_id, array_agg(o.id) AS candidate_ids
			FROM price_observation o LEFT JOIN deal_alert a ON a.observation_id = o.id
			WHERE o.id = ANY ($1) AND o.acquired_as IS NOT NULL
			GROUP BY 1
		)
		SELECT w.id, e.barcode, w.exact_edition, e.packaging, e.pl_voiceover, e.pl_subtitles, e.binding,
			o.id, o.price_cents, o.acquired_as, o.last_seen_at >= now() - interval '90 days',
			o.id = ANY (x.candidate_ids), o.edition_details, `+storedAlbumFields+`
		FROM targets x
		JOIN copy w ON w.id = x.wish_id AND w.status = 'wishlist'
			AND w.max_price_cents IS NULL AND w.max_market_percent IS NULL
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

func qualifyingAskingPrices(ctx context.Context, q querier, candidates []int64) (map[int64]bool, error) {
	comparisons, err := compareAskingPrices(ctx, q, candidates)
	if err != nil {
		return nil, err
	}
	qualified := make(map[int64]bool)
	for _, deal := range comparisons {
		if deal.Qualifies {
			qualified[deal.ObservationID] = true
		}
	}
	return qualified, nil
}

type matchedOffer struct {
	source, externalID string
	copyID             int64
}

func (s *Store) queueAutomaticAlerts(ctx context.Context, refreshed map[string]matchedOffer, asOf time.Time) (int, error) {
	if len(refreshed) == 0 {
		return 0, nil
	}
	sourceNames := make([]string, 0, len(refreshed))
	externalIDs := make([]string, 0, len(refreshed))
	copyIDs := make([]int64, 0, len(refreshed))
	for _, offer := range refreshed {
		sourceNames = append(sourceNames, offer.source)
		externalIDs = append(externalIDs, offer.externalID)
		copyIDs = append(copyIDs, offer.copyID)
	}

	candidates, err := automaticAlertCandidates(ctx, s.pool, sourceNames, externalIDs, copyIDs, asOf)
	if err != nil {
		return 0, err
	}
	ids := make([]int64, 0, len(candidates))
	for _, candidate := range candidates {
		ids = append(ids, candidate.id)
	}
	qualified, err := qualifyingAskingPrices(ctx, s.pool, ids)
	if err != nil {
		return 0, err
	}
	queued := 0
	for _, candidate := range candidates {
		if !qualified[candidate.id] {
			continue
		}
		added, err := s.queueAutomaticAlert(ctx, candidate)
		if err != nil {
			return queued, err
		}
		if added {
			queued++
		}
	}
	return queued, nil
}

func automaticAlertCandidates(ctx context.Context, q querier, sourceNames, externalIDs []string, copyIDs []int64,
	asOf time.Time,
) ([]alertCandidate, error) {
	rows, err := q.Query(ctx, `SELECT o.id, w.id, o.price_cents
		FROM unnest($1::text[], $2::text[], $3::bigint[]) AS seen(source, external_id, copy_id)
		JOIN price_observation o USING (source, external_id)
		JOIN copy w ON w.id = seen.copy_id AND w.id = o.wishlist_copy_id
		WHERE w.status = 'wishlist' AND w.max_price_cents IS NULL AND w.max_market_percent IS NULL
			AND o.last_seen_at >= $4 AND o.dismissed_at IS NULL AND o.gone_at IS NULL
			AND o.acquired_as IS NOT NULL AND `+freshListing+` AND `+wishOfListingTitle("w")+`
			AND `+listingFitsKind("w")+` AND `+wishNamedBy("w")+` AND `+mediumFits("w")+`
		ORDER BY o.id`, sourceNames, externalIDs, copyIDs, asOf)
	if err != nil {
		return nil, fmt.Errorf("automatic alert candidates: %w", err)
	}
	candidates, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (alertCandidate, error) {
		var candidate alertCandidate
		err := row.Scan(&candidate.id, &candidate.copyID, &candidate.priceCents)
		return candidate, err
	})
	if err != nil {
		return nil, fmt.Errorf("read automatic alert candidates: %w", err)
	}
	return candidates, nil
}

func (s *Store) queueAutomaticAlert(ctx context.Context, candidate alertCandidate) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("automatic alert: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	locked, err := tx.Exec(ctx, `SELECT 1 FROM price_observation
		WHERE id = $1 AND wishlist_copy_id = $2 AND price_cents = $3
			AND dismissed_at IS NULL AND gone_at IS NULL FOR UPDATE`, candidate.id, candidate.copyID, candidate.priceCents)
	if err != nil {
		return false, fmt.Errorf("automatic alert: lock listing: %w", err)
	}
	if locked.RowsAffected() == 0 {
		return false, nil
	}
	queued, err := queueAlert(ctx, tx, candidate, true)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("automatic alert: commit: %w", err)
	}
	return queued, nil
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
