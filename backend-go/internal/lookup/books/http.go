// Package books holds the book barcode providers: Biblioteka Narodowa, Open
// Library and Google Books. Each looks up a normalized ISBN-13 and maps the
// catalog record onto a lookup.Candidate; the lookup service merges their
// answers into one book.
package books

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/text/unicode/norm"

	"github.com/Automaat/ohara/backend-go/internal/lookup"
	"github.com/Automaat/ohara/backend-go/internal/metrics"
)

const (
	userAgent = "ohara/1.0 (self-hosted media catalog)"
	maxBody   = 4 << 20
)

var errTooLarge = errors.New("response too large")

// getJSON fetches rawURL and returns its body, which must be JSON that
// parse reads. The outcome counts under source in the source health
// metrics, so a changed response shape is a parse error even with a 200;
// an ok also records how many candidates parse found.
func getJSON(ctx context.Context, client *http.Client, source, rawURL string, parse func(json.RawMessage) ([]lookup.Candidate, error)) (json.RawMessage, error) {
	body, status, err := fetchJSON(ctx, client, rawURL)
	if err != nil {
		metrics.ObserveSource(source, status)
		return nil, err
	}
	cands, err := parse(body)
	if err != nil {
		metrics.ObserveSource(source, metrics.SourceParseError)
		return nil, fmt.Errorf("parse: %w", err)
	}
	metrics.SetSourceResults(source, len(cands))
	metrics.ObserveSource(source, metrics.SourceOK)
	return body, nil
}

// fetchJSON is getJSON without the metrics; status is its ObserveSource
// outcome.
func fetchJSON(ctx context.Context, client *http.Client, rawURL string) (json.RawMessage, string, error) {
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, http.NoBody)
	if err != nil {
		return nil, metrics.SourceError, errors.New("build request: invalid URL")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, metrics.SourceError, fmt.Errorf("request: %w", withoutURL(err))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, metrics.SourceError, fmt.Errorf("read response: %w", withoutURL(err))
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "http_" + strconv.Itoa(resp.StatusCode), fmt.Errorf("status %d", resp.StatusCode)
	}
	if len(body) > maxBody {
		return nil, metrics.SourceError, errTooLarge
	}
	if !json.Valid(body) {
		return nil, metrics.SourceParseError, errors.New("response is not JSON")
	}
	return body, metrics.SourceOK, nil
}

// withoutURL drops the request URL from a client error: it can carry an API
// key, and provider errors reach logs and the lookup response.
func withoutURL(err error) error {
	if uerr, ok := errors.AsType[*url.Error](err); ok {
		return uerr.Err
	}
	return err
}

// clean normalizes a catalog string and drops the ISBD punctuation that
// MARC leaves at the end of a subfield ("Solaris /", "SuperNOWA,").
func clean(s string) string {
	s = strings.TrimRight(strings.TrimSpace(norm.NFC.String(s)), " /:;=,")
	if strings.HasSuffix(s, ".") && !strings.HasSuffix(s, "..") {
		s = strings.TrimSpace(strings.TrimSuffix(s, "."))
	}
	return s
}

// personName turns a catalog heading ("Sapkowski, Andrzej") into display
// order ("Andrzej Sapkowski"). A trailing initial keeps its period.
func personName(s string) string {
	s = strings.TrimRight(strings.TrimSpace(norm.NFC.String(s)), " ,")
	last, first, ok := strings.Cut(s, ", ")
	if !ok || strings.Contains(first, ",") {
		return s
	}
	return first + " " + last
}

var yearRe = regexp.MustCompile(`(?:^|\D)(\d{4})(?:\D|$)`)

// year returns the first four-digit year in s that the collection accepts.
func year(s string) *int {
	for _, m := range yearRe.FindAllStringSubmatch(s, -1) {
		y, err := strconv.Atoi(m[1])
		if err == nil && y >= 1800 && y <= 2200 {
			return &y
		}
	}
	return nil
}

// binding maps a free-text binding description to the edition enum.
func binding(s string) *string {
	s = strings.ToLower(s)
	var b string
	switch {
	case s == "":
		return nil
	case strings.Contains(s, "miękk"), strings.Contains(s, "paperback"), strings.Contains(s, "broszur"):
		b = "paperback"
	case strings.Contains(s, "tward"), strings.Contains(s, "hardcover"), strings.Contains(s, "hardback"):
		b = "hardcover"
	default:
		b = "other"
	}
	return &b
}

func dedupe(names []string) []string {
	out := names[:0]
	seen := map[string]bool{}
	for _, n := range names {
		if n != "" && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}
