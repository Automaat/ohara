package deals

import (
	"slices"
	"strings"
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

func rocky(part string) Want {
	return Want{CopyID: 3, Kind: "movie", Name: strings.TrimSpace("Rocky " + part), Format: new("dvd")}
}

func album(name, artist, format string) Want {
	return Want{CopyID: 5, EditionID: 50, TitleID: 500, Kind: "music", Name: name, Authors: []string{artist}, Format: new(format)}
}

func TestAlbumMatches(t *testing.T) {
	wall := album("The Wall", "Pink Floyd", "cd")
	tests := []struct {
		name  string
		want  Want
		title string
		match bool
	}{
		{"artist, album and medium", wall, "Pink Floyd - The Wall 2CD", true},
		{"cd listing naming no medium", wall, "PINK FLOYD The Wall stan bdb", true},
		{"vinyl listing for a cd want", wall, "Pink Floyd The Wall winyl 2LP", false},
		{"cassette listing for a cd want", wall, "Pink Floyd The Wall kaseta MC", false},
		{"film disc for a cd want", wall, "Pink Floyd The Wall DVD film Alan Parker", false},
		{"artist missing", wall, "The Wall CD", false},
		{"album missing", wall, "Pink Floyd Animals CD", false},
		{"vinyl want needs a vinyl listing", album("The Wall", "Pink Floyd", "vinyl"), "Pink Floyd The Wall LP 1979", true},
		{"vinyl want skips an unnamed medium", album("The Wall", "Pink Floyd", "vinyl"), "Pink Floyd The Wall", false},
		{"artist stop words dropped", album("Abbey Road", "The Beatles", "cd"), "Beatles Abbey Road CD remaster", true},
		{"artist word is no medium", album("Please Hammer Don't Hurt 'Em", "MC Hammer", "cd"), "MC Hammer Please Hammer Don't Hurt 'Em CD", true},
		{"artist word is no cassette", album("Please Hammer Don't Hurt 'Em", "MC Hammer", "cassette"), "MC Hammer Please Hammer Don't Hurt 'Em CD", false},
		{"cassette named past the artist", album("Please Hammer Don't Hurt 'Em", "MC Hammer", "cassette"), "MC Hammer Please Hammer Don't Hurt 'Em kaseta", true},
		{"album title is no medium", album("Vinyl", "Dark Star", "cd"), "Dark Star Vinyl CD", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.want.Matches(tt.title); got != tt.match {
				t.Fatalf("Matches(%q) = %v, want %v", tt.title, got, tt.match)
			}
		})
	}
}

func TestSoundtrackAlbumSurvivesTheRejectPhrase(t *testing.T) {
	score := album("Interstellar Original Motion Picture Soundtrack", "Hans Zimmer", "cd")
	film := Want{Kind: "movie", Name: "Interstellar", Format: new("dvd")}
	l := &Listing{Title: "Hans Zimmer Interstellar Original Motion Picture Soundtrack CD", PriceCents: 2000}
	if got := pick([]Want{film, score}, l, defaultRules, Titles{}); got == nil || got.Kind != "music" {
		t.Fatalf("pick = %+v, want the soundtrack album", got)
	}
	if got := pick([]Want{film}, &Listing{Title: "Interstellar soundtrack DVD"}, defaultRules, Titles{}); got != nil {
		t.Fatalf("soundtrack listing matched the film: %+v", got)
	}
	kupie := &Listing{Title: "Kupię Hans Zimmer Interstellar Original Motion Picture Soundtrack CD"}
	if got := pick([]Want{score}, kupie, defaultRules, Titles{}); got != nil {
		t.Fatalf("other reject phrases still apply: %+v", got)
	}
}

func TestPickSkipsOtherSequel(t *testing.T) {
	wants := []Want{rocky("2"), rocky("3")}
	wants[1].CopyID = 4
	got := pick(wants, &Listing{Title: "Rocky 3 DVD", PriceCents: 1000}, Rules{}, Titles{})
	if got == nil || got.CopyID != 4 {
		t.Fatalf("pick(Rocky 3 DVD) = %+v, want the Rocky 3 copy", got)
	}
}

func TestWantMatches(t *testing.T) {
	dune := Want{Kind: "movie", Name: "Diuna", OriginalTitle: new("Dune"), Format: new("bluray")}
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
		{"sequel number kept", rocky("2"), "Rocky 2 DVD lektor", true},
		{"other sequel number", rocky("2"), "Rocky 3 DVD", false},
		{"sequel of the original", bladeRunner("bluray"), "Blade Runner 2049 Blu-ray", false},
		{"roman sequel of the original", rocky(""), "Rocky II DVD", false},
		{"release year after the title", bladeRunner("bluray"), "Blade Runner 1982 Blu-ray", true},
		{"sequel wanted by its number", Want{Kind: "movie", Name: "Blade Runner 2049", Format: new("bluray")}, "Blade Runner 2049 Blu-ray", true},
		{"number elsewhere in the listing", bladeRunner("dvd"), "Blade Runner DVD 2 płyty", true},
		{"english part two", dune, "Dune: Part Two (Diuna: Część druga) (Blu-ray)", false},
		{"polish część druga", dune, "Diuna. Część Druga, Blu-ray. Nowy Produkt", false},
		{"part number", dune, "Dune Part 2 Blu-ray", false},
		{"the first film", dune, "Diuna, Blu-ray - Denis Villeneuve", true},
		{"part one is the first film", dune, "Dune: Part One Blu-ray", true},
		{"part 1 is the first film", dune, "Dune Part 1 Blu-ray", true},
		{"część 1 is the first film", dune, "Diuna część 1 Blu-ray", true},
		{"part I is the first film", dune, "Dune Part I Blu-ray", true},
		{"ordinal without part is a word", Want{Kind: "movie", Name: "Dzień", Format: new("dvd")}, "Dzień drugi DVD", true},
		{"sequel wanted by its part", Want{Kind: "movie", Name: "Diuna: Część druga", Format: new("bluray")}, "Diuna. Część Druga, Blu-ray", true},
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
		{"album after its artist", album("The Wall", "Pink Floyd", "cd"), []string{"Pink Floyd The Wall"}},
		{"self-titled album", album("Metallica", "Metallica", "cd"), []string{"Metallica"}},
		{"album without artist", Want{Kind: "music", Name: "Kind of Blue", Format: new("cd")}, []string{"Kind of Blue"}},
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
	if got := pick(wants, &l, defaultRules, Titles{}); got == nil || got.CopyID != 2 {
		t.Fatalf("pick = %+v, want copy 2", got)
	}
	l.PriceCents = 9000
	if got := pick(wants, &l, defaultRules, Titles{}); got == nil || got.CopyID != 1 {
		t.Fatalf("pick over every limit = %+v, want the first match", got)
	}
	l.Title = "Kupię Blade Runner Blu-ray"
	if got := pick(wants, &l, defaultRules, Titles{}); got != nil {
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

func obcy() Want {
	return Want{
		CopyID: 11, EditionID: 110, TitleID: 1100, Kind: "movie", Name: "Obcy - 8. pasażer Nostromo",
		OriginalTitle: new("Alien"), Format: new("dvd"),
	}
}

func covenant() Want {
	return Want{CopyID: 12, EditionID: 120, TitleID: 1200, Kind: "movie", Name: "Alien: Covenant", Format: new("dvd")}
}

func TestPickMostSpecific(t *testing.T) {
	limited := obcy()
	limited.MaxPriceCents = new(int64(2500))
	gladiator := Want{CopyID: 13, TitleID: 1300, Kind: "movie", Name: "Gladiator", Format: new("dvd")}
	gladiator2 := Want{TitleID: 1301, Kind: "movie", Name: "Gladiator 2"}
	twin := obcy()
	twin.CopyID, twin.TitleID, twin.MaxPriceCents = 14, 1400, new(int64(3000))
	wall := album("The Wall", "Pink Floyd", "cd")
	wallLive := Want{TitleID: 501, Kind: "music", Name: "The Wall Live", Authors: []string{"Roger Waters"}}
	both := NewTitles([]Want{obcy(), covenant()})

	tests := []struct {
		name     string
		wants    []Want
		titles   Titles
		title    string
		cents    int64
		wantCopy int64
	}{
		{"sequel goes to its own wish", []Want{obcy(), covenant()}, both, "Alien Covenant DVD", 2000, 12},
		{"in either wish order", []Want{covenant(), obcy()}, both, "Alien: Covenant DVD", 2000, 12},
		{"over an affordable limit", []Want{limited, covenant()}, both, "Alien Covenant DVD", 2000, 12},
		{"over an affordable limit listed later", []Want{covenant(), limited}, both, "Alien Covenant DVD", 2000, 12},
		{"known title without a wish", []Want{obcy()}, both, "Alien Covenant DVD", 2000, 0},
		{"Polish name of the wanted film", []Want{obcy(), covenant()}, both, "Obcy 8. pasażer Nostromo DVD", 2000, 11},
		{"original name of the wanted film", []Want{obcy(), covenant()}, both, "Alien DVD Ridley Scott 1979", 2000, 11},
		{"without known titles", []Want{obcy()}, Titles{}, "Alien Covenant DVD", 2000, 11},
		{"scattered words of a longer title", []Want{gladiator}, NewTitles([]Want{gladiator, gladiator2}), "Gladiator DVD 2 płyty", 2000, 13},
		{"longer title as a phrase", []Want{gladiator}, NewTitles([]Want{gladiator, gladiator2}), "Gladiator 2 DVD", 2000, 0},
		{"equally specific titles keep the affordable want", []Want{limited, twin}, NewTitles([]Want{limited, twin}), "Alien DVD", 2800, 14},
		{"another artist's album", []Want{wall}, NewTitles([]Want{wall, wallLive}), "Pink Floyd The Wall Live CD", 2000, 5},
		{"another artist's album it names", []Want{wall}, NewTitles([]Want{wall, wallLive}), "Pink Floyd Roger Waters The Wall Live CD", 2000, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pick(tt.wants, &Listing{Title: tt.title, PriceCents: tt.cents}, defaultRules, tt.titles)
			switch {
			case tt.wantCopy == 0 && got != nil:
				t.Fatalf("pick(%q) = copy %d, want none", tt.title, got.CopyID)
			case tt.wantCopy != 0 && (got == nil || got.CopyID != tt.wantCopy):
				t.Fatalf("pick(%q) = %+v, want copy %d", tt.title, got, tt.wantCopy)
			}
		})
	}
}
