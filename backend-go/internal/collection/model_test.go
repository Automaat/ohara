package collection_test

import (
	"encoding/json"
	"testing"

	"github.com/Automaat/ohara/backend-go/internal/collection"
)

func TestTitleInputNormalize(t *testing.T) {
	tests := []struct {
		name      string
		in        collection.TitleInput
		wantField string
	}{
		{"movie", collection.TitleInput{Kind: "movie", Name: " Seksmisja ", TMDBID: new(int64(11906))}, ""},
		{"book", collection.TitleInput{Kind: "book", Name: "Solaris", Authors: []string{" Stanisław Lem ", " "}}, ""},
		{"unknown kind", collection.TitleInput{Kind: "vinyl", Name: "x"}, "kind"},
		{"blank name", collection.TitleInput{Kind: "movie", Name: "  "}, "name"},
		{"year out of range", collection.TitleInput{Kind: "movie", Name: "x", Year: new(1700)}, "year"},
		{"non-positive tmdb id", collection.TitleInput{Kind: "movie", Name: "x", TMDBID: new(int64(0))}, "tmdb_id"},
		{"tmdb id on a book", collection.TitleInput{Kind: "book", Name: "x", TMDBID: new(int64(1))}, "tmdb_id"},
		{"authors on a movie", collection.TitleInput{Kind: "movie", Name: "x", Authors: []string{"a"}}, "authors"},
		{"bn id on a movie", collection.TitleInput{Kind: "movie", Name: "x", BNID: new("b1")}, "bn_id"},
		{"open library id on a movie", collection.TitleInput{Kind: "movie", Name: "x", OpenLibraryID: new("OL1M")}, "openlibrary_id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := tt.in
			fe := in.Normalize()
			if tt.wantField == "" {
				if fe != nil {
					t.Fatalf("unexpected error %v", fe)
				}
				return
			}
			if fe == nil || fe.Field != tt.wantField {
				t.Fatalf("error = %v, want field %q", fe, tt.wantField)
			}
		})
	}
}

func TestTitleInputNormalizeTrims(t *testing.T) {
	in := collection.TitleInput{Kind: "book", Name: " Solaris ", PolishTitle: new("  "), Authors: []string{" Lem ", ""}}
	if fe := in.Normalize(); fe != nil {
		t.Fatalf("Normalize: %v", fe)
	}
	if in.Name != "Solaris" || in.PolishTitle != nil || len(in.Authors) != 1 || in.Authors[0] != "Lem" {
		t.Fatalf("normalized = %+v", in)
	}
}

func TestEditionInputNormalize(t *testing.T) {
	tests := []struct {
		name      string
		in        collection.EditionInput
		kind      string
		wantField string
	}{
		{"movie edition", collection.EditionInput{TitleID: 1, Barcode: new("5901234123457"), Format: new("bluray"), Packaging: []string{"steelbook"}}, "movie", ""},
		{"book edition", collection.EditionInput{TitleID: 1, Barcode: new("0-306-40615-2"), Binding: new("hardcover")}, "book", ""},
		{"no barcode", collection.EditionInput{TitleID: 1, Barcode: new(" "), Format: new("dvd")}, "movie", ""},
		{"missing title", collection.EditionInput{Format: new("dvd")}, "movie", "title_id"},
		{"bad barcode", collection.EditionInput{TitleID: 1, Barcode: new("5901234123458"), Format: new("dvd")}, "movie", "barcode"},
		{"unknown format", collection.EditionInput{TitleID: 1, Format: new("vhs")}, "movie", "format"},
		{"unknown packaging", collection.EditionInput{TitleID: 1, Format: new("dvd"), Packaging: []string{"box"}}, "movie", "packaging"},
		{"unknown subtitles", collection.EditionInput{TitleID: 1, Format: new("dvd"), PLSubtitles: "maybe"}, "movie", "pl_subtitles"},
		{"movie without format", collection.EditionInput{TitleID: 1}, "movie", "format"},
		{"binding on a movie", collection.EditionInput{TitleID: 1, Format: new("dvd"), Binding: new("paperback")}, "movie", "binding"},
		{"format on a book", collection.EditionInput{TitleID: 1, Format: new("dvd")}, "book", "format"},
		{"subtitles on a book", collection.EditionInput{TitleID: 1, PLSubtitles: "yes"}, "book", "pl_subtitles"},
		{"voice-over on a book", collection.EditionInput{TitleID: 1, PLVoiceover: "no"}, "book", "pl_voiceover"},
		{"packaging on a book", collection.EditionInput{TitleID: 1, Packaging: []string{"slipcover"}}, "book", "packaging"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := tt.in
			fe := in.Normalize()
			if fe == nil {
				fe = in.CheckKind(tt.kind)
			}
			if tt.wantField == "" {
				if fe != nil {
					t.Fatalf("unexpected error %v", fe)
				}
				return
			}
			if fe == nil || fe.Field != tt.wantField {
				t.Fatalf("error = %v, want field %q", fe, tt.wantField)
			}
		})
	}
}

func TestEditionInputNormalizeCanonicalizes(t *testing.T) {
	in := collection.EditionInput{TitleID: 1, Barcode: new("0-306-40615-2"), Packaging: []string{"slipcover", "slipcover"}}
	if fe := in.Normalize(); fe != nil {
		t.Fatalf("Normalize: %v", fe)
	}
	if *in.Barcode != "9780306406157" || len(in.Packaging) != 1 || in.PLSubtitles != "unknown" || in.PLVoiceover != "unknown" {
		t.Fatalf("normalized = %+v", in)
	}
}

func TestCopyInputNormalize(t *testing.T) {
	day := &collection.Date{}
	if err := json.Unmarshal([]byte(`"2026-11-01"`), day); err != nil {
		t.Fatalf("date: %v", err)
	}
	tests := []struct {
		name      string
		in        collection.CopyInput
		wantField string
	}{
		{"owned with price", collection.CopyInput{EditionID: 1, Status: "owned", PriceCents: new(int64(4999)), PurchasedOn: day, AcquiredAs: new("used"), Condition: new("good")}, ""},
		{"wishlist", collection.CopyInput{EditionID: 1, Status: "wishlist", AcquiredAs: new("used")}, ""},
		{"missing edition", collection.CopyInput{Status: "owned"}, "edition_id"},
		{"unknown status", collection.CopyInput{EditionID: 1, Status: "lent"}, "status"},
		{"negative price", collection.CopyInput{EditionID: 1, Status: "owned", PriceCents: new(int64(-1))}, "price_cents"},
		{"wishlist with price", collection.CopyInput{EditionID: 1, Status: "wishlist", PriceCents: new(int64(100))}, "price_cents"},
		{"wishlist with purchase date", collection.CopyInput{EditionID: 1, Status: "wishlist", PurchasedOn: day}, "purchased_on"},
		{"unknown acquisition", collection.CopyInput{EditionID: 1, Status: "owned", AcquiredAs: new("gift")}, "acquired_as"},
		{"unknown condition", collection.CopyInput{EditionID: 1, Status: "owned", Condition: new("broken")}, "condition"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := tt.in
			fe := in.Normalize()
			if tt.wantField == "" {
				if fe != nil {
					t.Fatalf("unexpected error %v", fe)
				}
				return
			}
			if fe == nil || fe.Field != tt.wantField {
				t.Fatalf("error = %v, want field %q", fe, tt.wantField)
			}
		})
	}
}

func TestDateJSON(t *testing.T) {
	var d collection.Date
	if err := json.Unmarshal([]byte(`"2026-02-29"`), &d); err == nil {
		t.Fatal("accepted an impossible date")
	}
	if err := json.Unmarshal([]byte(`"2026-11-17"`), &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := json.Marshal(d)
	if err != nil || string(out) != `"2026-11-17"` {
		t.Fatalf("marshal = %s, %v", out, err)
	}
}
