// Package barcode normalizes the product codes printed on discs and books:
// EAN-8, UPC-A, EAN-13 (ISBN-13 included), GTIN-14 and ISBN-10.
package barcode

import (
	"errors"
	"strings"
)

var (
	ErrFormat   = errors.New("barcode must have 8, 12, 13 or 14 digits, or be an ISBN-10")
	ErrChecksum = errors.New("barcode check digit does not match")
)

// Normalize strips spaces and hyphens, verifies the check digit and returns
// the canonical form. ISBN-10 becomes its ISBN-13, so both spellings of one
// book map to the same edition.
func Normalize(raw string) (string, error) {
	code := strings.ToUpper(strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' || r == '\t' {
			return -1
		}
		return r
	}, raw))

	if len(code) == 10 {
		return isbn10To13(code)
	}
	switch len(code) {
	case 8, 12, 13, 14:
	default:
		return "", ErrFormat
	}
	if !allDigits(code) {
		return "", ErrFormat
	}
	if gtinCheckDigit(code[:len(code)-1]) != code[len(code)-1] {
		return "", ErrChecksum
	}
	return code, nil
}

// IsBook reports whether a normalized code is in the Bookland ISBN range.
func IsBook(code string) bool {
	return len(code) == 13 && (strings.HasPrefix(code, "978") || strings.HasPrefix(code, "979"))
}

func isbn10To13(code string) (string, error) {
	if !allDigits(code[:9]) || (!allDigits(code[9:]) && code[9] != 'X') {
		return "", ErrFormat
	}
	sum := 0
	for i := range 9 {
		sum += int(code[i]-'0') * (10 - i)
	}
	check := 10
	if code[9] != 'X' {
		check = int(code[9] - '0')
	}
	if (sum+check)%11 != 0 {
		return "", ErrChecksum
	}
	body := "978" + code[:9]
	return body + string(gtinCheckDigit(body)), nil
}

// gtinCheckDigit is the GS1 mod-10 digit: weights 3 and 1 alternate from the
// rightmost body digit.
func gtinCheckDigit(body string) byte {
	sum := 0
	for i := range len(body) {
		d := int(body[len(body)-1-i] - '0')
		if i%2 == 0 {
			d *= 3
		}
		sum += d
	}
	return byte('0' + (10-sum%10)%10)
}

func allDigits(s string) bool {
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}
