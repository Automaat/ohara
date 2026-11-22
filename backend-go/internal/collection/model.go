// Package collection owns titles, their editions and the copies on the shelf
// or on the wishlist.
package collection

import (
	"encoding/json"
	"errors"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Automaat/ohara/backend-go/internal/barcode"
	"github.com/Automaat/ohara/backend-go/internal/httpx"
)

const (
	KindMovie = "movie"
	KindBook  = "book"
	KindMusic = "music"

	StatusOwned    = "owned"
	StatusWishlist = "wishlist"
	// StatusSold is a copy that left the shelf: it keeps its purchase and
	// adds the day it was sold and, when known, what it sold for.
	StatusSold = "sold"
	// StatusOrdered is a copy bought but not yet delivered: it keeps its
	// price, the day it was ordered and the shop, and may name the day it is
	// expected. It turns owned, keeping all of that, once it arrives.
	StatusOrdered = "ordered"

	maxText    = 500
	maxNotes   = 10000
	maxAuthors = 20
	maxTags    = 20
	// MaxPriceCents (1 000 000 zł) keeps collection totals far from int64 overflow.
	MaxPriceCents = 100_000_000
	// MinRating and MaxRating bound a title's rating: the 1 to 10 scale
	// Filmweb and lubimyczytac.pl use.
	MinRating = 1
	MaxRating = 10
)

var (
	kinds        = []string{KindMovie, KindBook, KindMusic}
	movieFormats = []string{"dvd", "bluray", "uhd"}
	musicFormats = []string{"cd", "vinyl", "cassette"}
	formats      = slices.Concat(movieFormats, musicFormats)
	bindings     = []string{"hardcover", "paperback", "other"}
	triStates    = []string{"yes", "no", "unknown"}
	packagings   = []string{"steelbook", "slipcover", "digipak", "mediabook"}
	statuses     = []string{StatusOwned, StatusWishlist, StatusSold, StatusOrdered}
	acquiredAs   = []string{"new", "used"}
	conditions   = []string{"mint", "very_good", "good", "fair", "poor"}
)

// Enums lists the values each enum field of this package's JSON accepts, by
// the name its `ts` struct tag gives, for the SPA's generated types.
func Enums() map[string][]string {
	return map[string][]string{
		"Kind":        slices.Clone(kinds),
		"MovieFormat": slices.Clone(movieFormats),
		"MusicFormat": slices.Clone(musicFormats),
		"Format":      slices.Clone(formats),
		"Binding":     slices.Clone(bindings),
		"TriState":    slices.Clone(triStates),
		"Packaging":   slices.Clone(packagings),
		"CopyStatus":  slices.Clone(statuses),
		"AcquiredAs":  slices.Clone(acquiredAs),
		"Condition":   slices.Clone(conditions),
	}
}

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
	ErrNotOwned         = errors.New("only owned copies can be lent")
	ErrAlreadyLent      = errors.New("this copy is already lent")
	ErrLentCopyStays    = errors.New("a lent copy stays owned until it comes back")
	ErrReturned         = errors.New("this loan is already returned")
	// ErrNotOrdered refuses to receive a copy that is not on order.
	ErrNotOrdered = errors.New("only ordered copies can be received")
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
	Kind          string   `json:"kind" ts:"Kind"`
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
	// Rating is the owner's own rating, a whole number from MinRating to
	// MaxRating; null clears it.
	Rating *int `json:"rating,omitempty" ts:",null"`
	// KeepRating makes an update leave the stored rating as it is, for a
	// PUT body without rating. Creates ignore it.
	KeepRating bool `json:"-"`
	// Tags are the owner's own groupings of the work. An update without
	// them (nil) keeps the stored tags; an empty list clears them.
	Tags TagList `json:"tags,omitempty"`
	// RequestKey makes a create safe to retry: a second create with the
	// same key returns the first title. Updates ignore it.
	RequestKey *string `json:"request_key,omitempty"`
}

// RatingInput is the body of PUT /api/titles/{id}/rating: the title's new
// rating, or null to clear it. It changes nothing else of the title.
type RatingInput struct {
	Rating *int `json:"rating"`
}

// Title is a work: a film, a book or an album.
type Title struct {
	ID int64 `json:"id"`
	TitleInput
	// Tags shadows TitleInput.Tags so an answer always lists them, each
	// with the spelling it was first saved with, in the title's order.
	Tags         []string  `json:"tags"`
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
	Format      *string  `json:"format" ts:"Format"`
	PLSubtitles string   `json:"pl_subtitles" ts:"TriState"`
	PLVoiceover string   `json:"pl_voiceover" ts:"TriState"`
	Packaging   []string `json:"packaging" ts:"Packaging"`
	Binding     *string  `json:"binding" ts:"Binding"`
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
// apply only to owned, sold and ordered copies, the sale's day and price only
// to sold ones, the expected delivery only to ordered ones; a sold copy needs
// its day. MaxPriceCents, the most a wishlist copy may
// cost, and MaxMarketPercent, the share of the used market price it may
// cost, apply only to wishlist copies: the deal hunter alerts on listings
// at or below either. So does ExactEdition, which keeps the hunter to
// listings naming the edition's details.
type CopyInput struct {
	EditionID     int64   `json:"edition_id"`
	Status        string  `json:"status" ts:"CopyStatus"`
	PriceCents    *int64  `json:"price_cents"`
	PurchasedOn   *Date   `json:"purchased_on"`
	Shop          string  `json:"shop"`
	AcquiredAs    *string `json:"acquired_as" ts:"AcquiredAs"`
	Condition     *string `json:"condition" ts:"Condition"`
	Notes         string  `json:"notes"`
	MaxPriceCents *int64  `json:"max_price_cents"`
	// MaxMarketPercent is 1 to 100: a listing at or under that percent of
	// the used market price of the wished title and format alerts.
	MaxMarketPercent *int `json:"max_market_percent,omitempty" ts:",null"`
	// ExactEdition makes a wish of a movie or book match only listings
	// that name its edition's packaging, Polish voice-over or subtitles
	// and binding; false matches every edition of the title and format.
	ExactEdition bool `json:"exact_edition,omitempty"`
	// SoldOn is the day a sold copy was sold; SalePriceCents what it sold
	// for, null when unknown.
	SoldOn         *Date  `json:"sold_on,omitempty" ts:",null"`
	SalePriceCents *int64 `json:"sale_price_cents,omitempty" ts:",null"`
	// ExpectedOn is the day an ordered copy should arrive, null when unknown.
	ExpectedOn *Date `json:"expected_on,omitempty" ts:",null"`
	// Location is where an owned copy is stored, such as "Regał 2"; a
	// wishlist, sold or ordered copy has none.
	Location *string `json:"location,omitempty" ts:",null"`
	// RequestKey makes a create safe to retry: a second create with the
	// same key returns the first copy. Updates ignore it.
	RequestKey *string `json:"request_key,omitempty"`
}

// maxRequestKey caps a create's RequestKey, in bytes.
const maxRequestKey = 100

// normalizeRequestKey trims a create's request key; blank is none. Postgres
// text takes neither a NUL byte nor invalid UTF-8, which a path can carry.
func normalizeRequestKey(key *string) (*string, *httpx.FieldError) {
	trimmed := trimOpt(key)
	switch {
	case trimmed == nil:
	case len(*trimmed) > maxRequestKey:
		return nil, fieldErr("request_key", "is too long")
	case strings.ContainsRune(*trimmed, 0) || !utf8.ValidString(*trimmed):
		return nil, fieldErr("request_key", "is not valid")
	}
	return trimmed, nil
}

// Copy is one physical item, owned, wanted, sold or on order.
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

// checkText refuses control characters in one-line text: Postgres text
// cannot hold a NUL, and a line break or tab would not survive the form's
// one-line field, which would rewrite the value on its next save.
func checkText(field, value string) *httpx.FieldError {
	if strings.ContainsFunc(value, unicode.IsControl) {
		return fieldErr(field, "is not valid")
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

// ratingMsg says what a rating may be, for any value refused.
const ratingMsg = "must be a whole number from 1 to 10"

func checkRating(rating *int) *httpx.FieldError {
	if rating != nil && (*rating < MinRating || *rating > MaxRating) {
		return fieldErr("rating", ratingMsg)
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
	if in.Tags != nil {
		tags, fe := normalizeTags(in.Tags)
		if fe != nil {
			return fe
		}
		in.Tags = tags
	}

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
		checkRating(in.Rating),
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

// TagList is the tags a title body carries: a JSON array of strings or
// null. An element that is not a string, null included, is refused as a
// type error on `tags` instead of read as a blank tag, which would drop it
// and clear the title's tags. encoding/json adds no field to an error an
// UnmarshalJSON returns, so the type names its one field itself.
type TagList []string

// UnmarshalJSON reads a JSON array of strings, or null as no list.
func (l *TagList) UnmarshalJSON(b []byte) error {
	var raw []*string
	if err := json.Unmarshal(b, &raw); err != nil {
		if typeErr, ok := errors.AsType[*json.UnmarshalTypeError](err); ok {
			typeErr.Field = "tags"
		}
		return err
	}
	if raw == nil {
		*l = nil
		return nil
	}
	tags := make(TagList, 0, len(raw))
	for _, tag := range raw {
		if tag == nil {
			return &json.UnmarshalTypeError{Value: "null", Type: reflect.TypeFor[string](), Field: "tags"}
		}
		tags = append(tags, *tag)
	}
	*l = tags
	return nil
}

// normalizeTags trims each tag, drops blank ones and keeps the first of
// those that differ only in case, in their order. A non-nil input gives a
// non-nil list, so an empty one still clears a title's tags.
func normalizeTags(raw TagList) (TagList, *httpx.FieldError) {
	tags := make(TagList, 0, len(raw))
	seen := make(map[string]bool, len(raw))
	for _, tag := range raw {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if e := checkLen("tags", tag, maxText); e != nil {
			return nil, e
		}
		// Postgres text takes neither a NUL byte nor invalid UTF-8.
		if strings.ContainsRune(tag, 0) || !utf8.ValidString(tag) {
			return nil, fieldErr("tags", "is not valid")
		}
		key := foldCase(tag)
		if seen[key] {
			continue
		}
		seen[key] = true
		tags = append(tags, tag)
	}
	if len(tags) > maxTags {
		return nil, fieldErr("tags", "too many tags")
	}
	return tags, nil
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
	in.Location = trimOpt(in.Location)
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
	if e := checkPrice("sale_price_cents", in.SalePriceCents); e != nil {
		return e
	}
	if in.Status != StatusWishlist && in.MaxPriceCents != nil {
		return fieldErr("max_price_cents", "applies only to wishlist copies")
	}
	if p := in.MaxMarketPercent; p != nil {
		if *p < 1 || *p > 100 {
			return fieldErr("max_market_percent", "must be between 1 and 100")
		}
		if in.Status != StatusWishlist {
			return fieldErr("max_market_percent", "applies only to wishlist copies")
		}
	}
	if in.ExactEdition && in.Status != StatusWishlist {
		return fieldErr("exact_edition", "applies only to wishlist copies")
	}
	if in.Status == StatusWishlist {
		if in.PriceCents != nil {
			return fieldErr("price_cents", "applies only to owned, sold and ordered copies")
		}
		if in.PurchasedOn != nil {
			return fieldErr("purchased_on", "applies only to owned, sold and ordered copies")
		}
	}
	if in.ExpectedOn != nil && in.Status != StatusOrdered {
		return fieldErr("expected_on", "applies only to ordered copies")
	}
	if in.Location != nil && in.Status != StatusOwned {
		return fieldErr("location", "applies only to owned copies")
	}
	if e := in.checkSale(); e != nil {
		return e
	}
	return firstErr(
		checkLen("shop", in.Shop, maxText),
		checkLen("location", deref(in.Location), maxText),
		checkText("location", deref(in.Location)),
		checkLen("notes", in.Notes, maxNotes),
		checkEnum("acquired_as", in.AcquiredAs, acquiredAs),
		checkEnum("condition", in.Condition, conditions),
	)
}

// checkSale requires the day of a sold copy's sale and keeps a sale off any
// other copy.
func (in *CopyInput) checkSale() *httpx.FieldError {
	if in.Status == StatusSold {
		if in.SoldOn == nil {
			return fieldErr("sold_on", "is required for sold copies")
		}
		return nil
	}
	if in.SoldOn != nil {
		return fieldErr("sold_on", "applies only to sold copies")
	}
	if in.SalePriceCents != nil {
		return fieldErr("sale_price_cents", "applies only to sold copies")
	}
	return nil
}

// LoanInput is the body of POST /api/copies/{id}/loans: who borrows an owned
// copy, since when (today in Poland when null) and until when (optional).
type LoanInput struct {
	Borrower string `json:"borrower"`
	LentOn   *Date  `json:"lent_on"`
	DueOn    *Date  `json:"due_on"`
}

// Normalize trims the input in place and checks it. A due date before a
// lent_on left out is checked against today by the store.
func (in *LoanInput) Normalize() *httpx.FieldError {
	in.Borrower = strings.TrimSpace(in.Borrower)
	switch {
	case in.Borrower == "":
		return fieldErr("borrower", "cannot be empty")
	case len(in.Borrower) > maxText:
		return fieldErr("borrower", "is too long")
	case strings.ContainsRune(in.Borrower, 0):
		return fieldErr("borrower", "is not valid")
	}
	if in.LentOn != nil {
		return checkDue(in.DueOn, in.LentOn.Time)
	}
	return nil
}

// checkDue refuses a due date before the day the copy was lent.
func checkDue(due *Date, lentOn time.Time) *httpx.FieldError {
	if due != nil && due.Before(lentOn) {
		return fieldErr("due_on", "cannot be before lent_on")
	}
	return nil
}

// ReturnInput is the body of PUT /api/loans/{id}: the day the copy came back.
type ReturnInput struct {
	ReturnedOn *Date `json:"returned_on"`
}

// Loan is one lending of an owned copy; ReturnedOn is null while it is out.
type Loan struct {
	ID         int64     `json:"id"`
	CopyID     int64     `json:"copy_id"`
	Borrower   string    `json:"borrower"`
	LentOn     Date      `json:"lent_on"`
	DueOn      *Date     `json:"due_on"`
	ReturnedOn *Date     `json:"returned_on"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
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
