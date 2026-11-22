package movie

import "testing"

func TestSeriesNameDropsTheCollectionWord(t *testing.T) {
	tests := []struct{ in, want string }{
		{in: "Harry Potter - kolekcja", want: "Harry Potter"},
		{in: "Szybcy i wściekli – Kolekcja", want: "Szybcy i wściekli"},
		{in: "Obcy (kolekcja)", want: "Obcy"},
		{in: "Hobbit  Kolekcja", want: "Hobbit"},
		{in: "James Bond Collection", want: "James Bond"},
		{in: "Kolekcja", want: "Kolekcja"},
		{in: "  Matrix  ", want: "Matrix"},
		{in: "Rekolekcja", want: "Rekolekcja"},
		{in: "", want: ""},
	}
	for _, tt := range tests {
		if got := seriesName(tt.in); got != tt.want {
			t.Errorf("seriesName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestReleasedPartsOrderAndSkipUnreleased(t *testing.T) {
	parts := []tmdbCollectionPart{
		{ID: 3, ReleaseDate: "2004-05-31"},
		{ID: 9, ReleaseDate: "2002-11-13"},
		{ID: 4, ReleaseDate: ""},
		{ID: 8, ReleaseDate: "2002-11-13"},
		{ID: 5, ReleaseDate: "2026-11-23"},
		{ID: 6, ReleaseDate: "2026-11-22"},
		{ID: 7, ReleaseDate: "soon"},
	}
	got := releasedParts(parts, "2026-11-22")
	want := []int64{8, 9, 3, 6}
	if len(got) != len(want) {
		t.Fatalf("released = %v, want IDs %v", got, want)
	}
	for i, p := range got {
		if p.ID != want[i] {
			t.Errorf("released[%d] = %d, want %d", i, p.ID, want[i])
		}
	}
}
