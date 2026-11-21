// Package collection owns titles, their editions and the copies on the shelf
// or on the wishlist.
package collection

import (
	"errors"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/Automaat/ohara/backend-go/internal/barcode"
	"github.com/Automaat/ohara/backend-go/internal/httpx"
)

const (
	KindMovie = "movie"
	KindBook  = "book"
	KindMusic = "music"

	StatusOwned    = "owned"
	StatusWishlist = "wishlist"

	maxText    = 500
	maxNotes   = 10000
	maxAuthors = 20
	// MaxPriceCents (1 000 000 zł) keeps collection totals far from int64 overflow.
	MaxPriceCents = 100_000_000
)

var (
	kinds        = []string{KindMovie, KindBook, KindMusic}
	movieFormats = []string{"dvd", "bluray", "uhd"}
	musicFormats = []string{"cd", "vinyl", "cassette"}
	formats      = slices.Concat(movieFormats, musicFormats)
	bindings     = []string{"hardcover", "paperback", "other"}
	triStates    = []string{"yes", "no", "unknown"}
	packagings   = []string{"steelbook", "slipcover", "digipak", "mediabook"}
	statuses     = []string{StatusOwned, StatusWishlist}
	acquiredAs   = []string{"new", "used"}
	conditions   = []string{"mint", "very_good", "good", "fair", "poor"}
)

// Sentinel errors the handlers map to HTTP status codes.
var (
	ErrNotFound         = errors.New("not found")
	ErrTitleNotFound    = errors.New("title not found")
	ErrEditionNotFound  = errors.New("edition not found")
	ErrDuplicateBarcode = errors.New("an edition with this barcode already exists")
	ErrDuplicateTMDB    = errors.New("a title with this TMDB id already exists")
	ErrKindMismatch     = errors.New("edition and title kinds differ")
	ErrKindLocked       = errors.New("title kind cannot change while it has editions")
	ErrHasEditions      = errors.New("title still has editions")
	ErrHasCopies        = errors.New("edition still has copies")
	// ErrRequestDiscarded refuses a create whose request key a discard gave up.
	ErrRequestDiscarded = errors.New("this add was already undone")
)

// Date is a calendar day carried as YYYY-MM-DD in JSON.
type Date struct{ time.Time }

const dateLayout = "2006-01-02"

// MarshalJSON writes YYYY-MM-DD.
func (d Date) MarshalJSON() ([]byte, error) {
	return []byte(`"` + d.Format(dateLayout) + `"`), nil
}

// UnmarshalJSON reads YYYY-MM-DD.
func (d *Date) UnmarshalJSON(b []byte) error {
	t, err := time.Parse(`"`+dateLayout+`"`, string(b))
	if err != nil {
		return errors.New("date must be YYYY-MM-DD")
	}
	d.Time = t
	return nil
}

// TitleInput is the body of POST and PUT /api/titles. Movie-only fields:
// tmdb_id. Book-only fields: bn_id, openlibrary_id. Authors are a book's
// authors or an album's artists; movies have none.
type TitleInput struct {
	Kind          string   `json:"kind"`
	Name          string   `json:"name"`
	OriginalTitle *string  `json:"original_title"`
	PolishTitle   *string  `json:"polish_title"`
	Year          *int     `json:"year"`
	TMDBID        *int64   `json:"tmdb_id"`
	Authors       []string `json:"authors"`
	BNID          *string  `json:"bn_id"`
	OpenLibraryID *string  `json:"openlibrary_id"`
	Notes         string   `json:"notes"`
	CoverURL      *string  `json:"cover_url,omitempty"`
	// RequestKey makes a create safe to retry: a second create with the
	// same key returns the first title. Updates ignore it.
	RequestKey *string `json:"request_key,omitempty"`
}

// Title is a work: a film, a book or an album.
type Title struct {
	ID int64 `json:"id"`
	TitleInput
	EditionCount int       `json:"edition_count"`
	CopyCount    int       `json:"copy_count"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// EditionInput is the body of POST and PUT /api/editions. Movie-only fields:
// pl_subtitles, pl_voiceover, packaging. Book-only: binding. Music-only:
// discogs_release_id. Format is required for movies (dvd, bluray, uhd) and
// music (cd, vinyl, cassette) and absent for books.
type EditionInput struct {
	TitleID     int64    `json:"title_id"`
	Barcode     *string  `json:"barcode"`
	Label       string   `json:"label"`
	Publisher   string   `json:"publisher"`
	Year        *int     `json:"year"`
	Format      *string  `json:"format"`
	PLSubtitles string   `json:"pl_subtitles"`
	PLVoiceover string   `json:"pl_voiceover"`
	Packaging   []string `json:"packaging"`
	Binding     *string  `json:"binding"`
	// DiscogsReleaseID is the Discogs release an album edition is; the
	// valuation prices it on the Discogs marketplace.
	DiscogsReleaseID *int64  `json:"discogs_release_id"`
	Notes            string  `json:"notes"`
	CoverURL         *string `json:"cover_url,omitempty"`
	// RequestKey makes a create safe to retry: a second create with the
	// same key returns the first edition. Updates ignore it.
	RequestKey *string `json:"request_key,omitempty"`
}

// Edition is a concrete release of a title.
type Edition struct {
	ID int64 `json:"id"`
	EditionInput
	CopyCount        int       `json:"copy_count"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
	CoverOverrideURL *string   `json:"cover_override_url,omitempty"`
}

// CopyInput is the body of POST and PUT /api/copies. Price and purchase date
// apply only to owned copies. MaxPriceCents, the most a wishlist copy may
// cost, applies only to wishlist copies: the deal hunter alerts on listings
// at or below it.
type CopyInput struct {
	EditionID     int64   `json:"edition_id"`
	Status        string  `json:"status"`
	PriceCents    *int64  `json:"price_cents"`
	PurchasedOn   *Date   `json:"purchased_on"`
	Shop          string  `json:"shop"`
	AcquiredAs    *string `json:"acquired_as"`
	Condition     *string `json:"condition"`
	Notes         string  `json:"notes"`
	MaxPriceCents *int64  `json:"max_price_cents"`
	// RequestKey makes a create safe to retry: a second create with the
	// same key returns the first copy. Updates ignore it.
	RequestKey *string `json:"request_key,omitempty"`
}

// maxRequestKey caps a create's RequestKey, in bytes.
const maxRequestKey = 100

// normalizeRequestKey trims a create's request key; blank is none.
func normalizeRequestKey(key *string) (*string, *httpx.FieldError) {
	trimmed := trimOpt(key)
	if trimmed != nil && len(*trimmed) > maxRequestKey {
		return nil, fieldErr("request_key", "is too long")
	}
	return trimmed, nil
}

// Copy is one physical item, owned or wanted.
type Copy struct {
	ID int64 `json:"id"`
	CopyInput
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func fieldErr(field, msg string) *httpx.FieldError {
	return &httpx.FieldError{Field: field, Msg: msg}
}

// trimOpt trims an optional string and turns blank into nil.
func trimOpt(s *string) *string {
	if s == nil {
		return nil
	}
	t := strings.TrimSpace(*s)
	if t == "" {
		return nil
	}
	return &t
}

func checkLen(field, value string, limit int) *httpx.FieldError {
	if len(value) > limit {
		return fieldErr(field, "is too long")
	}
	return nil
}

func checkEnum(field string, value *string, allowed []string) *httpx.FieldError {
	if value != nil && !slices.Contains(allowed, *value) {
		return fieldErr(field, "must be one of: "+strings.Join(allowed, ", "))
	}
	return nil
}

func checkYear(field string, year *int) *httpx.FieldError {
	if year != nil && (*year < 1800 || *year > 2200) {
		return fieldErr(field, "must be between 1800 and 2200")
	}
	return nil
}

func firstErr(errs ...*httpx.FieldError) *httpx.FieldError {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

// Normalize trims the input in place and checks it.
func (in *TitleInput) Normalize() *httpx.FieldError {
	in.Kind = strings.TrimSpace(in.Kind)
	in.Name = strings.TrimSpace(in.Name)
	in.Notes = strings.TrimSpace(in.Notes)
	in.OriginalTitle = trimOpt(in.OriginalTitle)
	in.PolishTitle = trimOpt(in.PolishTitle)
	in.BNID = trimOpt(in.BNID)
	in.OpenLibraryID = trimOpt(in.OpenLibraryID)
	key, fe := normalizeRequestKey(in.RequestKey)
	if fe != nil {
		return fe
	}
	in.RequestKey = key
	authors := make([]string, 0, len(in.Authors))
	for _, a := range in.Authors {
		if a = strings.TrimSpace(a); a != "" {
			authors = append(authors, a)
		}
	}
	in.Authors = authors

	if !slices.Contains(kinds, in.Kind) {
		return fieldErr("kind", "must be one of: "+strings.Join(kinds, ", "))
	}
	if in.Name == "" {
		return fieldErr("name", "cannot be empty")
	}
	if e := firstErr(
		checkLen("name", in.Name, maxText),
		checkLen("original_title", deref(in.OriginalTitle), maxText),
		checkLen("polish_title", deref(in.PolishTitle), maxText),
		checkLen("bn_id", deref(in.BNID), maxText),
		checkLen("openlibrary_id", deref(in.OpenLibraryID), maxText),
		checkLen("notes", in.Notes, maxNotes),
		normalizeCoverURL(in.CoverURL),
		checkYear("year", in.Year),
	); e != nil {
		return e
	}
	if in.TMDBID != nil && *in.TMDBID <= 0 {
		return fieldErr("tmdb_id", "must be positive")
	}
	if len(in.Authors) > maxAuthors {
		return fieldErr("authors", "too many authors")
	}
	for _, a := range in.Authors {
		if e := checkLen("authors", a, maxText); e != nil {
			return e
		}
	}
	return in.checkKindFields()
}

func (in *TitleInput) checkKindFields() *httpx.FieldError {
	switch {
	case in.Kind != KindMovie && in.TMDBID != nil:
		return fieldErr("tmdb_id", "applies only to movies")
	case in.Kind == KindMovie && len(in.Authors) > 0:
		return fieldErr("authors", "applies only to books and music")
	case in.Kind != KindBook && in.BNID != nil:
		return fieldErr("bn_id", "applies only to books")
	case in.Kind != KindBook && in.OpenLibraryID != nil:
		return fieldErr("openlibrary_id", "applies only to books")
	}
	return nil
}

// Normalize trims the input in place and checks the fields that do not depend
// on the title's kind; CheckKind covers the rest.
func (in *EditionInput) Normalize() *httpx.FieldError {
	in.Label = strings.TrimSpace(in.Label)
	in.Publisher = strings.TrimSpace(in.Publisher)
	in.Notes = strings.TrimSpace(in.Notes)
	in.Format = trimOpt(in.Format)
	in.Binding = trimOpt(in.Binding)
	in.PLSubtitles = defaultTri(in.PLSubtitles)
	in.PLVoiceover = defaultTri(in.PLVoiceover)
	key, fe := normalizeRequestKey(in.RequestKey)
	if fe != nil {
		return fe
	}
	in.RequestKey = key
	if in.TitleID <= 0 {
		return fieldErr("title_id", "is required")
	}
	if code := trimOpt(in.Barcode); code != nil {
		normalized, err := barcode.Normalize(*code)
		if err != nil {
			return fieldErr("barcode", err.Error())
		}
		in.Barcode = &normalized
	} else {
		in.Barcode = nil
	}
	packaging := make([]string, 0, len(in.Packaging))
	for _, p := range in.Packaging {
		p = strings.TrimSpace(p)
		if e := checkEnum("packaging", &p, packagings); e != nil {
			return e
		}
		if !slices.Contains(packaging, p) {
			packaging = append(packaging, p)
		}
	}
	in.Packaging = packaging
	if in.DiscogsReleaseID != nil && *in.DiscogsReleaseID <= 0 {
		return fieldErr("discogs_release_id", "must be positive")
	}
	return firstErr(
		checkLen("label", in.Label, maxText),
		checkLen("publisher", in.Publisher, maxText),
		checkLen("notes", in.Notes, maxNotes),
		normalizeCoverURL(in.CoverURL),
		checkYear("year", in.Year),
		checkEnum("format", in.Format, formats),
		checkEnum("binding", in.Binding, bindings),
		checkEnum("pl_subtitles", &in.PLSubtitles, triStates),
		checkEnum("pl_voiceover", &in.PLVoiceover, triStates),
	)
}

func normalizeCoverURL(value *string) *httpx.FieldError {
	if value == nil {
		return nil
	}
	*value = strings.TrimSpace(*value)
	if *value == "" {
		return nil
	}
	if err := checkLen("cover_url", *value, maxNotes); err != nil {
		return err
	}
	u, err := url.Parse(*value)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil {
		return fieldErr("cover_url", "must be an HTTP or HTTPS URL without credentials")
	}
	return nil
}

// CheckKind enforces the fields that depend on the title's kind.
func (in *EditionInput) CheckKind(kind string) *httpx.FieldError {
	if kind != KindMusic && in.DiscogsReleaseID != nil {
		return fieldErr("discogs_release_id", "applies only to music")
	}
	if kind == KindMovie {
		if e := checkFormat(in.Format, movieFormats, "movies"); e != nil {
			return e
		}
		if in.Binding != nil {
			return fieldErr("binding", "applies only to books")
		}
		return nil
	}
	if kind == KindMusic {
		if e := checkFormat(in.Format, musicFormats, "music"); e != nil {
			return e
		}
		if in.Binding != nil {
			return fieldErr("binding", "applies only to books")
		}
	} else if in.Format != nil {
		return fieldErr("format", "applies only to movies and music")
	}
	switch {
	case in.PLSubtitles != "unknown":
		return fieldErr("pl_subtitles", "applies only to movies")
	case in.PLVoiceover != "unknown":
		return fieldErr("pl_voiceover", "applies only to movies")
	case len(in.Packaging) > 0:
		return fieldErr("packaging", "applies only to movies")
	}
	return nil
}

// checkFormat requires a format from allowed, the formats of kind.
func checkFormat(format *string, allowed []string, kind string) *httpx.FieldError {
	if format == nil {
		return fieldErr("format", "is required for "+kind)
	}
	if !slices.Contains(allowed, *format) {
		return fieldErr("format", "must be one of: "+strings.Join(allowed, ", ")+" for "+kind)
	}
	return nil
}

// Normalize trims the input in place and checks it.
func (in *CopyInput) Normalize() *httpx.FieldError {
	in.Status = strings.TrimSpace(in.Status)
	in.Shop = strings.TrimSpace(in.Shop)
	in.Notes = strings.TrimSpace(in.Notes)
	in.AcquiredAs = trimOpt(in.AcquiredAs)
	in.Condition = trimOpt(in.Condition)
	key, fe := normalizeRequestKey(in.RequestKey)
	if fe != nil {
		return fe
	}
	in.RequestKey = key
	if in.EditionID <= 0 {
		return fieldErr("edition_id", "is required")
	}
	if e := checkEnum("status", &in.Status, statuses); e != nil {
		return e
	}
	if e := checkPrice("price_cents", in.PriceCents); e != nil {
		return e
	}
	if e := checkPrice("max_price_cents", in.MaxPriceCents); e != nil {
		return e
	}
	if in.Status == StatusOwned && in.MaxPriceCents != nil {
		return fieldErr("max_price_cents", "applies only to wishlist copies")
	}
	if in.Status == StatusWishlist {
		if in.PriceCents != nil {
			return fieldErr("price_cents", "applies only to owned copies")
		}
		if in.PurchasedOn != nil {
			return fieldErr("purchased_on", "applies only to owned copies")
		}
	}
	return firstErr(
		checkLen("shop", in.Shop, maxText),
		checkLen("notes", in.Notes, maxNotes),
		checkEnum("acquired_as", in.AcquiredAs, acquiredAs),
		checkEnum("condition", in.Condition, conditions),
	)
}

func checkPrice(field string, cents *int64) *httpx.FieldError {
	if cents == nil {
		return nil
	}
	if *cents < 0 {
		return fieldErr(field, "cannot be negative")
	}
	if *cents > MaxPriceCents {
		return fieldErr(field, "cannot exceed 1000000 PLN")
	}
	return nil
}

func defaultTri(v string) string {
	if v = strings.TrimSpace(v); v == "" {
		return "unknown"
	}
	return v
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
