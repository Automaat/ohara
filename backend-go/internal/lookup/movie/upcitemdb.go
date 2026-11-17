package movie

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// UPCitemdb queries the free UPCitemdb trial endpoint (100 requests a day per
// IP, no key). It knows mostly US releases.
type UPCitemdb struct {
	Client  *http.Client
	BaseURL string
}

// ErrQuotaExceeded reports a provider's daily request limit.
var ErrQuotaExceeded = errors.New("daily quota exceeded")

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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/prod/trial/lookup?upc="+url.QueryEscape(code), http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("upcitemdb: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	client := u.Client
	if client == nil {
		client = http.DefaultClient
	}
	status, body, err := get(client, u.Name(), req, http.StatusOK, http.StatusBadRequest, http.StatusNotFound)
	if isStatus(err, http.StatusTooManyRequests) {
		return nil, fmt.Errorf("upcitemdb: %w", ErrQuotaExceeded)
	}
	if err != nil {
		return nil, err
	}
	var r upcResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, parseFailed(u.Name(), err)
	}
	if status != http.StatusOK {
		if r.Code != "INVALID_UPC" && r.Code != "NOT_FOUND" {
			return nil, parseFailed(u.Name(), fmt.Errorf("status %d code %q", status, r.Code))
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
