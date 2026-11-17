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
// the canonical form: EAN-8, EAN-13 or GTIN-14. A UPC-A comes back as its
// EAN-13 (strip the leading 0 to get the UPC back) and an ISBN-10 as its
// ISBN-13, so every spelling of one product maps to the same edition.
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
	return canonical(code), nil
}

// canonical picks one spelling per GTIN, since leading zeros do not change
// the product: an EAN-8 stays 8 digits, a UPC-A becomes its EAN-13 (leading
// 0), and a GTIN-14 with indicator 0 drops to 13 digits. The check digit is
// unchanged by zero padding.
func canonical(code string) string {
	padded := strings.Repeat("0", 14-len(code)) + code
	switch {
	case strings.HasPrefix(padded, "000000"):
		return padded[6:]
	case padded[0] == '0':
		return padded[1:]
	default:
		return padded
	}
}

// IsBook reports whether a normalized code is in the Bookland ISBN range.
func IsBook(code string) bool {
	return len(code) == 13 && (strings.HasPrefix(code, "978") || strings.HasPrefix(code, "979"))
}

// ISBN10 returns the ISBN-10 spelling of a normalized 978 ISBN-13; 979
// ISBNs have none. Older catalog records carry only the ISBN-10.
func ISBN10(code string) (string, bool) {
	if len(code) != 13 || !strings.HasPrefix(code, "978") || !allDigits(code) {
		return "", false
	}
	sum := 0
	for i := range 9 {
		sum += int(code[3+i]-'0') * (10 - i)
	}
	check := (11 - sum%11) % 11
	if check == 10 {
		return code[3:12] + "X", true
	}
	return code[3:12] + string(byte('0'+check)), true
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
