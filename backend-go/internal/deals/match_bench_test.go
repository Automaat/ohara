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
