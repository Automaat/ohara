package discogs

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// wantsPerPage is the longest page Discogs serves of a wantlist.
const wantsPerPage = 100

// maxWantPages bounds the pages one wantlist read follows: 10,000 wants.
const maxWantPages = 100

// ErrWantlistTooLong is a wantlist longer than maxWantPages pages.
var ErrWantlistTooLong = errors.New("discogs: wantlist longer than 10000 wants")

// Identity returns the username of the token's owner.
func (c *Client) Identity(ctx context.Context) (string, error) {
	var r struct {
		Username string `json:"username"`
	}
	err := c.get(ctx, "/oauth/identity", &r, func() error {
		if r.Username == "" {
			return errors.New("no username")
		}
		return nil
	})
	return r.Username, err
}

// Want is one release on a wantlist and when it was put there; DateAdded
// is zero when Discogs sent none that parses.
type Want struct {
	ID        int64
	DateAdded time.Time
}

// Wants returns the wants on the wantlist of username, every page of it.
func (c *Client) Wants(ctx context.Context, username string) ([]Want, error) {
	var wants []Want
	for page := 1; ; page++ {
		if page > maxWantPages {
			return nil, ErrWantlistTooLong
		}
		q := url.Values{"page": {strconv.Itoa(page)}, "per_page": {strconv.Itoa(wantsPerPage)}}
		var r struct {
			Pagination *struct {
				Pages int `json:"pages"`
			} `json:"pagination"`
			Wants []struct {
				ID        int64  `json:"id"`
				DateAdded string `json:"date_added"`
			} `json:"wants"`
		}
		if err := c.get(ctx, wantsPath(username)+"?"+q.Encode(), &r, func() error {
			if r.Pagination == nil {
				return errors.New("no pagination field")
			}
			return nil
		}); err != nil {
			return nil, err
		}
		for _, w := range r.Wants {
			if w.ID > 0 {
				added, err := time.Parse(time.RFC3339, w.DateAdded)
				if err != nil {
					added = time.Time{}
				}
				wants = append(wants, Want{ID: w.ID, DateAdded: added})
			}
		}
		if page >= r.Pagination.Pages {
			return wants, nil
		}
	}
}

// AddWant puts release id on the wantlist of username, with no notes or
// rating, and returns when the request left, after its turn in the
// budget.
func (c *Client) AddWant(ctx context.Context, username string, id int64) (time.Time, error) {
	var sent time.Time
	err := c.send(ctx, http.MethodPut, wantPath(username, id),
		[]int{http.StatusOK, http.StatusCreated, http.StatusNoContent}, nil, nil, &sent)
	if err != nil {
		return sent, fmt.Errorf("add want %d: %w", id, err)
	}
	return sent, nil
}

// RemoveWant takes release id off the wantlist of username; a want
// already gone (404) counts as removed.
func (c *Client) RemoveWant(ctx context.Context, username string, id int64) error {
	err := c.send(ctx, http.MethodDelete, wantPath(username, id),
		[]int{http.StatusOK, http.StatusNoContent, http.StatusNotFound}, nil, nil, nil)
	if err != nil {
		return fmt.Errorf("remove want %d: %w", id, err)
	}
	return nil
}

func wantsPath(username string) string {
	return "/users/" + url.PathEscape(username) + "/wants"
}

func wantPath(username string, id int64) string {
	return wantsPath(username) + "/" + strconv.FormatInt(id, 10)
}
