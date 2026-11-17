package lookup

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Automaat/ohara/backend-go/internal/barcode"
)

// Stub answers every barcode with one low-confidence placeholder: a book for
// ISBNs, a DVD otherwise. It lets the scanner flow run end to end before the
// real providers exist; enable it with OHARA_LOOKUP_STUB=true.
type Stub struct{}

type stubResponse struct {
	Barcode string `json:"barcode"`
}

// Name is "stub".
func (Stub) Name() string { return "stub" }

// Fetch echoes the barcode.
func (Stub) Fetch(_ context.Context, code string) (json.RawMessage, error) {
	return json.Marshal(stubResponse{Barcode: code})
}

// Parse builds the placeholder candidate.
func (Stub) Parse(_ string, raw json.RawMessage) ([]Candidate, error) {
	var r stubResponse
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("decode stub response: %w", err)
	}
	if barcode.IsBook(r.Barcode) {
		binding := "paperback"
		return []Candidate{{
			Type:       "book",
			Title:      TitleFields{Name: "Książka " + r.Barcode},
			Edition:    EditionFields{Barcode: r.Barcode, Binding: &binding},
			Confidence: 0.1,
		}}, nil
	}
	format := "dvd"
	return []Candidate{{
		Type:       "movie",
		Title:      TitleFields{Name: "Film " + r.Barcode},
		Edition:    EditionFields{Barcode: r.Barcode, Format: &format, PLSubtitles: "unknown", PLVoiceover: "unknown"},
		Confidence: 0.1,
	}}, nil
}
