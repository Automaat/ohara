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
	prices := []Price{
		{EditionID: 1, Source: SourceOLX, Cents: 2000, AcquiredAs: &used, SeenAt: now},
		{EditionID: 1, Source: SourceOLX, Cents: 3000, SeenAt: now},
		{EditionID: 1, Source: SourceOLX, Cents: 9900, AcquiredAs: &fresh, SeenAt: now},
		{EditionID: 1, Source: SourceOLX, Cents: 2500, AcquiredAs: &used, SeenAt: now},
		{EditionID: 1, Source: SourceCeneo, Cents: 5000, AcquiredAs: &fresh, SeenAt: now},
		{EditionID: 1, Source: SourceCeneo, Cents: 6000, AcquiredAs: &fresh, SeenAt: now},
		{EditionID: 1, Source: SourceSkupSzop, Cents: 400, SeenAt: now.Add(-time.Hour)},
		{EditionID: 1, Source: SourceSkupSzop, Cents: 732, SeenAt: now},
		{EditionID: 2, Source: SourceSkupSzop, Cents: 0, SeenAt: now},
		{EditionID: 3, Source: "pepper", Cents: 100, SeenAt: now},
	}
	got := Ranges(prices)
	want := map[int64]Range{
		1: {
			Floor:   &Estimate{Cents: 732, Source: SourceSkupSzop, Count: 1},
			Mid:     &Estimate{Cents: 2500, Source: SourceOLX, Count: 3},
			Ceiling: &Estimate{Cents: 5500, Source: SourceCeneo, Count: 2},
		},
		2: {Floor: &Estimate{Cents: 0, Source: SourceSkupSzop, Count: 1}},
		3: {},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Ranges = %+v, want %+v", got, want)
	}
	if got[1].LowConfidence() {
		t.Error("three used prices should be confident")
	}
	if !got[2].LowConfidence() {
		t.Error("no used prices should be low confidence")
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
		{CopyID: 1, EditionID: 1, PriceCents: &paid},
		{CopyID: 2, EditionID: 1},
		{CopyID: 3, EditionID: 2, PriceCents: &paid},
	}
	ranges := map[int64]Range{
		1: {
			Floor:   &Estimate{Cents: 700, Source: SourceSkupSzop, Count: 1},
			Mid:     &Estimate{Cents: 2500, Source: SourceOLX, Count: 4},
			Ceiling: &Estimate{Cents: 6000, Source: SourceCeneo, Count: 1},
		},
	}
	Value(copies, ranges)
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
