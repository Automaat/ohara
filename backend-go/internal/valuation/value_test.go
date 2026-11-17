package valuation

import (
	"reflect"
	"testing"
	"time"
)

func TestTrimmedMedian(t *testing.T) {
	tests := []struct {
		name   string
		values []int64
		want   *Estimate
	}{
		{"none", nil, nil},
		{"one", []int64{2500}, &Estimate{Cents: 2500, Source: SourceOLX, Count: 1}},
		{"even count averages the middle two", []int64{1000, 3000}, &Estimate{Cents: 2000, Source: SourceOLX, Count: 2}},
		{"three keep every value", []int64{100, 2000, 90000}, &Estimate{Cents: 2000, Source: SourceOLX, Count: 3}},
		{
			"outliers beyond the fences are dropped",
			[]int64{2000, 2200, 2500, 2400, 2300, 100, 99000},
			&Estimate{Cents: 2300, Source: SourceOLX, Count: 5, Trimmed: 2},
		},
		{
			"tight values keep every one",
			[]int64{2000, 2100, 2200, 2300},
			&Estimate{Cents: 2150, Source: SourceOLX, Count: 4},
		},
		{"identical values", []int64{1500, 1500, 1500, 1500, 1500}, &Estimate{Cents: 1500, Source: SourceOLX, Count: 5}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := trimmedMedian(tt.values, SourceOLX); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("trimmedMedian(%v) = %+v, want %+v", tt.values, got, tt.want)
			}
		})
	}
}

func TestRanges(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	used, fresh := "used", "new"
	bluray, dvd := "bluray", "dvd"
	prices := []Price{
		{EditionID: 1, TitleID: 100, Source: SourceOLX, Cents: 2000, AcquiredAs: &used, SeenAt: now},
		{EditionID: 1, TitleID: 100, Source: SourceOLX, Cents: 3000, SeenAt: now},
		{EditionID: 1, TitleID: 100, Source: SourceOLX, Cents: 9900, AcquiredAs: &fresh, SeenAt: now},
		{EditionID: 2, TitleID: 100, Source: SourceOLX, Cents: 2500, AcquiredAs: &used, SeenAt: now},
		{EditionID: 1, TitleID: 100, Source: SourceCeneo, Cents: 5000, AcquiredAs: &fresh, SeenAt: now},
		{EditionID: 2, TitleID: 100, Source: SourceCeneo, Cents: 6000, AcquiredAs: &fresh, SeenAt: now},
		{EditionID: 1, TitleID: 100, Source: SourceSkupSzop, Cents: 400, SeenAt: now.Add(-time.Hour)},
		{EditionID: 1, TitleID: 100, Source: SourceSkupSzop, Cents: 732, SeenAt: now},
		{EditionID: 2, TitleID: 100, Source: SourceSkupSzop, Cents: 0, SeenAt: now},
		{EditionID: 3, TitleID: 200, Format: &bluray, Source: SourceOLX, Cents: 4000, AcquiredAs: &used, SeenAt: now},
		{EditionID: 5, TitleID: 300, Source: "pepper", Cents: 100, SeenAt: now},
		{EditionID: 7, TitleID: 400, Source: SourceOLX, Cents: 2000, AcquiredAs: &used, SeenAt: now},
		{EditionID: 7, TitleID: 400, Source: SourceVinted, Cents: 3000, AcquiredAs: &used, SeenAt: now},
		{EditionID: 7, TitleID: 400, Source: SourceVinted, Cents: 2600, SeenAt: now},
		{EditionID: 7, TitleID: 400, Source: SourceVinted, Cents: 9000, AcquiredAs: &fresh, SeenAt: now},
		{EditionID: 8, TitleID: 500, Source: SourceVinted, Cents: 1500, AcquiredAs: &used, SeenAt: now},
	}
	r := NewRanges(prices)
	tests := []struct {
		name    string
		edition int64
		title   int64
		format  *string
		want    Range
	}{
		{
			name: "own floor, pooled middle and ceiling", edition: 1, title: 100,
			want: Range{
				Floor:   &Estimate{Cents: 732, Source: SourceSkupSzop, Count: 1},
				Mid:     &Estimate{Cents: 2500, Source: SourceOLX, Count: 3},
				Ceiling: &Estimate{Cents: 5500, Source: SourceCeneo, Count: 2},
			},
		},
		{
			name: "second printing shares the title's prices", edition: 2, title: 100,
			want: Range{
				Floor:   &Estimate{Cents: 0, Source: SourceSkupSzop, Count: 1},
				Mid:     &Estimate{Cents: 2500, Source: SourceOLX, Count: 3},
				Ceiling: &Estimate{Cents: 5500, Source: SourceCeneo, Count: 2},
			},
		},
		{
			name: "edition without its own prices", edition: 9, title: 100,
			want: Range{
				Mid:     &Estimate{Cents: 2500, Source: SourceOLX, Count: 3},
				Ceiling: &Estimate{Cents: 5500, Source: SourceCeneo, Count: 2},
			},
		},
		{
			name: "same format pools", edition: 4, title: 200, format: &bluray,
			want: Range{Mid: &Estimate{Cents: 4000, Source: SourceOLX, Count: 1}},
		},
		{name: "other format does not", edition: 6, title: 200, format: &dvd, want: Range{}},
		{name: "other sources are ignored", edition: 5, title: 300, want: Range{}},
		{
			name: "OLX and Vinted pool used prices", edition: 7, title: 400,
			want: Range{Mid: &Estimate{Cents: 2600, Source: SourceUsed, Count: 3}},
		},
		{
			name: "Vinted alone", edition: 8, title: 500,
			want: Range{Mid: &Estimate{Cents: 1500, Source: SourceVinted, Count: 1}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := r.For(tt.edition, tt.title, tt.format); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("For(%d) = %+v, want %+v", tt.edition, got, tt.want)
			}
		})
	}
}

func TestLowConfidence(t *testing.T) {
	tests := []struct {
		name string
		mid  *Estimate
		want bool
	}{
		{"no middle", nil, true},
		{"too few prices", &Estimate{Count: MinObservations - 1}, true},
		{"enough prices", &Estimate{Count: MinObservations}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (Range{Mid: tt.mid}).LowConfidence(); got != tt.want {
				t.Errorf("LowConfidence = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValueAndSummarize(t *testing.T) {
	paid := int64(4000)
	copies := []CopyValue{
		{CopyID: 1, EditionID: 1, TitleID: 1, PriceCents: &paid},
		{CopyID: 2, EditionID: 1, TitleID: 1},
		{CopyID: 3, EditionID: 2, TitleID: 2, PriceCents: &paid},
	}
	used := "used"
	prices := []Price{{EditionID: 1, TitleID: 1, Source: SourceSkupSzop, Cents: 700}}
	for _, c := range []int64{2400, 2500, 2500, 2600} {
		prices = append(prices, Price{EditionID: 1, TitleID: 1, Source: SourceOLX, Cents: c, AcquiredAs: &used})
	}
	prices = append(prices, Price{EditionID: 1, TitleID: 1, Source: SourceCeneo, Cents: 6000})
	Value(copies, NewRanges(prices))
	if copies[0].Mid == nil || copies[1].Ceiling == nil || copies[2].Mid != nil {
		t.Fatalf("Value left ranges %+v", copies)
	}
	if copies[0].LowConfidence || !copies[2].LowConfidence {
		t.Fatalf("confidence = %v, %v", copies[0].LowConfidence, copies[2].LowConfidence)
	}
	got := Summarize(copies)
	want := Summary{
		Copies:        3,
		SpentCents:    8000,
		Floor:         Bound{Cents: 1400, Copies: 2},
		Mid:           Bound{Cents: 5000, Copies: 2},
		Ceiling:       Bound{Cents: 12000, Copies: 2},
		LowConfidence: 1,
	}
	if got != want {
		t.Fatalf("Summarize = %+v, want %+v", got, want)
	}
	if empty := Summarize(nil); empty != (Summary{}) {
		t.Fatalf("Summarize(nil) = %+v", empty)
	}
}

func TestParsePLN(t *testing.T) {
	tests := []struct {
		in   string
		want int64
		ok   bool
	}{
		{"7,32 zł", 732, true},
		{"1 000,00 zł", 100000, true},
		{"0,29 zł", 29, true},
		{"12 zł", 1200, true},
		{"zł", 0, false},
		{"2 000 000,00 zł", 0, false},
	}
	for _, tt := range tests {
		got, ok := parsePLN(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("parsePLN(%q) = %d, %v, want %d, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestIsISBN(t *testing.T) {
	s := func(v string) *string { return &v }
	tests := []struct {
		in   *string
		want bool
	}{
		{nil, false},
		{s("9788308074770"), true},
		{s("9790000000001"), true},
		{s("5902600069706"), false},
		{s("97883080747"), false},
	}
	for _, tt := range tests {
		if got := isISBN(tt.in); got != tt.want {
			t.Errorf("isISBN(%v) = %v, want %v", tt.in, got, tt.want)
		}
	}
}
