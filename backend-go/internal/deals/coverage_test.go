package deals

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	"github.com/Automaat/ohara/backend-go/internal/dbtest"
)

func TestDealRunStoresCoverageForEachSourceAndWish(t *testing.T) {
	pool := dbtest.Migrated(t)
	blade := wishlist(t, pool, wantBluray(nil))
	alien := wishlist(t, pool, &Want{Kind: "movie", Name: "Alien", Format: new("dvd")})
	found := &fakeSource{name: "found", perQuery: true, listings: []Listing{
		{Source: "found", ExternalID: "same", Title: "Blade Runner Blu-ray", PriceCents: 3000},
		{Source: "found", ExternalID: "dvd", Title: "Blade Runner DVD", PriceCents: 2000},
	}}
	empty := &fakeSource{name: "empty", perQuery: true}
	failed := &fakeSource{name: "failed", perQuery: true, err: statusError{http.StatusServiceUnavailable}}
	refused := &refusingSource{name: "refused", refuseFrom: 1, refusal: statusError{http.StatusTooManyRequests}}
	h := newTestHunter(pool, nil, found, empty, failed, refused)
	h.cfg.CanaryQuery = "Blade Runner blu-ray"

	ctx := context.Background()
	h.execute(ctx)
	last, ok, err := h.store.LastRun(ctx)
	if err != nil || !ok || last.Result == nil || last.Result.coverage == nil {
		t.Fatalf("saved run has no coverage: %+v, exists %t, error %v", last, ok, err)
	}
	coverage := last.Result.coverage
	if coverage.Truncated {
		t.Fatal("coverage unexpectedly truncated")
	}
	rows := make(map[coverageKey]*wishCoverage)
	for _, row := range coverage.Rows {
		rows[row.coverageKey] = row
	}
	tests := []struct {
		name string
		want wishCoverage
	}{
		{"duplicate match and format rejection", wishCoverage{
			coverageKey: coverageKey{Source: "found", CopyID: blade},
			Replies:     2, Candidates: 5, Matched: 1, Rejections: map[rejectionReason]int{"wrong_format": 2},
		}},
		{"other wish has separate candidates", wishCoverage{
			coverageKey: coverageKey{Source: "found", CopyID: alien}, Replies: 1, Candidates: 2,
			Rejections: map[rejectionReason]int{"wrong_format": 1, "title_mismatch": 1},
		}},
		{"successful empty replies", wishCoverage{
			coverageKey: coverageKey{Source: "empty", CopyID: blade}, Replies: 2, Empty: 2,
		}},
		{"source failures", wishCoverage{
			coverageKey: coverageKey{Source: "failed", CopyID: blade}, Failed: 2,
		}},
		{"reused canary refusal and skipped alias", wishCoverage{
			coverageKey: coverageKey{Source: "refused", CopyID: blade}, Refused: 1, Skipped: 1,
		}},
		{"skipped after another wish's refusal", wishCoverage{
			coverageKey: coverageKey{Source: "refused", CopyID: alien}, Skipped: 1,
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rows[tt.want.coverageKey]
			if !reflect.DeepEqual(got, &tt.want) {
				t.Errorf("coverage = %+v, want %+v", got, tt.want)
			}
		})
	}
}
