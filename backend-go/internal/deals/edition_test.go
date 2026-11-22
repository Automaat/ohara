package deals

import "testing"

// TestExactEditionWants checks that a want of only its edition needs a
// listing to name the edition's packaging, Polish voice-over or subtitles
// and binding, and that the same want of any edition matches as one
// without those details does.
func TestExactEditionWants(t *testing.T) {
	steelbook := Want{
		CopyID: 1, EditionID: 10, TitleID: 100, Kind: "movie", Name: "Blade Runner 2049",
		Format: new("bluray"), Packaging: []string{"steelbook"}, PLVoiceover: "unknown", PLSubtitles: "unknown",
	}
	lektor := Want{
		CopyID: 2, EditionID: 20, TitleID: 200, Kind: "movie", Name: "Łowca androidów",
		OriginalTitle: new("Blade Runner"), PolishTitle: new("Łowca androidów"), Format: new("dvd"),
		Packaging: []string{}, PLVoiceover: "yes", PLSubtitles: "unknown",
	}
	subtitles := lektor
	subtitles.PLVoiceover, subtitles.PLSubtitles = "no", "yes"
	both := lektor
	both.PLSubtitles = "yes"
	hardcover := Want{
		CopyID: 3, EditionID: 30, TitleID: 300, Kind: "book", Name: "Ostatnie życzenie",
		Authors: []string{"Andrzej Sapkowski"}, Barcode: new("9788375780635"),
		Binding: new("hardcover"), PLVoiceover: "unknown", PLSubtitles: "unknown",
	}
	paperback := hardcover
	paperback.Binding = new("paperback")
	otherBinding := hardcover
	otherBinding.Binding = new("other")
	plain := Want{
		CopyID: 4, EditionID: 40, TitleID: 400, Kind: "movie", Name: "Blade Runner 2049",
		Format: new("bluray"), Packaging: []string{}, PLVoiceover: "unknown", PLSubtitles: "unknown",
	}
	slipDigi := plain
	slipDigi.Packaging = []string{"slipcover", "digipak"}
	mediabook := plain
	mediabook.Packaging = []string{"mediabook"}
	album := Want{
		CopyID: 5, EditionID: 50, TitleID: 500, Kind: "music", Name: "The Wall",
		Authors: []string{"Pink Floyd"}, Format: new("cd"), Packaging: []string{"digipak"},
		PLVoiceover: "yes", PLSubtitles: "unknown",
	}

	tests := []struct {
		name    string
		want    Want
		matches map[string]bool
	}{
		{"steelbook", steelbook, map[string]bool{
			"Blade Runner 2049 Blu-ray steelbook":  true,
			"Blade Runner 2049 Blu-ray Steel Book": true,
			"Blade Runner 2049 Blu-ray":            false,
			"Blade Runner 2049 DVD steelbook":      false,
		}},
		{"Polish voice-over", lektor, map[string]bool{
			"Łowca androidów DVD lektor PL":      true,
			"Łowca androidów DVD z lektorem":     true,
			"Łowca androidów DVD polski dubbing": true,
			"Blade Runner DVD":                   false,
			"Blade Runner DVD napisy":            false,
			"Łowca androidów DVD":                false,
		}},
		{"Polish subtitles", subtitles, map[string]bool{
			"Blade Runner DVD napisy PL":         true,
			"Łowca androidów DVD polskie napisy": true,
			"Łowca androidów DVD lektor":         false,
		}},
		{"voice-over and subtitles", both, map[string]bool{
			"Łowca androidów DVD lektor napisy PL": true,
			"Łowca androidów DVD lektor PL":        false,
			"Blade Runner DVD napisy":              false,
		}},
		{"hardcover", hardcover, map[string]bool{
			"Ostatnie życzenie Sapkowski twarda oprawa":        true,
			"Ostatnie życzenie Sapkowski w twardej oprawie":    true,
			"Ostatnie życzenie Sapkowski oprawa twarda":        true,
			"Ostatnie życzenie Sapkowski hardcover":            true,
			"Ostatnie życzenie Sapkowski miękka okładka":       false,
			"Ostatnie życzenie Sapkowski":                      false,
			"Ostatnie życzenie Sapkowski oprawa broszurowa":    false,
			"Ostatnie życzenie Sapkowski twarda gra planszowa": false,
		}},
		{"paperback", paperback, map[string]bool{
			"Ostatnie życzenie Sapkowski miękka okładka": true,
			"Ostatnie życzenie Sapkowski oprawa miękka":  true,
			"Ostatnie życzenie Sapkowski broszurowa":     true,
			"Ostatnie życzenie Sapkowski twarda oprawa":  false,
			"Ostatnie życzenie Sapkowski":                false,
		}},
		{"another binding needs nothing", otherBinding, map[string]bool{
			"Ostatnie życzenie Sapkowski": true,
		}},
		{"edition without details", plain, map[string]bool{
			"Blade Runner 2049 Blu-ray":           true,
			"Blade Runner 2049 Blu-ray steelbook": true,
			"Blade Runner 2049 DVD":               false,
		}},
		{"every packaging", slipDigi, map[string]bool{
			"Blade Runner 2049 Blu-ray slipcover digipak":   true,
			"Blade Runner 2049 Blu-ray z obwolutą digipack": true,
			"Blade Runner 2049 Blu-ray slipcover":           false,
			"Blade Runner 2049 Blu-ray digipak":             false,
		}},
		{"mediabook", mediabook, map[string]bool{
			"Blade Runner 2049 Blu-ray Mediabook":  true,
			"Blade Runner 2049 Blu-ray media book": true,
			"Blade Runner 2049 Blu-ray":            false,
		}},
		{"album", album, map[string]bool{
			"Pink Floyd The Wall CD": true,
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exact := tt.want
			exact.ExactEdition = true
			for title, want := range tt.matches {
				checkMatch(t, "exact", &exact, title, want)
				checkMatch(t, "any edition", &tt.want, title, anyEdition(tt.want, title))
			}
		})
	}
}

// checkMatch checks Matches and Belongs under the default phrases for one
// listing title.
func checkMatch(t *testing.T, label string, w *Want, title string, want bool) {
	t.Helper()
	if got := w.Matches(title); got != want {
		t.Errorf("%s: Matches(%q) = %v, want %v", label, title, got, want)
	}
	if got := NewListingText(title, defaultRules).Belongs(w, Titles{}); got != want {
		t.Errorf("%s: Belongs(%q) = %v, want %v", label, title, got, want)
	}
}

// anyEdition is whether w matches title as a want whose edition has none
// of the details an exact want needs.
func anyEdition(w Want, title string) bool {
	w.ExactEdition = false
	w.Packaging, w.PLVoiceover, w.PLSubtitles, w.Binding = nil, "unknown", "unknown", nil
	return w.Matches(title)
}
