package deals

import (
	"context"
	"net/http"
	"regexp"
	"strconv"
)

const blueDVDSource = "bluedvd"

// BlueDVDURL is the WooCommerce Store API product search of bluedvd.pl, a
// shop of 4K UHD and Blu-ray discs, steelbooks and preorders.
const BlueDVDURL = "https://bluedvd.pl/wp-json/wc/store/v1/products"

// BlueDVD searches the bluedvd.pl shop, 50 products per query. Its
// listings are new copies, preorders included.
type BlueDVD struct {
	Client  *http.Client
	BaseURL string
}

// Name implements Source.
func (BlueDVD) Name() string { return blueDVDSource }

// PerQuery implements Source.
func (BlueDVD) PerQuery() bool { return true }

// Kinds limits the source to its film and television catalog.
func (BlueDVD) Kinds() []string { return []string{"movie", "tv"} }

// CanaryQuery implements Canaried: "dvd" finds almost only out-of-stock
// DVDs in this shop, "4k" its current 4K UHD releases.
func (BlueDVD) CanaryQuery() string { return "4k" }

// Search implements Source.
func (b BlueDVD) Search(ctx context.Context, query string) ([]Listing, error) {
	listings, err := wooStore{client: b.Client, source: b.Name(), endpoint: b.BaseURL}.search(ctx, query)
	for i := range listings {
		l := &listings[i]
		if season, tv := blueDVDSeason(l.Title); tv {
			l.Kinds = []string{"tv"}
			l.TVSeason = season
		}
	}
	return listings, err
}

var (
	seasonLabel = regexp.MustCompile(`(?i)\b(?:season|sezon)[:\s]+(\d+)\b`)
	seasonRange = regexp.MustCompile(`(?i)^\s*(?:[-–—/&+,]|and\b|i\b|to\b|do\b|episode\b|odcinek\b)\s*\d+`)
)

func blueDVDSeason(title string) (*int, bool) {
	hits := seasonLabel.FindAllStringSubmatchIndex(title, 2)
	if len(hits) == 0 {
		return nil, false
	}
	hit := hits[0]
	if len(hits) > 1 || seasonRange.MatchString(title[hit[1]:]) {
		return nil, true
	}
	season, err := strconv.Atoi(title[hit[2]:hit[3]])
	if err != nil || season > 9999 {
		return nil, true
	}
	return &season, true
}
