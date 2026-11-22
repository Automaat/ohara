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
