package valuation

import (
	"context"
	"testing"
	"time"

	"github.com/Automaat/ohara/backend-go/internal/dbtest"
)

// stillOffered is a goneAfter for a listing the valuation still finds.
const stillOffered = -1

// usedBluray is a used OLX Blu-ray of title 1 at cents, first seen at
// first and gone goneAfter later, or still offered for stillOffered.
func usedBluray(first time.Time, cents int64, goneAfter time.Duration) Price {
	bluray := "bluray"
	p := Price{
		EditionID: 1, TitleID: 1, Format: &bluray, Source: SourceOLX, Cents: cents,
		AcquiredAs: used(), SeenAt: first, FirstSeenAt: first,
	}
	if goneAfter >= 0 {
		at := first.Add(goneAfter)
		p.GoneAt = &at
	}
	return p
}

func TestLikelySold(t *testing.T) {
	first := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	listing := func(cents int64, goneAfter time.Duration) Price {
		return usedBluray(first, cents, goneAfter)
	}
	dvd := "dvd"
	tests := []struct {
		name   string
		prices []Price
		want   *Estimate
	}{
		{
			name:   "median of listings gone soon after they appeared",
			prices: []Price{listing(4000, 5*day), listing(5000, 9*day), listing(6000, 12*day)},
			want:   &Estimate{Cents: 5000, Source: SourceOLX, Count: 3},
		},
		{
			name:   "two listings",
			prices: []Price{listing(4000, 5*day), listing(5000, 9*day)},
			want:   &Estimate{Cents: 4500, Source: SourceOLX, Count: 2},
		},
		{
			name:   "one listing",
			prices: []Price{listing(4000, 5*day)},
			want:   &Estimate{Cents: 4000, Source: SourceOLX, Count: 1},
		},
		{
			name:   "gone on the cut-off still counts",
			prices: []Price{listing(4000, LikelySoldWithin)},
			want:   &Estimate{Cents: 4000, Source: SourceOLX, Count: 1},
		},
		{
			name: "gone after the cut-off or still offered does not count",
			prices: []Price{
				listing(4000, 5*day), listing(9000, LikelySoldWithin+time.Minute), listing(9500, 30*day),
				listing(8000, stillOffered),
			},
			want: &Estimate{Cents: 4000, Source: SourceOLX, Count: 1},
		},
		{
			name: "new copies and other sources do not count",
			prices: func() []Price {
				sealed := listing(9900, 3*day)
				sealed.AcquiredAs = fresh()
				skup := listing(700, 3*day)
				skup.Source = SourceSkupSzop
				return []Price{sealed, skup, listing(4000, 5*day)}
			}(),
			want: &Estimate{Cents: 4000, Source: SourceOLX, Count: 1},
		},
		{
			name: "other title or format does not count",
			prices: func() []Price {
				other := listing(9000, 3*day)
				other.TitleID = 2
				disc := listing(1000, 3*day)
				disc.Format = &dvd
				return []Price{other, disc, listing(4000, 5*day)}
			}(),
			want: &Estimate{Cents: 4000, Source: SourceOLX, Count: 1},
		},
		{
			name: "outliers are left out",
			prices: []Price{
				listing(4000, day), listing(4200, day), listing(4400, day), listing(4600, day), listing(40000, day),
			},
			want: &Estimate{Cents: 4300, Source: SourceOLX, Count: 4, Trimmed: 1},
		},
		{
			name:   "none",
			prices: []Price{listing(8000, stillOffered)},
			want:   nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bluray := "bluray"
			copies := []CopyValue{{CopyID: 1, EditionID: 1, TitleID: 1, Format: &bluray}}
			Value(copies, NewRanges(tt.prices))
			got := copies[0].LikelySold
			if (got == nil) != (tt.want == nil) || got != nil && *got != *tt.want {
				t.Fatalf("LikelySold = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestLikelySoldLeavesValueAndTotals(t *testing.T) {
	first := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	var offered, gone []Price
	for _, c := range []int64{2400, 2500, 2600} {
		offered = append(offered, usedBluray(first, c, stillOffered))
		gone = append(gone, usedBluray(first, c, 5*24*time.Hour))
	}
	bluray := "bluray"
	without := []CopyValue{{CopyID: 1, EditionID: 1, TitleID: 1, Format: &bluray}}
	Value(without, NewRanges(offered))
	with := []CopyValue{{CopyID: 1, EditionID: 1, TitleID: 1, Format: &bluray}}
	Value(with, NewRanges(gone))
	if with[0].LikelySold == nil || without[0].LikelySold != nil {
		t.Fatalf("likely-sold = %+v from gone listings, %+v from offered ones", with[0].LikelySold, without[0].LikelySold)
	}
	if *with[0].Mid != *without[0].Mid || with[0].LowConfidence != without[0].LowConfidence {
		t.Fatalf("used value %+v with gone listings, %+v without", with[0].Mid, without[0].Mid)
	}
	if Summarize(with) != Summarize(without) {
		t.Fatalf("Summarize = %+v with gone listings, %+v without", Summarize(with), Summarize(without))
	}
}

func TestLikelySoldFromStoredListings(t *testing.T) {
	pool := dbtest.Migrated(t)
	seed(t, pool)
	ctx := context.Background()
	exec(t, pool, `INSERT INTO price_observation
			(source, external_id, price_cents, edition_id, acquired_as, observed_at, last_seen_at, gone_at)
		VALUES ('olx', 's1', 4000, 20, 'used', now() - interval '20 days', now() - interval '16 days', now() - interval '15 days'),
		       ('olx', 's2', 5000, 20, 'used', now() - interval '20 days', now() - interval '12 days', now() - interval '11 days'),
		       ('olx', 's3', 6000, 20, 'used', now() - interval '20 days', now() - interval '9 days', now() - interval '8 days'),
		       ('olx', 'late', 9000, 20, 'used', now() - interval '60 days', now() - interval '31 days', now() - interval '30 days'),
		       ('olx', 'open', 7000, 20, 'used', now() - interval '20 days', now(), NULL),
		       ('olx', 'old', 100, 20, 'used', now() - interval '120 days', now() - interval '100 days', now() - interval '99 days')`)
	store := NewStore(pool)
	window := 90 * 24 * time.Hour
	copies, sum, err := store.Valued(ctx, window)
	if err != nil {
		t.Fatalf("Valued: %v", err)
	}
	want := Estimate{Cents: 5000, Source: SourceOLX, Count: 3}
	var blurays int
	for _, c := range copies {
		if c.EditionID != 20 {
			if c.LikelySold != nil {
				t.Fatalf("copy %d of edition %d has likely-sold %+v", c.CopyID, c.EditionID, c.LikelySold)
			}
			continue
		}
		blurays++
		if c.LikelySold == nil || *c.LikelySold != want {
			t.Fatalf("copy %d likely-sold = %+v, want %+v", c.CopyID, c.LikelySold, want)
		}
		if c.Mid == nil || c.Mid.Count != 5 {
			t.Fatalf("copy %d used value = %+v, want every listing in the window", c.CopyID, c.Mid)
		}
	}
	if blurays != 2 || sum.Mid.Copies != 2 {
		t.Fatalf("valued %d Blu-ray copies, summary %+v", blurays, sum)
	}
	titled, err := store.TitleValued(ctx, window, 2)
	if err != nil {
		t.Fatalf("TitleValued: %v", err)
	}
	for _, c := range titled {
		if c.LikelySold == nil || *c.LikelySold != want {
			t.Fatalf("TitleValued copy %d likely-sold = %+v, want %+v", c.CopyID, c.LikelySold, want)
		}
	}
}
