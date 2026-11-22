package deals

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"golang.org/x/text/unicode/runenames"
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
		{"audiobook", audiobook("Ostatnie życzenie", "Andrzej Sapkowski"), []string{"Ostatnie życzenie sapkowski audiobook"}},
		{"audiobook already named", audiobook("Lalka audiobook", "Bolesław Prus"), []string{"Lalka audiobook prus"}},
		{"name already has the keyword", Want{Kind: "movie", Name: "Seria DVD", Format: new("dvd")}, []string{"Seria DVD"}},
		{"album after its artist", album("The Wall", "Pink Floyd", "cd"), []string{"Pink Floyd The Wall"}},
		{"self-titled album", album("Metallica", "Metallica", "cd"), []string{"Metallica"}},
		{"album without artist", Want{Kind: "music", Name: "Kind of Blue", Format: new("cd")}, []string{"Kind of Blue"}},
		{"surname in its own spelling", book("Sto lat samotności", "Gabriel García Márquez"), []string{"Sto lat samotności márquez"}},
		{"surname keeps a mark no letter takes", book("Zostań ze mną", "Ayọ̀bámi Adébáyọ̀"), []string{"Zostań ze mną adébáyọ̀"}},
		{"accent-only variants of a name", Want{Kind: "movie", Name: "Amélie", OriginalTitle: new("Amelie"), Format: new("dvd")}, []string{"Amelie dvd", "Amélie dvd"}},
		{"name has the surname unaccented", book("Sto lat samotności Marquez", "Gabriel García Márquez"), []string{"Sto lat samotności Marquez"}},
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

func TestSearchQueriesKeepSpellings(t *testing.T) {
	plain := Want{CopyID: 1, Kind: "movie", Name: "Amelie", Format: new("dvd")}
	accented := Want{CopyID: 2, Kind: "movie", Name: "Amélie", Format: new("dvd")}
	again := Want{CopyID: 3, Kind: "movie", Name: "AMÉLIE", Format: new("dvd")}
	got := searchQueries([]Want{plain, accented, again})
	want := []string{"Amelie dvd", "Amélie dvd"}
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
	alias := Want{
		CopyID: 15, TitleID: 1500, Kind: "movie", Name: "Obcy", PolishTitle: new("Obcy"),
		OriginalTitle: new("Alien"), Format: new("dvd"), MaxPriceCents: new(int64(2500)),
	}
	newYork := Want{CopyID: 16, TitleID: 1600, Kind: "movie", Name: "New York", Format: new("dvd"), MaxPriceCents: new(int64(2500))}
	newYorkNewYork := Want{CopyID: 17, TitleID: 1700, Kind: "movie", Name: "New York, New York", Format: new("dvd")}

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
		{"longer title extending another alias", []Want{alias, covenant()}, NewTitles([]Want{alias, covenant()}), "Obcy: Przymierze / Alien: Covenant DVD", 2000, 12},
		{"media word inside the longer title", []Want{limited, covenant()}, both, "Alien DVD Covenant", 2000, 12},
		{"disc count after the longer title", []Want{obcy()}, both, "Alien Covenant 2 płyty DVD", 2000, 0},
		{"repeated words", []Want{newYork, newYorkNewYork}, NewTitles([]Want{newYork, newYorkNewYork}), "New York New York DVD", 2000, 17},
		{"shorter title with repeated words", []Want{newYork, newYorkNewYork}, NewTitles([]Want{newYork, newYorkNewYork}), "New York DVD", 2000, 16},
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

func TestPreparedWantMatchesAsUnprepared(t *testing.T) {
	wants := []Want{
		bladeRunner("dvd"), bladeRunner("bluray"), wiedzmin(), rocky(""), rocky("2"),
		album("The Wall", "Pink Floyd", "cd"), album("Vinyl", "MC Hammer", "vinyl"), album("Interstellar", "Hans Zimmer", "cd"),
	}
	titles := NewTitles([]Want{
		{TitleID: 900, Kind: "movie", Name: "Blade Runner 2049"},
		{TitleID: 901, Kind: "music", Name: "The Wall Live", Authors: []string{"Pink Floyd"}},
	})
	listings := []string{
		"Łowca androidów DVD", "Blade Runner Blu-ray", "Blade Runner 2049 DVD", "Sapkowski Ostatnie życzenie twarda okładka",
		"Ostatnie życzenie pudełko Sapkowski", "Rocky II DVD", "Rocky 2 DVD", "Pink Floyd The Wall CD", "Pink Floyd The Wall Live CD",
		"MC Hammer Vinyl LP", "Hans Zimmer Interstellar soundtrack CD", "Kupię Rocky DVD",
		"Rocky 99999999999999999999 DVD", "Rocky 9223372036854775807 DVD",
	}
	for _, title := range listings {
		text := NewListingText(title, defaultRules)
		for i := range wants {
			plain := wants[i]
			prepared := wants[i]
			prepared.Prepare()
			if got, want := prepared.Matches(title), plain.Matches(title); got != want {
				t.Errorf("prepared %q Matches(%q) = %v, unprepared %v", plain.Name, title, got, want)
			}
			if got, want := defaultRules.RejectedFor(title, &prepared), defaultRules.RejectedFor(title, &plain); got != want {
				t.Errorf("prepared %q RejectedFor(%q) = %v, unprepared %v", plain.Name, title, got, want)
			}
			if got, want := titles.Outranks(&prepared, title), titles.Outranks(&plain, title); got != want {
				t.Errorf("prepared %q Outranks(%q) = %v, unprepared %v", plain.Name, title, got, want)
			}
			want := plain.Matches(title) && !defaultRules.RejectedFor(title, &plain) && !titles.Outranks(&plain, title)
			if got := text.Belongs(&prepared, titles); got != want {
				t.Errorf("Belongs(%q, %q) = %v, want %v", title, plain.Name, got, want)
			}
		}
	}
}

func TestPreparedWantChangedAfterwards(t *testing.T) {
	w := wiedzmin()
	w.Prepare()
	w.Name = "Miecz przeznaczenia"
	w.Authors = []string{"Jan Kowalski"}
	if w.Matches("Sapkowski Ostatnie życzenie") {
		t.Fatal("a want renamed after Prepare still matches its old name")
	}
	if !w.Matches("Miecz przeznaczenia Kowalski") {
		t.Fatal("a want renamed after Prepare does not match its new name")
	}
	if got := w.Queries(); !slices.Equal(got, []string{"Miecz przeznaczenia kowalski"}) {
		t.Fatalf("Queries() = %q, want the new name with the new surname", got)
	}
}

func TestNumberParsesAsAtoi(t *testing.T) {
	for _, word := range []string{
		"", "0", "007", "2", "1999", "2049", "9223372036854775807", "9223372036854775808",
		"99999999999999999999", "00000000000000000000001", "12a", "a12", "٢", "4k", "dvd",
	} {
		want, err := strconv.Atoi(word)
		got, ok := number(word)
		if ok != (err == nil) || (ok && got != want) {
			t.Errorf("number(%q) = %d, %v; strconv.Atoi = %d, %v", word, got, ok, want, err)
		}
	}
}

func TestLotsDoNotMatch(t *testing.T) {
	wall := album("The Wall", "Pink Floyd", "cd")
	trylogia := Want{Kind: "movie", Name: "Władca Pierścieni: Trylogia", Format: new("dvd")}
	tests := []struct {
		name  string
		want  Want
		title string
		match bool
	}{
		{"film set", bladeRunner("dvd"), "Zestaw 3 filmy DVD: Blade Runner, Gladiator, Obcy", false},
		{"film package", bladeRunner("dvd"), "Pakiet 10 płyt DVD Ridley Scott Blade Runner Gladiator Prometeusz", false},
		{"film collection", bladeRunner("dvd"), "Kolekcja filmów DVD 20 szt. Blade Runner, Matrix, Terminator", false},
		{"book volumes", wiedzmin(), "Wiedźmin komplet 8 tomów Sapkowski Ostatnie życzenie Miecz przeznaczenia", false},
		{"album discs", wall, "Pink Floyd 5 płyt CD The Wall Animals Meddle Wish You Were Here", false},
		{"one film on five discs", bladeRunner("dvd"), "Blade Runner Metalbox 5xDVD z PL", true},
		{"one film on two discs", bladeRunner("dvd"), "Blade Runner DVD 2 płyty", true},
		{"newspaper collection", bladeRunner("dvd"), "Blade Runner DVD Kolekcja Gazety Wyborczej", true},
		{"collector's edition", bladeRunner("bluray"), "Łowca Androidów Blu-Ray PL Edycja kolekcjonerska 30 rocznica", true},
		{"wished set", trylogia, "Władca Pierścieni Trylogia 3 filmy DVD", true},
		{"package holding the wished set", trylogia, "Pakiet DVD: Władca Pierścieni Trylogia 3 filmy + Hobbit Trylogia", false},
		{"set of two films", bladeRunner("dvd"), "Komplet DVD: Blade Runner + Gladiator", false},
		{"album discs past the name", album("White Album", "The Beatles", "cd"), "The Beatles White Album Super Deluxe – 6 płyt CD", true},
		{"book volumes past the name", Want{Kind: "book", Name: "Nędznicy", Authors: []string{"Victor Hugo"}}, "Nędznicy Victor Hugo – 5 tomów", true},
		{"formats of one film", bladeRunner("uhd"), "Blade Runner 4K UHD – zestaw 4K + Blu-ray", true},
		{"wished set word", Want{Kind: "book", Name: "Wiedźmin komplet", Authors: []string{"Andrzej Sapkowski"}}, "Komplet Wiedźmin Sapkowski 8 tomów", true},
		{"sequel on discs", Want{Kind: "movie", Name: "Diuna", Format: new("bluray")}, "Diuna część 2 – płyty Blu-ray", false},
		{"sequel before a disc word", bladeRunner("dvd"), "Blade Runner 2049 — płyty DVD", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NewListingText(tt.title, defaultRules).Belongs(&tt.want, Titles{}); got != tt.match {
				t.Fatalf("Belongs(%q) = %v, want %v", tt.title, got, tt.match)
			}
			got := tt.want.Matches(tt.title) && !defaultRules.RejectedFor(tt.title, &tt.want)
			if got != tt.match {
				t.Fatalf("Matches(%q) && !RejectedFor = %v, want %v", tt.title, got, tt.match)
			}
		})
	}
}

func book(name, author string) Want {
	return Want{CopyID: 6, EditionID: 60, TitleID: 600, Kind: "book", Name: name, Authors: []string{author}}
}

func audiobook(name, author string) Want {
	w := book(name, author)
	w.Format = new(FormatAudiobook)
	return w
}

func TestAudiobookMatches(t *testing.T) {
	audio := audiobook("Ostatnie życzenie", "Andrzej Sapkowski")
	paper := book("Ostatnie życzenie", "Andrzej Sapkowski")
	titled := func(want func(name, author string) Want) Want { return want("Audiobook", "Andrzej Sapkowski") }
	tests := []struct {
		name  string
		want  Want
		title string
		match bool
	}{
		{"audiobook names its medium", audio, "Ostatnie życzenie Sapkowski audiobook CD", true},
		{"audiobook in capitals", audio, "SAPKOWSKI OSTATNIE ŻYCZENIE AUDIOBOOK", true},
		{"genitive audiobooka", audio, "Sprzedam audiobooka Ostatnie życzenie Sapkowskiego", true},
		{"plural audiobooki", audio, "Audiobooki: Ostatnie życzenie - Sapkowski", true},
		{"audio book in two words", audio, "Ostatnie życzenie Sapkowski audio book mp3", true},
		{"polish audiobuk", audio, "Ostatnie życzenie Sapkowski audiobuk", true},
		{"audioksiążka", audio, "Audioksiążka Ostatnie życzenie Sapkowski", true},
		{"print listing for an audiobook", audio, "Ostatnie życzenie Sapkowski twarda oprawa", false},
		{"cd alone names no audiobook", audio, "Ostatnie życzenie Sapkowski CD", false},
		{"audiobook still needs the author", audio, "Ostatnie życzenie audiobook", false},
		{"print book ignores an audiobook", paper, "Ostatnie życzenie Sapkowski audiobook CD", false},
		{"print book ignores audiobooka", paper, "Ostatnie życzenie Sapkowskiego audiobooka", false},
		{"print book matches print", paper, "Ostatnie życzenie Sapkowski miękka oprawa", true},
		{"a title naming audiobooks is no medium", book("Audiobook", "Jan Kowalski"), "Audiobook Kowalski książka", true},
		{"an audiobook titled so needs another word", audiobook("Audiobook", "Jan Kowalski"), "Audiobook Kowalski książka", false},
		{"movies ignore the word", bladeRunner("dvd"), "Blade Runner DVD audiobook gratis", true},
		{"bez audiobooka names print", paper, "Ostatnie życzenie Sapkowskiego - książka bez audiobooka", true},
		{"bez audiobooka is no audiobook", audio, "Ostatnie życzenie Sapkowskiego - książka bez audiobooka", false},
		{"brak audio book is no audiobook", audio, "Ostatnie życzenie Sapkowski brak audio book", false},
		{"audiobook before a short title", audiobook("To", "Stephen King"), "Audiobook To Stephen King", true},
		{"short title before audiobook", audiobook("To", "Stephen King"), "To Stephen King audiobook", true},
		{"short print title after audiobook", book("To", "Stephen King"), "Audiobook To Stephen King", false},
		{"a title naming audiobooks, as an audiobook", titled(audiobook), "Audiobook Sapkowskiego — audiobook na CD", true},
		{"a title naming audiobooks, its audiobook is no print", titled(book), "Audiobook Sapkowskiego — audiobook na CD", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.want.Matches(tt.title); got != tt.match {
				t.Fatalf("Matches(%q) = %v, want %v", tt.title, got, tt.match)
			}
		})
	}
}

func TestSpellingVariantsMatch(t *testing.T) {
	tests := []struct {
		name  string
		want  Want
		title string
		match bool
	}{
		{"artist without accents", album("Homogenic", "Björk", "cd"), "Bjork Homogenic CD", true},
		{"listing with accents", album("Homogenic", "Bjork", "cd"), "Björk Homogenic CD", true},
		{"umlaut in a band name", album("Ace of Spades", "Motörhead", "vinyl"), "Motorhead Ace of Spades LP winyl", true},
		{"decomposed accents", album("Homogenic", "Björk", "cd"), "Björk Homogenic CD", true},
		{"author without accents", book("Sto lat samotności", "Gabriel García Márquez"), "Sto lat samotności Gabriel Garcia Marquez", true},
		{"title without accents", book("Sto lat samotności", "Gabriel García Márquez"), "Sto lat samotnosci Marquez", true},
		{"genitive of -ski", book("Ostatnie życzenie", "Andrzej Sapkowski"), "Ostatnie życzenie Andrzeja Sapkowskiego", true},
		{"genitive of a noun surname", book("Lalka", "Bolesław Prus"), "Lalka Prusa lektura", true},
		{"genitive after -cz", book("Pan Tadeusz", "Adam Mickiewicz"), "Pan Tadeusz Adama Mickiewicza", true},
		{"genitive of -ska", book("Wiersze wybrane", "Wisława Szymborska"), "Wiersze wybrane Wisławy Szymborskiej", true},
		{"genitive of a short surname", book("Solaris", "Stanisław Lem"), "Solaris Lema", true},
		{"genitive of -o", book("Zemsta", "Aleksander Fredro"), "Zemsta Fredry", true},
		{"another author", book("Ostatnie życzenie", "Andrzej Sapkowski"), "Ostatnie życzenie Pilipiuk", false},
		{"dative of -ski", book("Ostatnie życzenie", "Andrzej Sapkowski"), "Ostatnie życzenie, nagroda Sapkowskiemu", true},
		{"feminine form is another person", book("Ostatnie życzenie", "Andrzej Sapkowski"), "Ostatnie życzenie Sapkowska", false},
		{"genitive of a consonant surname", book("Papierowe miasta", "John Green"), "Papierowe miasta Johna Greena", true},
		{"a longer surname is another author", book("Papierowe miasta", "John Green"), "Papierowe miasta Graham Greene", false},
		{"surname ending in e", book("Moc i chwała", "Graham Greene"), "Moc i chwała Grahama Greene'a", true},
		{"adjective case of a surname ending in e", book("Cierpienia młodego Wertera", "Johann Wolfgang Goethe"), "Cierpienia młodego Wertera Goethego", true},
		{"fleeting e", book("Wojna z salamandrami", "Karel Čapek"), "Wojna z salamandrami Čapka", true},
		{"fleeting e, instrumental", book("Tango", "Sławomir Mrożek"), "Tango ze Sławomirem Mrożkiem", true},
		{"genitive of -owa", book("Nad Niemnem", "Eliza Orzeszkowa"), "Nad Niemnem Orzeszkowej", true},
		{"stroke letter folded", album("Homogenic", "Aǥa", "cd"), "Aga Homogenic CD", true},
		{"surname as a prefix of another word", book("Solaris", "Stanisław Lem"), "Solaris Lemański", false},
		{"short surname takes no ending", book("Imię róży", "Umberto Eco"), "Imię róży Ecoa", false},
		{"title words are not inflected", book("Lalka", "Bolesław Prus"), "Lalki Prusa", false},
		{"artist names are not inflected", album("Homogenic", "Björk", "cd"), "Homogenic Björka CD", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.want.Matches(tt.title); got != tt.match {
				t.Fatalf("Matches(%q) = %v, want %v", tt.title, got, tt.match)
			}
		})
	}
}

func TestShortTitles(t *testing.T) {
	m := Want{Kind: "movie", Name: "M", Format: new("bluray")}
	up := Want{Kind: "movie", Name: "Odlot", OriginalTitle: new("Up"), Format: new("dvd")}
	it := Want{Kind: "movie", Name: "To", OriginalTitle: new("It"), Format: new("bluray")}
	x := Want{Kind: "movie", Name: "X", Format: new("dvd")}
	et := Want{Kind: "movie", Name: "E.T.", Format: new("dvd")}
	mAlias := Want{Kind: "movie", Name: "M le maudit", OriginalTitle: new("M"), Format: new("bluray")}
	tests := []struct {
		name  string
		want  Want
		title string
		match bool
	}{
		{"one letter leading the listing", m, "M Fritz Lang Blu-ray", true},
		{"one letter inside another title", m, "Mission: Impossible 2 (M:I-2) Blu-ray", false},
		{"one letter after a separator", m, "Fritz Lang - M (1931) Blu-ray", true},
		{"one letter as an initial", m, "M. Night Shyamalan Znaki Blu-ray", false},
		{"one letter joined to a word", x, "X-Men DVD", false},
		{"original short title", up, "Up DVD Pixar", true},
		{"polish long title", up, "Odlot DVD Pixar", true},
		{"short title as a later word", up, "Step Up DVD taniec", false},
		{"short title starting a longer one", up, "Up in the Air DVD", false},
		{"short title after media words", up, "DVD Up Pixar", false},
		{"short title past a leading medium", up, "DVD: Up", true},
		{"short title before a year", it, "To (2017) Blu-ray lektor PL", true},
		{"short title before the medium", it, "It Blu-ray Stephen King", true},
		{"short title starting another film", it, "It Follows Blu-ray", false},
		{"short word starting a sentence", it, "To nie jest kraj dla starych ludzi Blu-ray", false},
		{"short title after a separator", it, "Stephen King - To (2017) Blu-ray", true},
		{"short title with an apostrophe", it, "It's a Wonderful Life Blu-ray", false},
		{"sequel of a short title", it, "To: Rozdział 2 Blu-ray", false},
		{"letters with dots", et, "E.T. DVD Spielberg", true},
		{"letters with dots before more words", et, "E.T. Spielberg DVD", false},
		{"album after its artist", album("21", "Adele", "cd"), "Adele 21 Deluxe Edition CD", true},
		{"album after a separator", album("21", "Adele", "cd"), "Adele - 21 (2011) CD", true},
		{"album number in another album", album("21", "Adele", "cd"), "Adele Live at Royal Albert Hall 21 utworów CD", false},
		{"book before its author", book("To", "Stephen King"), "To Stephen King wydanie kieszonkowe", true},
		{"book after its author", book("To", "Stephen King"), "Stephen King To", true},
		{"book starting another title", book("To", "Stephen King"), "To co zostało Stephen King", false},
		{"book before its declined author", book("To", "Stephen King"), "To Stephena Kinga, wydanie kieszonkowe", true},
		{"short title before a numbered medium", up, "Up DVD9 Pixar", true},
		{"short alias after a lot word", mAlias, "M:I Blu-ray - pakiet: M le maudit / Metropolis Blu-ray", false},
		{"lot word after a leading short alias", mAlias, "M Blu-ray - pakiet: M le maudit / Metropolis Blu-ray", false},
		{"letters split by a separator", et, "„E” / „T” DVD – dwa różne filmy", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.want.Matches(tt.title); got != tt.match {
				t.Fatalf("Matches(%q) = %v, want %v", tt.title, got, tt.match)
			}
		})
	}
}

func TestShortTitleOutranked(t *testing.T) {
	x := Want{CopyID: 18, TitleID: 1800, Kind: "movie", Name: "X", Format: new("dvd")}
	xMen := Want{TitleID: 1801, Kind: "movie", Name: "X-Men"}
	titles := NewTitles([]Want{x, xMen})
	for title, want := range map[string]bool{"X Men DVD": false, "X DVD Ti West": true, "X (2022) DVD": true} {
		if got := NewListingText(title, Rules{}).Belongs(&x, titles); got != want {
			t.Errorf("Belongs(%q) = %v, want %v", title, got, want)
		}
	}
}

func TestShortTitleOutranks(t *testing.T) {
	seven := Want{CopyID: 19, TitleID: 1900, Kind: "book", Name: "The 7", Authors: []string{"Bolesław Prus"}}
	sevenEight := Want{TitleID: 1901, Kind: "book", Name: "7 i 8", Authors: []string{"Bolesław Prus"}}
	titles := NewTitles([]Want{seven, sevenEight})
	for title, want := range map[string]bool{"7 i 8 Prusa – wydanie ilustrowane": false, "The 7 Prusa – wydanie ilustrowane": true} {
		if got := NewListingText(title, Rules{}).Belongs(&seven, titles); got != want {
			t.Errorf("Belongs(%q) = %v, want %v", title, got, want)
		}
	}
}

func TestRulesFoldDiacritics(t *testing.T) {
	rules := Rules{Reject: []string{"café", "pudelko"}, Accept: []string{"pudełko gratis"}}
	tests := []struct {
		title  string
		reject bool
	}{
		{"Homogenic CD cafe edition", true},
		{"Homogenic CD CAFÉ", true},
		{"Homogenic CD pudełko", true},
		{"Homogenic CD + pudelko gratis", false},
	}
	for _, tt := range tests {
		t.Run(tt.title, func(t *testing.T) {
			if got := rules.Rejected(tt.title); got != tt.reject {
				t.Fatalf("Rejected(%q) = %v, want %v", tt.title, got, tt.reject)
			}
		})
	}
}

func TestWordsFoldOnlyLatinLetters(t *testing.T) {
	tests := map[string][]string{
		"Łódź Żółć":            {"lodz", "zolc"},
		"Øresund Đorđe":        {"oresund", "dorde"},
		"Ñandú Çà":             {"nandu", "ca"},
		"Чайковский Ёлка":      {"чайковский", "ёлка"},
		"Adébáyọ̀":             {"adebayo"},
		"\u0301 Ojciec \u0301": {"ojciec"},
		"हिन्दी फ़िल्म":        {"हिन्दी", "फ़िल्म"},
		"Björk – CD":          {"bjork", "cd"},
	}
	for in, want := range tests {
		if got := words(in); !slices.Equal(got, want) {
			t.Errorf("words(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestWordsFoldEveryLatinDiacritic checks every Latin letter Unicode names
// "LATIN SMALL LETTER X WITH ..." (or its capital) folds to x.
func TestWordsFoldEveryLatinDiacritic(t *testing.T) {
	name := regexp.MustCompile(`^LATIN (?:SMALL|CAPITAL) LETTER ([A-Z]) WITH `)
	checked := 0
	for _, rt := range unicode.Latin.R16 {
		for r := rune(rt.Lo); r <= rune(rt.Hi); r += rune(rt.Stride) {
			checkFolds(t, name, r, &checked)
		}
	}
	for _, rt := range unicode.Latin.R32 {
		for r := rune(rt.Lo); r <= rune(rt.Hi); r += rune(rt.Stride) {
			checkFolds(t, name, r, &checked)
		}
	}
	if checked < 500 {
		t.Fatalf("checked %d letters, want the Latin script's letters with diacritics", checked)
	}
}

func checkFolds(t *testing.T, name *regexp.Regexp, r rune, checked *int) {
	t.Helper()
	n := runenames.Name(r)
	m := name.FindStringSubmatch(n)
	// "D WITH SMALL LETTER Z" is the title case of a digraph, no diacritic.
	if m == nil || strings.Contains(n, " WITH SMALL LETTER ") {
		return
	}
	*checked++
	// A capital Go does not lowercase is left alone: words lowercases first.
	if unicode.IsUpper(r) && unicode.ToLower(r) == r {
		return
	}
	if got, want := words(string(r)), []string{strings.ToLower(m[1])}; !slices.Equal(got, want) {
		t.Errorf("words(%q) (%U %s) = %q, want %q", string(r), r, n, got, want)
	}
}

// diuna is a "Diuna" book wish in an edition with barcode, nil for one
// without.
func diuna(barcode *string) Want {
	return Want{
		CopyID: 7, EditionID: 70, TitleID: 700, Kind: "book", Name: "Diuna",
		OriginalTitle: new("Dune"), Authors: []string{"Frank Herbert"}, Barcode: barcode,
	}
}

func TestPolishBookWants(t *testing.T) {
	solaris := Want{
		CopyID: 8, EditionID: 80, TitleID: 800, Kind: "book", Name: "Solaris",
		OriginalTitle: new("Solaris"), Authors: []string{"Stanisław Lem"}, Barcode: new("9788308049068"),
	}
	tests := []struct {
		name    string
		want    Want
		matches map[string]bool
		queries []string
	}{
		{"Polish ISBN", diuna(new("9788381880206")), map[string]bool{
			"Dune Frank Herbert paperback English":  false,
			"Frank Herbert - Dune (Ace Books 1990)": false,
			"Diuna Frank Herbert Rebis":             true,
		}, []string{"Diuna herbert"}},
		{"no barcode", diuna(nil), map[string]bool{
			"Dune Frank Herbert paperback English": false,
			"Diuna Frank Herbert Rebis":            true,
		}, []string{"Diuna herbert"}},
		{"English ISBN", diuna(new("9780441013593")), map[string]bool{
			"Frank Herbert - Dune (Ace Books 1990)": true,
			"Diuna Frank Herbert Rebis":             true,
		}, []string{"Dune herbert", "Diuna herbert"}},
		{"same original and Polish title", solaris, map[string]bool{"Solaris Lem": true}, []string{"Solaris lem"}},
		{"Polish prefix on a barcode that is no ISBN", diuna(new("97883128")), map[string]bool{
			"Frank Herbert - Dune (Ace Books 1990)": true,
		}, []string{"Dune herbert", "Diuna herbert"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for title, want := range tt.matches {
				if got := tt.want.Matches(title); got != want {
					t.Errorf("Matches(%q) = %v, want %v", title, got, want)
				}
			}
			if got := tt.want.Queries(); !slices.Equal(got, tt.queries) {
				t.Errorf("Queries() = %q, want %q", got, tt.queries)
			}
		})
	}
}

func TestPolishBookWantFollowsBarcodeChange(t *testing.T) {
	w := diuna(new("9780441013593"))
	w.Prepare()
	w.Barcode = new("9788381880206")
	if w.Matches("Dune Frank Herbert paperback English") {
		t.Error("a prepared want whose edition turned Polish still matches its original title")
	}
}

// TestKnownTitlesKeepOriginalTitles checks that a known title, which has
// no edition, keeps its original title for Outranks: "Dune Messiah" names
// the sequel, not the English "Dune" wished.
func TestKnownTitlesKeepOriginalTitles(t *testing.T) {
	dune := diuna(new("9780441013593"))
	messiah := Want{
		TitleID: 701, Kind: "book", Name: "Mesjasz Diuny", OriginalTitle: new("Dune Messiah"),
		Authors: []string{"Frank Herbert"},
	}
	titles := NewTitles([]Want{dune, messiah})
	const listing = "Dune Messiah Frank Herbert paperback"
	if !dune.Matches(listing) {
		t.Fatalf("English Dune wish does not match %q", listing)
	}
	if !titles.Outranks(&dune, listing) {
		t.Errorf("Outranks(%q) = false, want the known Dune Messiah to outrank Dune", listing)
	}
}
