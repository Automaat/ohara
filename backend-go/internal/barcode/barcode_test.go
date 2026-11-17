package barcode_test

import (
	"errors"
	"testing"

	"github.com/Automaat/ohara/backend-go/internal/barcode"
)

func TestNormalize(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr error
	}{
		{"EAN-13", "5901234123457", "5901234123457", nil},
		{"EAN-13 with spaces and hyphens", " 590-1234 12345-7 ", "5901234123457", nil},
		{"UPC-A", "036000291452", "036000291452", nil},
		{"EAN-8", "96385074", "96385074", nil},
		{"GTIN-14", "00012345600012", "00012345600012", nil},
		{"ISBN-13", "978-0-306-40615-7", "9780306406157", nil},
		{"ISBN-10 becomes ISBN-13", "0-306-40615-2", "9780306406157", nil},
		{"ISBN-10 with X check digit", "080442957x", "9780804429573", nil},
		{"bad EAN-13 check digit", "5901234123458", "", barcode.ErrChecksum},
		{"bad ISBN-10 check digit", "0306406153", "", barcode.ErrChecksum},
		{"X inside ISBN-10", "03X6406152", "", barcode.ErrFormat},
		{"letters", "59012341234AB", "", barcode.ErrFormat},
		{"too short", "1234567", "", barcode.ErrFormat},
		{"eleven digits", "12345678901", "", barcode.ErrFormat},
		{"empty", "", "", barcode.ErrFormat},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := barcode.Normalize(tt.in)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Normalize(%q) err = %v, want %v", tt.in, err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("Normalize(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestIsBook(t *testing.T) {
	tests := []struct {
		code string
		want bool
	}{
		{"9788373012349", true},
		{"9791234567896", true},
		{"5901234123457", false},
		{"96385074", false},
	}
	for _, tt := range tests {
		if got := barcode.IsBook(tt.code); got != tt.want {
			t.Errorf("IsBook(%q) = %v, want %v", tt.code, got, tt.want)
		}
	}
}
