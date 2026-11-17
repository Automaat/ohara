package books

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Automaat/ohara/backend-go/internal/barcode"
	"github.com/Automaat/ohara/backend-go/internal/lookup"
)

// BNBaseURL is the Biblioteka Narodowa open data API (CC0).
const BNBaseURL = "https://data.bn.org.pl"

// BN looks an ISBN up in the Biblioteka Narodowa catalog, the best source
// for Polish (978-83) books. Its search also matches ISBNs a record only
// mentions, such as the original of a translation, so Parse keeps only
// records whose own ISBN field (MARC 020 $a) is the barcode.
type BN struct {
	BaseURL string
	Client  *http.Client
}

// Name is "bn".
func (BN) Name() string { return "bn" }

// Accepts reports whether code is an ISBN.
func (BN) Accepts(code string) bool { return barcode.IsBook(code) }

// Fetch searches by ISBN-13 and, when nothing carries it, by the ISBN-10
// that older records store instead.
func (p BN) Fetch(ctx context.Context, code string) (json.RawMessage, error) {
	raw, err := p.search(ctx, code)
	if err != nil {
		return nil, err
	}
	isbn10, ok := barcode.ISBN10(code)
	if !ok {
		return raw, nil
	}
	cands, err := p.Parse(code, raw)
	if err != nil {
		return nil, err
	}
	if len(cands) > 0 {
		return raw, nil
	}
	return p.search(ctx, isbn10)
}

func (p BN) search(ctx context.Context, isbn string) (json.RawMessage, error) {
	base := p.BaseURL
	if base == "" {
		base = BNBaseURL
	}
	return getJSON(ctx, p.Client, base+"/api/institutions/bibs.json?limit=10&isbnIssn="+url.QueryEscape(isbn))
}

type bnResponse struct {
	Bibs []bnBib `json:"bibs"`
}

type bnBib struct {
	ID                   int64  `json:"id"`
	Deleted              bool   `json:"deleted"`
	PublicationYear      string `json:"publicationYear"`
	TimePeriodOfCreation string `json:"timePeriodOfCreation"`
	Marc                 struct {
		Fields []map[string]json.RawMessage `json:"fields"`
	} `json:"marc"`
}

type marcField struct {
	Tag       string              `json:"-"`
	Ind2      string              `json:"ind2"`
	Subfields []map[string]string `json:"subfields"`
}

// sub returns the values of subfield code, in order.
func (f marcField) sub(code string) []string {
	var out []string
	for _, s := range f.Subfields {
		if v, ok := s[code]; ok {
			out = append(out, v)
		}
	}
	return out
}

func (f marcField) first(code string) string {
	if v := f.sub(code); len(v) > 0 {
		return v[0]
	}
	return ""
}

// dataFields decodes the MARC data fields; control fields (001-009) are
// plain strings and are skipped.
func (b bnBib) dataFields() []marcField {
	var out []marcField
	for _, f := range b.Marc.Fields {
		for tag, raw := range f {
			var mf marcField
			if len(raw) == 0 || raw[0] != '{' || json.Unmarshal(raw, &mf) != nil {
				continue
			}
			mf.Tag = tag
			out = append(out, mf)
		}
	}
	return out
}

// Parse maps each catalog record of this exact ISBN to a book candidate.
func (BN) Parse(code string, raw json.RawMessage) ([]lookup.Candidate, error) {
	var resp bnResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("decode bn response: %w", err)
	}
	isbn10, _ := barcode.ISBN10(code)
	var out []lookup.Candidate
	for _, bib := range resp.Bibs {
		if bib.Deleted {
			continue
		}
		if c, ok := bnCandidate(code, isbn10, bib); ok {
			out = append(out, c)
		}
	}
	return out, nil
}

func bnCandidate(code, isbn10 string, bib bnBib) (lookup.Candidate, bool) {
	fields := bib.dataFields()
	isbnField, ok := matchISBN(fields, code, isbn10)
	if !ok {
		return lookup.Candidate{}, false
	}
	var name, originalTitle, label, publisher, imprint string
	var authors []string
	for _, f := range fields {
		switch f.Tag {
		case "245":
			parts := append(f.sub("a"), append(f.sub("n"), f.sub("p")...)...)
			for i := range parts {
				parts[i] = clean(parts[i])
			}
			name = strings.Join(dedupe(parts), ". ")
		case "246":
			if strings.HasPrefix(f.first("i"), "Tytuł oryginału") && originalTitle == "" {
				originalTitle = clean(f.first("a"))
			}
		case "100":
			authors = append(authors, personName(f.first("a")))
		case "700":
			if len(f.sub("t")) == 0 && isAuthorRole(f.sub("e"), f.sub("4")) {
				authors = append(authors, personName(f.first("a")))
			}
		case "250":
			if label == "" {
				label = clean(f.first("a"))
			}
		case "710":
			if publisher == "" && f.first("4") == "pbl" {
				publisher = clean(f.first("a"))
			}
		case "260", "264":
			if imprint == "" && (f.Tag == "260" || f.Ind2 == "1") {
				imprint = clean(f.first("b"))
			}
		}
	}
	if name == "" {
		return lookup.Candidate{}, false
	}
	if publisher == "" {
		publisher = imprint
	}
	title := lookup.TitleFields{
		Name:    name,
		Authors: dedupe(authors),
		BNID:    new(strconv.FormatInt(bib.ID, 10)),
	}
	if originalTitle != "" && originalTitle != name {
		title.OriginalTitle = &originalTitle
	}
	if y, err := strconv.Atoi(strings.TrimSpace(bib.TimePeriodOfCreation)); err == nil && y >= 1800 && y <= 2200 {
		title.Year = &y
	}
	return lookup.Candidate{
		Type:  "book",
		Title: title,
		Edition: lookup.EditionFields{
			Barcode:   code,
			Label:     label,
			Publisher: publisher,
			Year:      year(bib.PublicationYear),
			Binding:   binding(strings.Join(isbnField.sub("q"), " ")),
		},
		Confidence: 0.9,
	}, true
}

// matchISBN finds the 020 field whose $a is this book's ISBN. A $z is a
// canceled or foreign ISBN and never matches.
func matchISBN(fields []marcField, code, isbn10 string) (marcField, bool) {
	for _, f := range fields {
		if f.Tag != "020" {
			continue
		}
		for _, a := range f.sub("a") {
			isbn := leadingISBN(a)
			if isbn == code || (isbn10 != "" && isbn == isbn10) {
				return f, true
			}
		}
	}
	return marcField{}, false
}

// leadingISBN reads the ISBN at the start of a 020 $a, which may be
// hyphenated or followed by a qualifier.
func leadingISBN(s string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(s) {
		switch {
		case r >= '0' && r <= '9', r == 'X', r == 'x':
			b.WriteRune(r)
		case r == '-':
		default:
			return strings.ToUpper(b.String())
		}
	}
	return strings.ToUpper(b.String())
}

// isAuthorRole reports whether a 700 entry's relator terms ($e) or codes
// ($4) name an author; an entry with neither is an added author heading.
func isAuthorRole(terms, codes []string) bool {
	if len(terms) == 0 && len(codes) == 0 {
		return true
	}
	for _, r := range terms {
		switch strings.ToLower(clean(r)) {
		case "autor", "współautor":
			return true
		}
	}
	for _, c := range codes {
		if strings.EqualFold(strings.TrimSpace(c), "aut") {
			return true
		}
	}
	return false
}
