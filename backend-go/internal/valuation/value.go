// Package valuation estimates what the owned collection is worth. A daily
// run asks OLX for every owned edition, SkupSzop for every owned book and
// Discogs for every owned album release, keeps the prices in
// price_observation and stores the day's collection total. Each copy gets
// a range: the buy-back offer as its floor and the median used asking price
// on OLX as its middle. An album edition with Discogs prices
// takes the cheapest Discogs copy for sale as its floor, the suggested
// price of the grade its condition maps to (Very Good Plus without one) as
// its middle and the suggested Mint price as its ceiling; other copies
// have no ceiling.
package valuation

import (
	"cmp"
	"maps"
	"math"
	"slices"
	"time"
)

// Sources the valuation reads from price_observation.
const (
	SourceOLX      = "olx"
	SourceSkupSzop = "skupszop"
)

var (
	discogsSources = []string{
		SourceDiscogsLowest, SourceDiscogsMint, SourceDiscogsNearMint, SourceDiscogsVGPlus,
		SourceDiscogsVG, SourceDiscogsGood, SourceDiscogsPoor,
	}
	valuationSources = append([]string{SourceOLX, SourceSkupSzop}, discogsSources...)
	pooledSources    = []string{SourceOLX}
)

// Grades of the Discogs suggestion a copy's middle is, as the page names
// them.
const (
	GradeNearMint = "NM"
	GradeVGPlus   = "VG+"
	GradeVG       = "VG"
	GradeGood     = "G"
	GradePoor     = "P"
)

// Grades lists the grades a middle can name, best first.
var Grades = []string{GradeNearMint, GradeVGPlus, GradeVG, GradeGood, GradePoor}

// gradeSources maps each grade to the source its suggestion is stored
// under.
var gradeSources = map[string]string{
	GradeNearMint: SourceDiscogsNearMint,
	GradeVGPlus:   SourceDiscogsVGPlus,
	GradeVG:       SourceDiscogsVG,
	GradeGood:     SourceDiscogsGood,
	GradePoor:     SourceDiscogsPoor,
}

// isGradeSource reports a source holding the suggestion of a middle
// grade.
func isGradeSource(source string) bool {
	for _, s := range gradeSources {
		if s == source {
			return true
		}
	}
	return false
}

// conditionGrades maps a copy's condition to the Discogs grade its used
// value is priced at. A worse condition never maps to a better grade; a
// mint copy is no sealed one, so it takes Near Mint and leaves Mint to the
// ceiling.
var conditionGrades = map[string]string{
	"mint":      GradeNearMint,
	"very_good": GradeVGPlus,
	"good":      GradeVG,
	"fair":      GradeGood,
	"poor":      GradePoor,
}

// MinObservations is the fewest used prices a middle value needs before it
// is trusted; a copy with fewer is low confidence.
const MinObservations = 3

// Estimate is one bound of a value range: the amount, the source it comes
// from, how many prices it rests on and how many outliers were left out.
type Estimate struct {
	Cents   int64  `json:"cents"`
	Source  string `json:"source" ts:"ValueSource"`
	Count   int    `json:"count"`
	Trimmed int    `json:"trimmed"`
}

// Range is the value of one edition. A nil bound has no prices yet; only a
// Discogs release has a ceiling.
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
// median of OLX prices that are not for new copies, pooled over
// every edition of the same title and format. The edition's own latest
// Discogs suggestions replace the middle and give the ceiling, as a
// Discogs release is one pressing. A middle grade counts only when the
// edition's newest suggestions answer named it: one run stores an answer
// at one instant, so a grade it left out is older and does not count.
type Ranges struct {
	floors       map[int64]*Estimate
	mids         map[group]*Estimate
	discogsMids  map[int64]map[string]*Estimate
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
	SourceSkupSzop:        SourceSkupSzop,
	SourceDiscogsLowest:   SourceDiscogs,
	SourceDiscogsMint:     SourceDiscogs,
	SourceDiscogsNearMint: SourceDiscogs,
	SourceDiscogsVGPlus:   SourceDiscogs,
	SourceDiscogsVG:       SourceDiscogs,
	SourceDiscogsGood:     SourceDiscogs,
	SourceDiscogsPoor:     SourceDiscogs,
}

// NewRanges sorts prices into floors and Discogs bounds per edition and
// middles per title and format.
func NewRanges(prices []Price) Ranges {
	latest := map[editionPrice]*Price{}
	used := map[group][]int64{}
	for i := range prices {
		p := &prices[i]
		g := groupOf(p.TitleID, p.Format)
		switch p.Source {
		case SourceOLX:
			if p.AcquiredAs == nil || *p.AcquiredAs != "new" {
				used[g] = append(used[g], p.Cents)
			}
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
	newest := map[int64]time.Time{}
	for k, p := range latest {
		if isGradeSource(k.source) && p.SeenAt.After(newest[k.editionID]) {
			newest[k.editionID] = p.SeenAt
		}
	}
	r := Ranges{
		floors:       map[int64]*Estimate{},
		mids:         make(map[group]*Estimate, len(used)),
		discogsMids:  map[int64]map[string]*Estimate{},
		discogsCeils: map[int64]*Estimate{},
	}
	for k, p := range latest {
		e := &Estimate{Cents: p.Cents, Source: estimateSources[k.source], Count: 1}
		switch k.source {
		case SourceSkupSzop, SourceDiscogsLowest:
			r.floors[k.editionID] = e
		case SourceDiscogsMint:
			r.discogsCeils[k.editionID] = e
		default:
			if !p.SeenAt.Equal(newest[k.editionID]) {
				continue
			}
			if r.discogsMids[k.editionID] == nil {
				r.discogsMids[k.editionID] = map[string]*Estimate{}
			}
			r.discogsMids[k.editionID][k.source] = e
		}
	}
	for g, v := range used {
		r.mids[g] = trimmedMedian(v, SourceOLX)
	}
	return r
}

// For is the range of one edition of titleID in format, for a copy
// without a condition.
func (r Ranges) For(editionID, titleID int64, format *string) Range {
	out, _ := r.ForCopy(editionID, titleID, format, nil)
	return out
}

// ForCopy is the range of a copy of one edition of titleID in format in
// condition, with the grade of the Discogs suggestion its middle is, or ""
// when the middle is none. The suggestion of the grade the condition maps
// to is the middle; without a condition or a suggestion for its grade,
// the Very Good Plus one is.
func (r Ranges) ForCopy(editionID, titleID int64, format, condition *string) (Range, string) {
	g := groupOf(titleID, format)
	out := Range{Floor: r.floors[editionID], Mid: r.mids[g], Ceiling: r.discogsCeils[editionID]}
	suggested := r.discogsMids[editionID]
	grade := GradeVGPlus
	if condition != nil {
		if c, ok := conditionGrades[*condition]; ok && suggested[gradeSources[c]] != nil {
			grade = c
		}
	}
	e := suggested[gradeSources[grade]]
	if e == nil {
		return out, ""
	}
	out.Mid = e
	return out, grade
}

// Market is the used price of one title in one format: the middle a copy
// of it gets before a Discogs suggestion replaces it. A null Format is a
// title whose editions name none, such as a book.
type Market struct {
	TitleID int64   `json:"title_id"`
	Format  *string `json:"format" ts:"Format"`
	Estimate
}

// Markets lists the middle of every title and format with used prices,
// by title, then format.
func (r Ranges) Markets() []Market {
	groups := slices.SortedFunc(maps.Keys(r.mids), func(a, b group) int {
		return cmp.Or(cmp.Compare(a.titleID, b.titleID), cmp.Compare(a.format, b.format))
	})
	out := make([]Market, 0, len(groups))
	for _, g := range groups {
		e := r.mids[g]
		if e == nil {
			continue
		}
		m := Market{TitleID: g.titleID, Estimate: *e}
		if g.format != "" {
			m.Format = &g.format
		}
		out = append(out, m)
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
	CopyID     int64   `json:"copy_id"`
	EditionID  int64   `json:"edition_id"`
	TitleID    int64   `json:"title_id"`
	TitleName  string  `json:"title_name"`
	Kind       string  `json:"kind" ts:"Kind"`
	Format     *string `json:"format" ts:"Format"`
	Barcode    *string `json:"barcode"`
	PriceCents *int64  `json:"price_cents"`
	CoverURL   *string `json:"cover_url,omitempty"`
	// DiscogsReleaseID is the album edition's Discogs release, so a price
	// from Discogs can credit it.
	DiscogsReleaseID *int64    `json:"discogs_release_id,omitempty" ts:",null"`
	Floor            *Estimate `json:"floor"`
	Mid              *Estimate `json:"mid"`
	Ceiling          *Estimate `json:"ceiling"`
	// MidGrade is the grade of the Discogs suggestion the middle is;
	// absent when the middle is no suggestion.
	MidGrade      string `json:"mid_grade,omitempty" ts:"DiscogsGrade"`
	LowConfidence bool   `json:"low_confidence"`
	// Condition is the copy's condition, which picks its Discogs grade.
	Condition *string `json:"-"`
}

// Bound is one bound summed over the collection: the total of the copies
// that have it and how many copies that is.
type Bound struct {
	Cents  int64 `json:"cents"`
	Copies int   `json:"copies"`
}

// Totals is the collection's value range against what was paid for it,
// as one day of valuation history keeps it.
type Totals struct {
	Copies        int   `json:"copies"`
	SpentCents    int64 `json:"spent_cents"`
	Floor         Bound `json:"floor"`
	Mid           Bound `json:"mid"`
	Ceiling       Bound `json:"ceiling"`
	LowConfidence int   `json:"low_confidence"`
}

// Gain compares the used value with the price paid over the copies that
// have both, so the two sums cover the same copies.
type Gain struct {
	Copies     int   `json:"copies"`
	MidCents   int64 `json:"mid_cents"`
	SpentCents int64 `json:"spent_cents"`
}

// Summary is the collection's totals now, with its gain.
type Summary struct {
	Totals
	Gain Gain `json:"gain"`
}

// Value fills each copy's range from ranges, at the copy's condition.
func Value(copies []CopyValue, ranges Ranges) {
	for i := range copies {
		c := &copies[i]
		r, grade := ranges.ForCopy(c.EditionID, c.TitleID, c.Format, c.Condition)
		c.Floor, c.Mid, c.Ceiling = r.Floor, r.Mid, r.Ceiling
		c.MidGrade = grade
		c.LowConfidence = r.LowConfidence()
	}
}

// Summarize totals valued copies.
func Summarize(copies []CopyValue) Summary {
	s := Summary{Totals: Totals{Copies: len(copies)}}
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
			if c.Mid != nil {
				s.Gain.Copies++
				s.Gain.MidCents += c.Mid.Cents
				s.Gain.SpentCents += *c.PriceCents
			}
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
