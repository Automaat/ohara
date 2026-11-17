package deals

import (
	"slices"
	"testing"
)

var defaultRules = Rules{
	Reject: []string{"pudełko", "okładka", "uszkodzon", "kupię", "zamienię", "wymienię", "plakat", "soundtrack"},
	Accept: []string{"twarda okładka", "miękka okładka"},
}

func bladeRunner(format string) Want {
	return Want{
		CopyID: 1, EditionID: 10, TitleID: 100, Kind: "movie", Name: "Blade Runner",
		PolishTitle: new("Łowca androidów"), OriginalTitle: new("Blade Runner"), Format: new(format),
	}
}

func wiedzmin() Want {
	return Want{
		CopyID: 2, EditionID: 20, TitleID: 200, Kind: "book", Name: "Ostatnie życzenie",
		Authors: []string{"Andrzej Sapkowski"},
	}
}

func TestWantMatches(t *testing.T) {
	tests := []struct {
		name  string
		want  Want
		title string
		match bool
	}{
		{"original title with format", bladeRunner("bluray"), "Blade Runner The Final Cut BluRay", true},
		{"polish title folded", bladeRunner("bluray"), "ŁOWCA ANDROIDÓW [Blu-ray] lektor", true},
		{"blu ray with a space", bladeRunner("bluray"), "Lowca androidow blu ray PL", true},
		{"wrong format", bladeRunner("bluray"), "Blade Runner DVD sama płyta", false},
		{"4k combo is uhd, not bluray", bladeRunner("bluray"), "Blade Runner 4K UHD + Blu-ray", false},
		{"4k combo for an uhd want", bladeRunner("uhd"), "Łowca Androidów (Blade Runner: The Final Cut) 4K UHD + Blu-ray", true},
		{"dvd inside a word", bladeRunner("dvd"), "Blade Runner Metalbox 5xDVD z PL", true},
		{"no format word", bladeRunner("dvd"), "Blade Runner - Philip K. Dick", false},
		{"missing title word", bladeRunner("bluray"), "Runner blu-ray", false},
		{"book with surname", wiedzmin(), "Ostatnie Życzenie - Sapkowski, twarda", true},
		{"book without author", wiedzmin(), "Ostatnie życzenie książka", false},
		{"book title missing", wiedzmin(), "Sapkowski Krew elfów", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.want.Matches(tt.title); got != tt.match {
				t.Fatalf("Matches(%q) = %v, want %v", tt.title, got, tt.match)
			}
		})
	}
}

func TestRulesRejected(t *testing.T) {
	tests := []struct {
		title  string
		reject bool
	}{
		{"Blade Runner 2049 samo metalowe pudełko steelbook", true},
		{"Kupię Blade Runner blu-ray", true},
		{"Plakat filmowy oryginalny - Blade Runner 2049", true},
		{"Płyta uszkodzona Blade Runner", true},
		{"Blade Runner blu-ray stan idealny", false},
		{"Ostatnie życzenie Sapkowski twarda okładka", false},
		{"Ostatnie życzenie sama okładka", true},
		{"MIĘKKA OKŁADKA, okładka lekko przetarta", true},
	}
	for _, tt := range tests {
		t.Run(tt.title, func(t *testing.T) {
			if got := defaultRules.Rejected(tt.title); got != tt.reject {
				t.Fatalf("Rejected(%q) = %v, want %v", tt.title, got, tt.reject)
			}
		})
	}
}

func TestWantQueries(t *testing.T) {
	tests := []struct {
		name string
		want Want
		out  []string
	}{
		{"movie names with format", bladeRunner("bluray"), []string{"Łowca androidów blu-ray", "Blade Runner blu-ray"}},
		{"uhd keyword", bladeRunner("uhd"), []string{"Łowca androidów 4k", "Blade Runner 4k"}},
		{"book with surname", wiedzmin(), []string{"Ostatnie życzenie sapkowski"}},
		{"book without authors", Want{Kind: "book", Name: "Lalka"}, []string{"Lalka"}},
		{"name already has the keyword", Want{Kind: "movie", Name: "Seria DVD", Format: new("dvd")}, []string{"Seria DVD"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.want.Queries(); !slices.Equal(got, tt.out) {
				t.Fatalf("Queries() = %q, want %q", got, tt.out)
			}
		})
	}
}

func TestPickPrefersAffordableWant(t *testing.T) {
	cheap := bladeRunner("bluray")
	cheap.CopyID, cheap.MaxPriceCents = 1, new(int64(3000))
	generous := bladeRunner("bluray")
	generous.CopyID, generous.MaxPriceCents = 2, new(int64(6000))
	wants := []Want{cheap, generous}

	l := Listing{Title: "Blade Runner Blu-ray", PriceCents: 4500}
	if got := pick(wants, &l, defaultRules); got == nil || got.CopyID != 2 {
		t.Fatalf("pick = %+v, want copy 2", got)
	}
	l.PriceCents = 9000
	if got := pick(wants, &l, defaultRules); got == nil || got.CopyID != 1 {
		t.Fatalf("pick over every limit = %+v, want the first match", got)
	}
	l.Title = "Kupię Blade Runner Blu-ray"
	if got := pick(wants, &l, defaultRules); got != nil {
		t.Fatalf("pick rejected listing = %+v, want nil", got)
	}
}

func TestSearchQueriesDeduplicates(t *testing.T) {
	a, b := bladeRunner("bluray"), bladeRunner("bluray")
	b.CopyID = 2
	got := searchQueries([]Want{a, b, wiedzmin()})
	want := []string{"Łowca androidów blu-ray", "Blade Runner blu-ray", "Ostatnie życzenie sapkowski"}
	if !slices.Equal(got, want) {
		t.Fatalf("searchQueries = %q, want %q", got, want)
	}
}

func TestFormatPLN(t *testing.T) {
	for cents, want := range map[int64]string{0: "0,00 zł", 4599: "45,99 zł", 123450: "1 234,50 zł", 100000000: "1 000 000,00 zł"} {
		if got := formatPLN(cents); got != want {
			t.Errorf("formatPLN(%d) = %q, want %q", cents, got, want)
		}
	}
}

func TestParsePrice(t *testing.T) {
	tests := []struct {
		in    string
		cents int64
		ok    bool
	}{
		{"3 000,21zł", 300021, true},
		{"45 zł", 4500, true},
		{"33.7", 3370, true},
		{"1.234,50", 123450, true},
		{"za darmo", 0, false},
		{"2 000 000 zł", 0, false},
	}
	for _, tt := range tests {
		cents, ok := parsePrice(tt.in)
		if cents != tt.cents || ok != tt.ok {
			t.Errorf("parsePrice(%q) = %d, %v; want %d, %v", tt.in, cents, ok, tt.cents, tt.ok)
		}
	}
}
