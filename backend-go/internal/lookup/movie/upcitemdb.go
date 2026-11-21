package movie

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Automaat/ohara/backend-go/internal/fetch"
)

// UPCitemdb queries the free UPCitemdb trial endpoint (100 requests a day and
// 6 a minute per IP, no key). It knows mostly US releases. Past either limit
// it answers 429, which Products reports as a *RateLimitError.
type UPCitemdb struct {
	Client  *http.Client
	BaseURL string
}

// ErrQuotaExceeded reports a source's request limit.
var ErrQuotaExceeded = errors.New("rate limit exceeded")

type upcResponse struct {
	Code  string `json:"code"`
	Items []struct {
		Title    string `json:"title"`
		Category string `json:"category"`
	} `json:"items"`
}

// Name is "upcitemdb".
func (UPCitemdb) Name() string { return "upcitemdb" }

// Products looks barcode up. UPCitemdb answers an unknown code with 200 and
// no items, and an invalid one with 400 INVALID_UPC; both are no match.
func (u UPCitemdb) Products(ctx context.Context, code string) ([]Product, error) {
	base := u.BaseURL
	if base == "" {
		base = "https://api.upcitemdb.com"
	}
	resp, err := get(ctx, u.Client, u.Name(), fetch.Request{
		URL:    base + "/prod/trial/lookup?upc=" + url.QueryEscape(code),
		Header: http.Header{"Accept": {"application/json"}},
		OK:     []int{http.StatusOK, http.StatusBadRequest, http.StatusNotFound},
	})
	if isStatus(err, http.StatusTooManyRequests) {
		return nil, fmt.Errorf("upcitemdb: %w", &RateLimitError{Reset: limitReset(resp.Header)})
	}
	if err != nil {
		return nil, err
	}
	var r upcResponse
	if err := json.Unmarshal(resp.Body, &r); err != nil {
		return nil, parseFailed(u.Name(), err)
	}
	if resp.Status != http.StatusOK {
		if r.Code != "INVALID_UPC" && r.Code != "NOT_FOUND" {
			return nil, parseFailed(u.Name(), fmt.Errorf("status %d code %q", resp.Status, r.Code))
		}
		found(u.Name(), 0)
		return []Product{}, nil
	}
	products := []Product{}
	for _, it := range r.Items {
		if t := strings.TrimSpace(it.Title); t != "" {
			products = append(products, Product{Source: "upcitemdb", Name: t, Category: it.Category})
		}
	}
	found(u.Name(), len(products))
	return products, nil
}
