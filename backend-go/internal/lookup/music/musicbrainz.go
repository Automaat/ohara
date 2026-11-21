package music

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Automaat/ohara/backend-go/internal/fetch"
	"github.com/Automaat/ohara/backend-go/internal/metrics"
)

// Source names in the source health metrics.
const (
	SourceMusicBrainz = "musicbrainz"
	SourceCoverArt    = "coverartarchive"
)

// Public endpoints.
const (
	MusicBrainzURL = "https://musicbrainz.org"
	CoverArtURL    = "https://coverartarchive.org"
)

// userAgent identifies the app with a contact URL, as MusicBrainz requires;
// anonymous clients are throttled hard.
const userAgent = "ohara/1.0 ( https://github.com/Automaat/ohara )"

// maxBody caps a response; a release search page is a few dozen KiB.
const maxBody = 4 << 20

// mbMaxResults bounds the releases a barcode search keeps.
const mbMaxResults = 10

// MBRelease is one MusicBrainz release, cut to what a candidate uses.
// Cover is the Cover Art Archive front thumbnail, filled in by the
// provider.
type MBRelease struct {
	ID           string     `json:"id"`
	Score        int        `json:"score"`
	Title        string     `json:"title"`
	Date         string     `json:"date,omitempty"`
	Country      string     `json:"country,omitempty"`
	Barcode      string     `json:"barcode,omitempty"`
	ArtistCredit []MBCredit `json:"artist-credit,omitempty"`
	LabelInfo    []MBLabel  `json:"label-info,omitempty"`
	Media        []MBMedium `json:"media,omitempty"`
	Cover        string     `json:"cover,omitempty"`
}

// MBCredit is one credited artist and the phrase joining it to the next.
type MBCredit struct {
	Name       string `json:"name"`
	JoinPhrase string `json:"joinphrase,omitempty"`
}

// MBLabel is a label and the catalog number it released under.
type MBLabel struct {
	CatalogNumber string `json:"catalog-number,omitempty"`
	Label         *struct {
		Name string `json:"name"`
	} `json:"label,omitempty"`
}

// MBMedium is one disc or tape of a release.
type MBMedium struct {
	Format string `json:"format,omitempty"`
}

// Artist joins the credited artists as MusicBrainz prints them.
func (r *MBRelease) Artist() string {
	var b strings.Builder
	for _, c := range r.ArtistCredit {
		b.WriteString(c.Name)
		b.WriteString(c.JoinPhrase)
	}
	return strings.TrimSpace(b.String())
}

// MusicBrainz searches MusicBrainz releases by barcode. No key is needed;
// MusicBrainz allows about one request a second.
type MusicBrainz struct {
	Client  *http.Client
	BaseURL string
}

// Releases returns the releases whose barcode is code. A 13-digit code
// with a leading zero is a UPC-A, which MusicBrainz usually stores with 12
// digits, so both spellings are asked.
func (m MusicBrainz) Releases(ctx context.Context, code string) ([]MBRelease, error) {
	query := "barcode:" + code
	if upc, ok := upcA(code); ok {
		query += " OR barcode:" + upc
	}
	base := m.BaseURL
	if base == "" {
		base = MusicBrainzURL
	}
	q := url.Values{"query": {query}, "fmt": {"json"}, "limit": {strconv.Itoa(mbMaxResults)}}
	body, err := get(ctx, m.Client, SourceMusicBrainz, base+"/ws/2/release?"+q.Encode(), http.StatusOK)
	if err != nil {
		return nil, err
	}
	var r struct {
		Releases *[]MBRelease `json:"releases"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, parseFailed(SourceMusicBrainz, err)
	}
	if r.Releases == nil {
		return nil, parseFailed(SourceMusicBrainz, errors.New("no releases field"))
	}
	res := *r.Releases
	if len(res) > mbMaxResults {
		res = res[:mbMaxResults]
	}
	found(SourceMusicBrainz, len(res))
	return res, nil
}

// CoverArt reads front covers from the Cover Art Archive.
type CoverArt struct {
	Client  *http.Client
	BaseURL string
}

// Front returns the front cover thumbnail of MusicBrainz release mbid, or
// "" when the archive has none.
func (c CoverArt) Front(ctx context.Context, mbid string) (string, error) {
	base := c.BaseURL
	if base == "" {
		base = CoverArtURL
	}
	body, err := get(ctx, c.Client, SourceCoverArt, base+"/release/"+url.PathEscape(mbid), http.StatusOK, http.StatusNotFound)
	if err != nil {
		return "", err
	}
	if body == nil {
		succeeded(SourceCoverArt)
		return "", nil
	}
	var r struct {
		Images *[]struct {
			Front      bool              `json:"front"`
			Image      string            `json:"image"`
			Thumbnails map[string]string `json:"thumbnails"`
		} `json:"images"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return "", parseFailed(SourceCoverArt, err)
	}
	if r.Images == nil {
		return "", parseFailed(SourceCoverArt, errors.New("no images field"))
	}
	succeeded(SourceCoverArt)
	for _, img := range *r.Images {
		if !img.Front {
			continue
		}
		for _, size := range []string{"250", "small", "500", "large"} {
			if u := img.Thumbnails[size]; u != "" {
				return httpsURL(u), nil
			}
		}
		return httpsURL(img.Image), nil
	}
	return "", nil
}

// httpsURL upgrades the archive's http links, which the PWA would block as
// mixed content.
func httpsURL(u string) string {
	if rest, ok := strings.CutPrefix(u, "http://"); ok {
		return "https://" + rest
	}
	return u
}

// upcA returns the 12-digit UPC-A of a normalized EAN-13 that starts with 0.
func upcA(code string) (string, bool) {
	if len(code) == 13 && code[0] == '0' {
		return code[1:], true
	}
	return "", false
}

// get fetches rawURL as JSON and returns the body of a response whose
// status is in ok; a 404 among ok returns a nil body. Failures count under
// source; the caller counts the ok once the body parses. Transport errors
// name the URL without its query.
func get(ctx context.Context, client *http.Client, source, rawURL string, ok ...int) ([]byte, error) {
	resp, err := fetch.Do(ctx, client, fetch.Request{
		URL:     rawURL,
		Header:  http.Header{"Accept": {"application/json"}, "User-Agent": {userAgent}},
		MaxBody: maxBody,
		OK:      ok,
	})
	if err != nil {
		metrics.ObserveSource(source, fetch.Outcome(err))
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	if resp.Status == http.StatusNotFound {
		return nil, nil
	}
	return resp.Body, nil
}

func succeeded(source string) { metrics.ObserveSource(source, metrics.SourceOK) }

func found(source string, n int) {
	metrics.SetSourceResults(source, n)
	succeeded(source)
}

func parseFailed(source string, err error) error {
	metrics.ObserveSource(source, metrics.SourceParseError)
	return fmt.Errorf("%s: parse: %w", source, err)
}
