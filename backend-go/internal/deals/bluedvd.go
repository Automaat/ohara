package deals

import (
	"context"
	"net/http"
)

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
func (BlueDVD) Name() string { return "bluedvd" }

// PerQuery implements Source.
func (BlueDVD) PerQuery() bool { return true }

// CanaryQuery implements Canaried: "dvd" finds almost only out-of-stock
// DVDs in this shop, "4k" its current 4K UHD releases.
func (BlueDVD) CanaryQuery() string { return "4k" }

// Search implements Source.
func (b BlueDVD) Search(ctx context.Context, query string) ([]Listing, error) {
	return wooStore{client: b.Client, source: b.Name(), endpoint: b.BaseURL}.search(ctx, query)
}
