package movie

import (
	"slices"
	"testing"
)

func TestParseName(t *testing.T) {
	tests := []struct {
		name, in   string
		title, alt string
		year       *int
		format     *string
		subs, lekt string
		packaging  []string
	}{
		{name: "ceneo bracket", in: "Incepcja [Blu-Ray]", title: "Incepcja", format: new("bluray"), subs: "unknown", lekt: "unknown"},
		{name: "original title in parens", in: "Incepcja (Inception) (2Blu-ray)", title: "Incepcja", alt: "Inception", format: new("bluray"), subs: "unknown", lekt: "unknown"},
		{name: "4k wins over blu-ray", in: "Incepcja [Blu-Ray 4K]+[Blu-Ray]", title: "Incepcja", format: new("uhd"), subs: "unknown", lekt: "unknown"},
		{name: "ceneo page prefix", in: "Film Blu-ray Kursk [Blu-Ray]", title: "Kursk", format: new("bluray"), subs: "unknown", lekt: "unknown"},
		{name: "year and subtitles", in: "Matrix (1999) DVD napisy PL", title: "Matrix", year: new(1999), format: new("dvd"), subs: "yes", lekt: "unknown"},
		{name: "lektor and subtitle colon kept", in: "Władca Pierścieni: Drużyna Pierścienia (DVD) Lektor PL", title: "Władca Pierścieni: Drużyna Pierścienia", format: new("dvd"), subs: "unknown", lekt: "yes"},
		{name: "packaging", in: "Joker (Steelbook) [Blu-Ray 4K] digipack", title: "Joker", format: new("uhd"), subs: "unknown", lekt: "unknown", packaging: []string{"steelbook", "digipak"}},
		{name: "upcitemdb listing", in: "Teatr Sensacji Kobra - Vol. 5 (dvd 3 Disc) Teatr Tv Polish Polski", title: "Teatr Sensacji Kobra - Vol. 5", format: new("dvd"), subs: "unknown", lekt: "unknown"},
		{name: "plus edition", in: "Diuna 4K UHD + Blu-ray (wydanie kolekcjonerskie)", title: "Diuna", format: new("uhd"), subs: "unknown", lekt: "unknown"},
		{name: "no format", in: "Listy do M. 4", title: "Listy do M. 4", subs: "unknown", lekt: "unknown"},
		{name: "sequel number before format", in: "Listy do M. 4 DVD", title: "Listy do M. 4", format: new("dvd"), subs: "unknown", lekt: "unknown"},
		{name: "disc count", in: "Psy 2xDVD", title: "Psy", format: new("dvd"), subs: "unknown", lekt: "unknown"},
		{name: "bracket first", in: "[DVD] Psy", title: "Psy", format: new("dvd"), subs: "unknown", lekt: "unknown"},
		{name: "title only in parens", in: "DVD (Psy)", title: "Psy", format: new("dvd"), subs: "unknown", lekt: "unknown"},
		{name: "dubbing", in: "Kraina Lodu DVD dubbing", title: "Kraina Lodu", format: new("dvd"), subs: "unknown", lekt: "yes"},
		{name: "english subtitles only", in: "Incepcja Blu-ray napisy angielskie", title: "Incepcja", format: new("bluray"), subs: "unknown", lekt: "unknown"},
		{name: "explicit absence", in: "Psy DVD lektor: brak, napisy: brak", title: "Psy", format: new("dvd"), subs: "no", lekt: "no"},
		{name: "bez lektora", in: "Psy DVD bez lektora, napisy angielskie i polskie", title: "Psy", format: new("dvd"), subs: "yes", lekt: "no"},
		{name: "polish qualifier", in: "Psy DVD Lektor PL Napisy PL", title: "Psy", format: new("dvd"), subs: "yes", lekt: "yes"},
		{name: "bez napisów", in: "Psy DVD dubbing polski, bez napisów", title: "Psy", format: new("dvd"), subs: "no", lekt: "yes"},
		{name: "word containing bd", in: "Abdykacja DVD", title: "Abdykacja", format: new("dvd"), subs: "unknown", lekt: "unknown"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := ParseName(tc.in)
			if h.Title != tc.title || h.AltTitle != tc.alt {
				t.Errorf("title = %q alt %q, want %q alt %q", h.Title, h.AltTitle, tc.title, tc.alt)
			}
			if !eqPtr(h.Year, tc.year) {
				t.Errorf("year = %v, want %v", deref(h.Year), deref(tc.year))
			}
			if !eqPtr(h.Format, tc.format) {
				t.Errorf("format = %v, want %v", deref(h.Format), deref(tc.format))
			}
			if h.PLSubtitles != tc.subs || h.PLVoiceover != tc.lekt {
				t.Errorf("subs %q voiceover %q, want %q %q", h.PLSubtitles, h.PLVoiceover, tc.subs, tc.lekt)
			}
			if !slices.Equal(h.Packaging, tc.packaging) {
				t.Errorf("packaging = %v, want %v", h.Packaging, tc.packaging)
			}
		})
	}
}

func TestSimilarity(t *testing.T) {
	tests := []struct {
		a, b     string
		min, max float64
	}{
		{"Władca Pierścieni", "wladca pierscieni", 1, 1},
		{"Listy do M. 4", "Listy do M. 4", 1, 1},
		{"Listy do M. 4", "Listy do M. 2", 0, 0.7},
		{"Incepcja", "Inception", 0.5, 0.8},
		{"", "Matrix", 0, 0},
	}
	for _, tc := range tests {
		got := similarity(tc.a, tc.b)
		if got < tc.min || got > tc.max {
			t.Errorf("similarity(%q, %q) = %v, want [%v, %v]", tc.a, tc.b, got, tc.min, tc.max)
		}
	}
}

func TestMainTitle(t *testing.T) {
	tests := map[string]string{
		"25 lat niewinności. Sprawa Tomka Komendy": "25 lat niewinności",
		"Władca Pierścieni: Drużyna Pierścienia":   "Władca Pierścieni",
		"Teatr Sensacji Kobra - Vol. 5":            "Teatr Sensacji Kobra",
		"Listy do M. 4":                            "Listy do M",
		"Kursk":                                    "",
		"2: Dwa":                                   "",
	}
	for in, want := range tests {
		if got := mainTitle(in); got != want {
			t.Errorf("mainTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func eqPtr[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func deref[T any](p *T) any {
	if p == nil {
		return nil
	}
	return *p
}
