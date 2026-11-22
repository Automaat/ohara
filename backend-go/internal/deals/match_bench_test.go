package deals

import (
	"fmt"
	"testing"
)

// benchWants are n wants as a run holds them: two movies in every three,
// each with a Polish and an original title, and a book by Jan Kowalski.
func benchWants(n int) []Want {
	wants := make([]Want, n)
	formats := []string{"dvd", "bluray", "uhd"}
	for i := range wants {
		f := formats[i%3]
		pl := fmt.Sprintf("Polski tytuł numer %d", i)
		orig := fmt.Sprintf("Original Title %d", i)
		wants[i] = Want{
			CopyID: int64(i + 1), TitleID: int64(i + 1), Kind: "movie", Name: fmt.Sprintf("Some Movie Title %d", i),
			PolishTitle: &pl, OriginalTitle: &orig, Format: &f,
		}
		if i%3 == 1 {
			wants[i] = Want{
				CopyID: int64(i + 1), TitleID: int64(i + 1), Kind: "book", Name: fmt.Sprintf("Książka o czymś %d", i),
				Authors: []string{"Jan Kowalski"},
			}
		}
	}
	return wants
}

var benchRules = Rules{
	Reject: []string{"pudełko", "okładka", "uszkodzon", "kupię", "zamienię", "wymienię", "plakat", "soundtrack", "odtwarzacz", "moduł"},
	Accept: []string{"twarda okładka", "miękka okładka"},
}

// BenchmarkPick1000 picks the want of one listing among 1,000, prepared
// once as a hunt prepares them.
func BenchmarkPick1000(b *testing.B) {
	wants := benchWants(1000)
	titles := NewTitles(benchWants(1000))
	for i := range wants {
		wants[i].Prepare()
	}
	l := &Listing{Title: "Łowca Androidów DVD PL Edycja kolekcjonerska Kowalski", PriceCents: 1000}
	b.ReportAllocs()
	for b.Loop() {
		_ = pick(wants, l, benchRules, titles)
	}
}

// BenchmarkPick1000Matching picks among 1,000 wishes that all match the
// listing and none of whose limits it meets, so pick checks every one.
func BenchmarkPick1000Matching(b *testing.B) {
	wants := make([]Want, 1000)
	for i := range wants {
		wants[i] = Want{CopyID: int64(i + 1), TitleID: 7, Kind: "movie", Name: "Alien", Format: new("dvd")}
		wants[i].Prepare()
	}
	titles := NewTitles([]Want{{TitleID: 7, Kind: "movie", Name: "Alien"}, {TitleID: 8, Kind: "movie", Name: "Alien: Covenant"}})
	l := &Listing{Title: "Alien DVD", PriceCents: 1000}
	b.ReportAllocs()
	for b.Loop() {
		_ = pick(wants, l, benchRules, titles)
	}
}

// BenchmarkPick1000WrongMedium matches a CD listing against 1,000 vinyl
// wishes of the same album, each turned away by its medium.
func BenchmarkPick1000WrongMedium(b *testing.B) {
	wants := make([]Want, 1000)
	for i := range wants {
		wants[i] = Want{CopyID: int64(i + 1), TitleID: int64(i + 1), Kind: "music", Name: "Greatest Hits", Format: new("vinyl")}
		wants[i].Prepare()
	}
	l := &Listing{Title: "Greatest Hits CD", PriceCents: 1000}
	b.ReportAllocs()
	for b.Loop() {
		_ = pick(wants, l, benchRules, Titles{})
	}
}

// BenchmarkPick1000LongNumber picks among 1,000 matching wishes for a
// listing whose number is past any int.
func BenchmarkPick1000LongNumber(b *testing.B) {
	wants := make([]Want, 1000)
	for i := range wants {
		wants[i] = Want{CopyID: int64(i + 1), TitleID: 1, Kind: "movie", Name: "Alien", Format: new("dvd")}
		wants[i].Prepare()
	}
	l := &Listing{Title: "Alien 99999999999999999999 DVD", PriceCents: 2000}
	b.ReportAllocs()
	for b.Loop() {
		_ = pick(wants, l, Rules{}, Titles{})
	}
}
