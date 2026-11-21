// Package fetch sends one request to an outside source and reads its
// answer the same way for every source: the body is read up to the
// caller's cap and a longer one fails, the status must be one the caller
// accepts, and errors never carry the URL's query, which can hold an API
// key or a token.
package fetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"

	"github.com/Automaat/ohara/backend-go/internal/metrics"
)

// BrowserUserAgent is a browser's User-Agent, for sites that answer
// library defaults with a refusal or a challenge page.
const BrowserUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/130.0 Safari/537.36"

// ErrTooLarge is a body longer than the caller's cap.
var ErrTooLarge = errors.New("response too large")

// StatusError is an answer whose status the caller does not accept.
type StatusError struct{ Code int }

func (e StatusError) Error() string { return "status " + strconv.Itoa(e.Code) }

// StatusCode is the status the source answered.
func (e StatusError) StatusCode() int { return e.Code }

// Request is one request to a source.
type Request struct {
	// Method is GET when empty.
	Method string
	URL    string
	// Body is sent when not nil.
	Body   io.Reader
	Header http.Header
	// MaxBody is the longest body read; a longer one fails.
	MaxBody int64
	// OK lists the accepted statuses, 200 when empty.
	OK []int
}

// Response is an answer: its status, its headers, which can say when to
// retry, and its whole body when the status is accepted.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// Do sends r with client (http.DefaultClient when nil) and reads the body
// of an answer whose status r accepts. Any other status is a StatusError,
// returned with the answer's status and headers but no body; when that
// body also fails to read, the error names the failure and still wraps
// the StatusError, so a refusal stays a refusal however its page ends.
// Errors name the method, host and path at most, never the query.
func Do(ctx context.Context, client *http.Client, r Request) (Response, error) {
	method := r.Method
	if method == "" {
		method = http.MethodGet
	}
	body := r.Body
	if body == nil {
		body = http.NoBody
	}
	req, err := http.NewRequestWithContext(ctx, method, r.URL, body)
	if err != nil {
		return Response{}, errors.New("build request: invalid URL")
	}
	maps.Copy(req.Header, r.Header)
	if client == nil {
		client = http.DefaultClient
	}
	ok := r.OK
	if len(ok) == 0 {
		ok = []int{http.StatusOK}
	}
	resp, err := client.Do(req)
	if err != nil {
		return Response{}, fmt.Errorf("%s %s://%s%s: %w", method, req.URL.Scheme, req.URL.Host, req.URL.Path, withoutURL(err))
	}
	defer resp.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, r.MaxBody+1))
	if readErr != nil {
		readErr = withoutURL(readErr)
	} else if int64(len(data)) > r.MaxBody {
		readErr = ErrTooLarge
	}
	answer := Response{Status: resp.StatusCode, Header: resp.Header}
	if !slices.Contains(ok, resp.StatusCode) {
		se := StatusError{Code: resp.StatusCode}
		if readErr != nil {
			return answer, fmt.Errorf("%w (%w)", readErr, se)
		}
		return answer, se
	}
	if readErr != nil {
		return Response{}, readErr
	}
	answer.Body = data
	return answer, nil
}

// Outcome is the source health status (metrics.ObserveSource) of a Do
// that returned err: http_<code> for a status the caller did not accept,
// a parse error for a body over the cap and an error for anything else.
func Outcome(err error) string {
	if err == nil {
		return metrics.SourceOK
	}
	if se, ok := errors.AsType[StatusError](err); ok {
		return "http_" + strconv.Itoa(se.Code)
	}
	if errors.Is(err, ErrTooLarge) {
		return metrics.SourceParseError
	}
	return metrics.SourceError
}

// withoutURL drops the request URL from a client error.
func withoutURL(err error) error {
	if uerr, ok := errors.AsType[*url.Error](err); ok {
		return uerr.Err
	}
	return err
}
