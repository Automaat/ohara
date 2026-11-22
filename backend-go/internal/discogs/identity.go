package discogs

import (
	"context"
	"errors"
)

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
