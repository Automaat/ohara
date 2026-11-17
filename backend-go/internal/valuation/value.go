// Package valuation estimates what the owned collection is worth. A daily
// run asks OLX and Ceneo for every owned edition, SkupSzop for every owned
// book and Discogs for every owned album release, keeps the prices in
// price_observation and stores the day's collection total. Each copy gets
// a range: the buy-back offer as its floor, the median used asking price
// on OLX as its middle and the new price on Ceneo as its ceiling. An album
// edition with Discogs prices takes the cheapest Discogs copy for sale as
// its floor and the suggested Very Good Plus and Mint prices as its middle
// and ceiling.
package valuation

import (
	"math"
	"slices"
	"time"
)

// Sources the valuation reads from price_observation.
const (
	SourceOLX      = "olx"
	SourceCeneo    = "ceneo"
	SourceSkupSzop = "skupszop"
)

var (
	discogsSources   = []string{SourceDiscogsLowest, SourceDiscogsVGPlus, SourceDiscogsMint}
	valuationSources = append([]string{SourceOLX, SourceCeneo, SourceSkupSzop}, discogsSources...)
)

// MinObservations is the fewest OLX prices a middle value needs before it
// is trusted; a copy with fewer is low confidence.
const MinObservations = 3

// Estimate is one bound of a value range: the amount, the source it comes
// from, how many prices it rests on and how many outliers were left out.
type Estimate struct {
	Cents   int64  `json:"cents"`
	Source  string `json:"source"`
	Count   int    `json:"count"`
	Trimmed int    `json:"trimmed"`
}

// Range is the value of one edition. A nil bound has no prices yet.
type Range struct {
	Floor   *Estimate
	Mid     *Estimate
	Ceiling *Estimate
}

// LowConfidence reports a middle value missing or resting on fewer than
// MinObservations OLX prices. A Discogs suggestion rests on the release's
// sale history, so one is enough.
func (r Range) LowConfidence() bool {
	return r.Mid == nil || (r.Mid.Source != SourceDiscogs && r.Mid.Count < MinObservations)
}

// Price is one observation the valuation window holds, with the title and
// format of the edition it was recorded for.
type Price struct {
	EditionID  int64
	TitleID    int64
	Format     *string
	Source     string
	Cents      int64
	AcquiredAs *string
	SeenAt     time.Time
}

// group is the set of editions whose listings price each other: one title
// in one format. Listings rarely tell a book's printings apart, and a
// matched listing is stored against only one of them.
type group struct {
	titleID int64
	format  string
}

func groupOf(titleID int64, format *string) group {
	g := group{titleID: titleID}
	if format != nil {
		g.format = *format
	}
	return g
}

// Ranges values editions from the prices in the window. The floor is the
// edition's own latest SkupSzop offer, as a buy-back quotes one ISBN, or
// its latest cheapest Discogs copy for sale. The middle is the trimmed
// median of OLX prices that are not for new copies and the ceiling the
// trimmed median of Ceneo's lowest new prices, both pooled over every
// edition of the same title and format; the edition's own latest Discogs
// suggestions replace them, as a Discogs release is one pressing.
type Ranges struct {
	floors       map[int64]*Estimate
	mids         map[group]*Estimate
	ceils        map[group]*Estimate
	discogsMids  map[int64]*Estimate
	discogsCeils map[int64]*Estimate
}

// editionPrice is the latest price of one source for one edition.
type editionPrice struct {
	source    string
	editionID int64
}

// estimateSources are the sources whose latest price per edition is a
// bound as is, and the source that bound names.
var estimateSources = map[string]string{
	SourceSkupSzop:      SourceSkupSzop,
	SourceDiscogsLowest: SourceDiscogs,
	SourceDiscogsVGPlus: SourceDiscogs,
	SourceDiscogsMint:   SourceDiscogs,
}

// NewRanges sorts prices into floors and Discogs bounds per edition and
// middles and ceilings per title and format.
func NewRanges(prices []Price) Ranges {
	latest := map[editionPrice]*Price{}
	used := map[group][]int64{}
	fresh := map[group][]int64{}
	for i := range prices {
		p := &prices[i]
		g := groupOf(p.TitleID, p.Format)
		switch p.Source {
		case SourceOLX:
			if p.AcquiredAs == nil || *p.AcquiredAs != "new" {
				used[g] = append(used[g], p.Cents)
			}
		case SourceCeneo:
			fresh[g] = append(fresh[g], p.Cents)
		default:
			if _, ok := estimateSources[p.Source]; !ok {
				continue
			}
			k := editionPrice{source: p.Source, editionID: p.EditionID}
			if f := latest[k]; f == nil || p.SeenAt.After(f.SeenAt) {
				latest[k] = p
			}
		}
	}
	r := Ranges{
		floors:       map[int64]*Estimate{},
		mids:         make(map[group]*Estimate, len(used)),
		ceils:        make(map[group]*Estimate, len(fresh)),
		discogsMids:  map[int64]*Estimate{},
		discogsCeils: map[int64]*Estimate{},
	}
	for k, p := range latest {
		e := &Estimate{Cents: p.Cents, Source: estimateSources[k.source], Count: 1}
		switch k.source {
		case SourceDiscogsVGPlus:
			r.discogsMids[k.editionID] = e
		case SourceDiscogsMint:
			r.discogsCeils[k.editionID] = e
		default:
			r.floors[k.editionID] = e
		}
	}
	for g, v := range used {
		r.mids[g] = trimmedMedian(v, SourceOLX)
	}
	for g, v := range fresh {
		r.ceils[g] = trimmedMedian(v, SourceCeneo)
	}
	return r
}

// For is the range of one edition of titleID in format.
func (r Ranges) For(editionID, titleID int64, format *string) Range {
	g := groupOf(titleID, format)
	out := Range{Floor: r.floors[editionID], Mid: r.mids[g], Ceiling: r.ceils[g]}
	if e := r.discogsMids[editionID]; e != nil {
		out.Mid = e
	}
	if e := r.discogsCeils[editionID]; e != nil {
		out.Ceiling = e
	}
	return out
}

// trimmedMedian is the median of values once outliers are dropped: with
// four or more values, those beyond 1.5 interquartile ranges from the
// quartiles. Nil when there are no values.
func trimmedMedian(values []int64, source string) *Estimate {
	if len(values) == 0 {
		return nil
	}
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	kept := sorted
	if len(sorted) >= 4 {
		q1, q3 := quantile(sorted, 0.25), quantile(sorted, 0.75)
		iqr := q3 - q1
		lo, hi := q1-1.5*iqr, q3+1.5*iqr
		kept = make([]int64, 0, len(sorted))
		for _, v := range sorted {
			if f := float64(v); f >= lo && f <= hi {
				kept = append(kept, v)
			}
		}
	}
	return &Estimate{
		Cents:   int64(math.Round(quantile(kept, 0.5))),
		Source:  source,
		Count:   len(kept),
		Trimmed: len(sorted) - len(kept),
	}
}

// quantile interpolates linearly between the sorted values around q.
func quantile(sorted []int64, q float64) float64 {
	pos := q * float64(len(sorted)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	frac := pos - float64(lo)
	return float64(sorted[lo])*(1-frac) + float64(sorted[hi])*frac
}

// CopyValue is one owned copy with its edition's range.
type CopyValue struct {
	CopyID        int64     `json:"copy_id"`
	EditionID     int64     `json:"edition_id"`
	TitleID       int64     `json:"title_id"`
	TitleName     string    `json:"title_name"`
	Kind          string    `json:"kind"`
	Format        *string   `json:"format"`
	Barcode       *string   `json:"barcode"`
	PriceCents    *int64    `json:"price_cents"`
	Floor         *Estimate `json:"floor"`
	Mid           *Estimate `json:"mid"`
	Ceiling       *Estimate `json:"ceiling"`
	LowConfidence bool      `json:"low_confidence"`
}

// Bound is one bound summed over the collection: the total of the copies
// that have it and how many copies that is.
type Bound struct {
	Cents  int64 `json:"cents"`
	Copies int   `json:"copies"`
}

// Summary is the collection's value range against what was paid for it.
type Summary struct {
	Copies        int   `json:"copies"`
	SpentCents    int64 `json:"spent_cents"`
	Floor         Bound `json:"floor"`
	Mid           Bound `json:"mid"`
	Ceiling       Bound `json:"ceiling"`
	LowConfidence int   `json:"low_confidence"`
}

// Value fills each copy's range from ranges.
func Value(copies []CopyValue, ranges Ranges) {
	for i := range copies {
		c := &copies[i]
		r := ranges.For(c.EditionID, c.TitleID, c.Format)
		c.Floor, c.Mid, c.Ceiling = r.Floor, r.Mid, r.Ceiling
		c.LowConfidence = r.LowConfidence()
	}
}

// Summarize totals valued copies.
func Summarize(copies []CopyValue) Summary {
	s := Summary{Copies: len(copies)}
	add := func(b *Bound, e *Estimate) {
		if e != nil {
			b.Cents += e.Cents
			b.Copies++
		}
	}
	for i := range copies {
		c := &copies[i]
		if c.PriceCents != nil {
			s.SpentCents += *c.PriceCents
		}
		add(&s.Floor, c.Floor)
		add(&s.Mid, c.Mid)
		add(&s.Ceiling, c.Ceiling)
		if c.LowConfidence {
			s.LowConfidence++
		}
	}
	return s
}
