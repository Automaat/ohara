// Package music resolves a CD, vinyl or cassette barcode to album
// releases. Discogs is asked first, since its release is what the
// valuation prices; when it finds nothing, fails or has no token,
// MusicBrainz answers instead, with covers from the Cover Art Archive.
// When neither knows the barcode, a shop catalog (Allegro) names the
// product and Discogs is searched for that name; those releases always go
// to the user to confirm. One
// barcode often maps to several releases (pressings, countries), so each
// becomes a candidate and the user picks the edition.
package music

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/Automaat/ohara/backend-go/internal/barcode"
	"github.com/Automaat/ohara/backend-go/internal/discogs"
	"github.com/Automaat/ohara/backend-go/internal/lookup"
	"github.com/Automaat/ohara/backend-go/internal/lookup/movie"
)

// DiscogsSearcher finds Discogs releases by barcode or by name;
// *discogs.Client implements it.
type DiscogsSearcher interface {
	SearchBarcode(ctx context.Context, code string) ([]discogs.Release, error)
	SearchText(ctx context.Context, query, format string) ([]discogs.Release, error)
}

// ProductNamer names the products a shop catalog lists for a barcode;
// *movie.Allegro implements it.
type ProductNamer interface {
	Products(ctx context.Context, code string) ([]movie.Product, error)
}

// ReleaseSearcher finds MusicBrainz releases by barcode.
type ReleaseSearcher interface {
	Releases(ctx context.Context, code string) ([]MBRelease, error)
}

// CoverFinder returns a MusicBrainz release's front cover URL.
type CoverFinder interface {
	Front(ctx context.Context, mbid string) (string, error)
}

// Provider is the "music" lookup provider. A nil Discogs skips it (no
// token); a nil MusicBrainz leaves Discogs alone; a nil Covers shows
// MusicBrainz releases without covers; a nil Names, or a nil Discogs, skips
// the search by product name.
type Provider struct {
	Discogs     DiscogsSearcher
	MusicBrainz ReleaseSearcher
	Covers      CoverFinder
	Names       ProductNamer
}

// maxCovers bounds the Cover Art Archive requests per barcode.
const maxCovers = 3

// Raw is the provider's cached response. Query is the product name
// Discogs was searched for when no source knew the barcode.
type Raw struct {
	Discogs     []discogs.Release `json:"discogs"`
	MusicBrainz []MBRelease       `json:"musicbrainz"`
	Query       string            `json:"query,omitempty"`
}

// Name is "music".
func (*Provider) Name() string { return "music" }

// Accepts every barcode except ISBNs.
func (*Provider) Accepts(code string) bool { return !barcode.IsBook(code) }

// Fetch asks Discogs, then MusicBrainz when Discogs found nothing, then
// searches Discogs for the shop catalog's product name. With no
// release found, a failed source fails the fetch so a "no match" built on
// an outage is not cached; with releases found, the failures come back as
// a *lookup.PartialError.
func (p *Provider) Fetch(ctx context.Context, code string) (json.RawMessage, error) {
	raw := Raw{Discogs: []discogs.Release{}, MusicBrainz: []MBRelease{}}
	var errs []error
	if p.Discogs != nil {
		rel, err := p.searchDiscogs(ctx, code)
		if err != nil {
			errs = append(errs, err)
		} else {
			raw.Discogs = rel
		}
	}
	if len(raw.Discogs) == 0 && p.MusicBrainz != nil {
		rel, err := p.MusicBrainz.Releases(ctx, code)
		if err != nil {
			errs = append(errs, err)
		} else {
			raw.MusicBrainz = rel
			errs = append(errs, p.addCovers(ctx, raw.MusicBrainz))
		}
	}
	if len(raw.Discogs) == 0 && len(raw.MusicBrainz) == 0 && p.Names != nil && p.Discogs != nil {
		rel, query, err := p.searchByName(ctx, code)
		if err != nil {
			errs = append(errs, err)
		} else {
			raw.Discogs, raw.Query = rel, query
		}
	}
	failed := errors.Join(errs...)
	if len(raw.Discogs) == 0 && len(raw.MusicBrainz) == 0 && failed != nil {
		return nil, failed
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("encode music response: %w", err)
	}
	if failed != nil {
		return nil, &lookup.PartialError{Raw: data, Err: failed}
	}
	return data, nil
}

// searchDiscogs asks for code, then for the 12-digit UPC-A of a code with
// a leading zero, which Discogs often stores as printed.
func (p *Provider) searchDiscogs(ctx context.Context, code string) ([]discogs.Release, error) {
	rel, err := p.Discogs.SearchBarcode(ctx, code)
	if err != nil || len(rel) > 0 {
		return rel, err
	}
	if upc, ok := upcA(code); ok {
		return p.Discogs.SearchBarcode(ctx, upc)
	}
	return rel, nil
}

// maxNamed bounds the releases a product-name search keeps: a name
// matches every pressing of an album.
const maxNamed = 5

// searchByName asks the shop catalog what code is and searches Discogs for
// the first product name that is still a query once cleaned.
func (p *Provider) searchByName(ctx context.Context, code string) ([]discogs.Release, string, error) {
	products, err := p.Names.Products(ctx, code)
	if err != nil {
		return nil, "", err
	}
	for _, prod := range products {
		query, format := productQuery(prod.Name)
		if query == "" {
			continue
		}
		rel, err := p.Discogs.SearchText(ctx, query, format)
		if err != nil {
			return nil, "", err
		}
		return rel[:min(len(rel), maxNamed)], query, nil
	}
	return []discogs.Release{}, "", nil
}

// mediumWords are shop-listing words that name the medium or the
// listing's state, not the album; the first medium word sets the format.
var mediumWords = map[string]string{
	"cd": "CD", "2cd": "CD", "płyta": "", "plyta": "", "album": "",
	"winyl": "Vinyl", "vinyl": "Vinyl", "lp": "Vinyl", "2lp": "Vinyl", "kaseta": "Cassette", "mc": "Cassette",
	"nowa": "", "nowy": "", "folia": "", "folii": "", "w": "", "digipack": "", "digipak": "", "box": "",
}

// productQuery strips medium and listing words from a shop product name
// ("1-800-Oświecenie Taco Hemingway CD") and returns the rest as a Discogs
// query with the medium as its format filter.
func productQuery(name string) (string, string) {
	var kept []string
	format := ""
	for w := range strings.FieldsSeq(name) {
		key := strings.ToLower(strings.Trim(w, "()[]{},.;:!\"'"))
		if f, ok := mediumWords[key]; ok {
			if format == "" {
				format = f
			}
			continue
		}
		if key == "" {
			continue
		}
		kept = append(kept, w)
	}
	return strings.Join(kept, " "), format
}

func (p *Provider) addCovers(ctx context.Context, releases []MBRelease) error {
	if p.Covers == nil {
		return nil
	}
	var errs []error
	for i := range releases[:min(len(releases), maxCovers)] {
		cover, err := p.Covers.Front(ctx, releases[i].ID)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		releases[i].Cover = cover
	}
	return errors.Join(errs...)
}

// Confidence model: a release whose own barcode is the scanned one is a
// sure match, though several of them still go to the user to pick the
// edition; a release the search returned for another barcode stays below
// lookup.ConfirmThreshold.
const (
	discogsExact  = 0.9
	discogsFuzzy  = 0.6
	mbExact       = 0.85
	mbFuzzyWeight = 0.6
)

// Parse turns the cached releases into album candidates.
func (p *Provider) Parse(code string, data json.RawMessage) ([]lookup.Candidate, error) {
	var raw Raw
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("decode music response: %w", err)
	}
	cands := make([]lookup.Candidate, 0, len(raw.Discogs)+len(raw.MusicBrainz))
	for i := range raw.Discogs {
		cands = append(cands, discogsCandidate(code, &raw.Discogs[i]))
	}
	for i := range raw.MusicBrainz {
		cands = append(cands, mbCandidate(code, &raw.MusicBrainz[i]))
	}
	return cands, nil
}

func discogsCandidate(code string, r *discogs.Release) lookup.Candidate {
	artist, album := splitDiscogsTitle(r.Title)
	id := r.ID
	e := lookup.EditionFields{
		Barcode:          code,
		Format:           discogsFormat(r.Format),
		Year:             parseYear(r.Year),
		Label:            describe(descriptions(r.Format), r.Country, r.CatNo),
		DiscogsReleaseID: &id,
	}
	if len(r.Label) > 0 {
		e.Publisher = r.Label[0]
	}
	conf := discogsFuzzy
	if anyBarcode(code, r.Barcode...) {
		conf = discogsExact
	}
	c := lookup.Candidate{
		Type:       "music",
		Title:      lookup.TitleFields{Name: album, Authors: nonEmpty(artist)},
		Edition:    e,
		Confidence: conf,
	}
	if !strings.HasSuffix(r.CoverImage, "spacer.gif") {
		c.CoverURL = r.CoverImage
	}
	return c
}

func mbCandidate(code string, r *MBRelease) lookup.Candidate {
	e := lookup.EditionFields{Barcode: code, Year: parseYear(r.Date)}
	var formats []string
	for _, m := range r.Media {
		formats = append(formats, m.Format)
	}
	e.Format = mbFormat(formats)
	var catNo string
	for _, li := range r.LabelInfo {
		if e.Publisher == "" && li.Label != nil {
			e.Publisher = li.Label.Name
		}
		if catNo == "" {
			catNo = li.CatalogNumber
		}
	}
	e.Label = describe(nil, r.Country, catNo)
	conf := mbFuzzyWeight * float64(min(max(r.Score, 0), 100)) / 100
	if anyBarcode(code, r.Barcode) {
		conf = mbExact
	}
	return lookup.Candidate{
		Type:       "music",
		Title:      lookup.TitleFields{Name: r.Title, Authors: nonEmpty(r.Artist())},
		Edition:    e,
		CoverURL:   r.Cover,
		Confidence: conf,
	}
}

// discogsSuffix is the number Discogs appends to tell same-named artists
// apart ("Nirvana (2)"), and the star of an artist name variation.
var discogsSuffix = regexp.MustCompile(`\s*\(\d+\)$|\*$`)

// splitDiscogsTitle splits Discogs' "Artist - Album" into the artist,
// without its disambiguation, and the album, in that order.
func splitDiscogsTitle(title string) (string, string) {
	artist, album, ok := strings.Cut(title, " - ")
	if !ok {
		return "", strings.TrimSpace(title)
	}
	return strings.TrimSpace(discogsSuffix.ReplaceAllString(strings.TrimSpace(artist), "")), strings.TrimSpace(album)
}

// mediumNames are the Discogs format names of each edition format, and
// the words of a MusicBrainz medium format that name it.
var mediumNames = []struct {
	format  string
	discogs []string
	mb      string
}{
	{"vinyl", []string{"Vinyl"}, "vinyl"},
	{"cassette", []string{"Cassette"}, "cassette"},
	{"cd", []string{"CD", "CDr", "SACD", "Hybrid", "HDCD", "SHM-CD", "Blu-spec CD"}, "cd"},
}

// discogsFormat is the edition format of the first medium Discogs names.
func discogsFormat(formats []string) *string {
	for _, f := range formats {
		for _, m := range mediumNames {
			for _, name := range m.discogs {
				if strings.EqualFold(f, name) {
					format := m.format
					return &format
				}
			}
		}
	}
	return nil
}

// mbFormat is the edition format of the first medium MusicBrainz names
// ("CD", "Enhanced CD", "12\" Vinyl", "Cassette").
func mbFormat(formats []string) *string {
	for _, f := range formats {
		words := strings.FieldsFunc(strings.ToLower(f), func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r)
		})
		for _, m := range mediumNames {
			for _, w := range words {
				if w == m.mb || (m.mb == "cd" && strings.HasSuffix(w, "cd")) {
					format := m.format
					return &format
				}
			}
		}
	}
	return nil
}

// descriptions are the Discogs format words that describe the release
// ("Album", "Reissue", "Remastered"), without the medium names and the
// album marker every release carries.
func descriptions(formats []string) []string {
	var out []string
	for _, f := range formats {
		if discogsFormat([]string{f}) != nil || strings.EqualFold(f, "Album") {
			continue
		}
		out = append(out, f)
	}
	return out
}

// describe joins the edition details a user tells pressings apart by.
func describe(descr []string, country, catNo string) string {
	var parts []string
	if len(descr) > 0 {
		parts = append(parts, strings.Join(descr, ", "))
	}
	for _, p := range []string{country, catNo} {
		if p = strings.TrimSpace(p); p != "" && p != "none" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, " · ")
}

// anyBarcode reports whether one of the printed barcodes is code, ignoring
// spaces, dashes and leading zeros.
func anyBarcode(code string, printed ...string) bool {
	want := strings.TrimLeft(code, "0")
	for _, p := range printed {
		digits := strings.Map(func(r rune) rune {
			if r >= '0' && r <= '9' {
				return r
			}
			return -1
		}, p)
		if digits != "" && strings.TrimLeft(digits, "0") == want {
			return true
		}
	}
	return false
}

// parseYear reads the year a Discogs year or a MusicBrainz date starts with.
func parseYear(s string) *int {
	if len(s) < 4 {
		return nil
	}
	y, err := strconv.Atoi(s[:4])
	if err != nil || y == 0 {
		return nil
	}
	return &y
}

func nonEmpty(s string) []string {
	if s == "" {
		return nil
	}
	return []string{s}
}
