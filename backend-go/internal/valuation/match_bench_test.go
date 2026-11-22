package valuation

import (
	"fmt"
	"testing"

	"github.com/Automaat/ohara/backend-go/internal/deals"
)

// BenchmarkMatch1000 matches one listing against 1,000 holdings, prepared
// once as a valuation run prepares them.
func BenchmarkMatch1000(b *testing.B) {
	hs := make([]Holding, 1000)
	wants := make([]deals.Want, len(hs))
	formats := []string{"dvd", "bluray", "uhd"}
	for i := range hs {
		f := formats[i%3]
		pl := fmt.Sprintf("Polski tytuł numer %d", i)
		orig := fmt.Sprintf("Original Title %d", i)
		hs[i] = Holding{Want: deals.Want{
			TitleID: int64(i + 1), Kind: "movie", Name: fmt.Sprintf("Some Movie Title %d", i),
			PolishTitle: &pl, OriginalTitle: &orig, Format: &f,
		}}
		if i%3 == 1 {
			hs[i].Want = deals.Want{
				TitleID: int64(i + 1), Kind: "book", Name: fmt.Sprintf("Książka o czymś %d", i),
				Authors: []string{"Jan Kowalski"},
			}
		}
		wants[i] = hs[i].Want
	}
	titles := deals.NewTitles(wants)
	for i := range hs {
		hs[i].Want.Prepare()
	}
	rules := deals.Rules{
		Reject: []string{"pudełko", "okładka", "uszkodzon", "kupię", "zamienię", "wymienię", "plakat", "soundtrack", "odtwarzacz", "moduł"},
		Accept: []string{"twarda okładka", "miękka okładka"},
	}
	l := &deals.Listing{Title: "Łowca Androidów DVD PL Edycja kolekcjonerska Kowalski", PriceCents: 1000}
	b.ReportAllocs()
	for b.Loop() {
		_ = match(hs, l, rules, titles)
	}
}

// BenchmarkMatch1000OtherFormat matches a DVD listing whose number fits
// an int against 1,000 Blu-ray holdings of the same film.
func BenchmarkMatch1000OtherFormat(b *testing.B) {
	hs := make([]Holding, 1000)
	for i := range hs {
		hs[i].Want = deals.Want{EditionID: int64(i + 1), TitleID: 1, Kind: "movie", Name: "Alien", Format: new("bluray")}
		hs[i].Want.Prepare()
	}
	l := &deals.Listing{Title: "Alien 9223372036854775807 DVD", PriceCents: 2000}
	b.ReportAllocs()
	for b.Loop() {
		_ = match(hs, l, deals.Rules{}, deals.Titles{})
	}
}
