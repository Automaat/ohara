package discogs

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// instancesPerPage is the longest page Discogs serves; nobody holds one
// release more times than that.
const instancesPerPage = 100

// Field is one field the instances of a user's collection carry, such as
// "Media Condition". A dropdown takes only one of its Options.
type Field struct {
	ID      int64    `json:"id"`
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Options []string `json:"options"`
}

// CollectionFields returns the fields of the collection of username.
func (c *Client) CollectionFields(ctx context.Context, username string) ([]Field, error) {
	var r struct {
		Fields *[]Field `json:"fields"`
	}
	if err := c.get(ctx, collectionPath(username)+"/fields", &r, func() error {
		if r.Fields == nil {
			return errors.New("no fields field")
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return *r.Fields, nil
}

// Instance is one copy of a release in a user's collection, in a folder.
type Instance struct {
	InstanceID int64 `json:"instance_id"`
	FolderID   int64 `json:"folder_id"`
}

// ReleaseInstances returns the instances of release id in the collection
// of username, in any folder; none is an empty slice. Discogs answers a
// release the collection lacks with an empty list or with a 404 and a
// message, and both mean none.
func (c *Client) ReleaseInstances(ctx context.Context, username string, id int64) ([]Instance, error) {
	q := url.Values{"per_page": {strconv.Itoa(instancesPerPage)}}
	var r struct {
		Releases *[]Instance `json:"releases"`
		Message  *string     `json:"message"`
	}
	err := c.send(ctx, http.MethodGet, collectionPath(username)+"/releases/"+strconv.FormatInt(id, 10)+"?"+q.Encode(),
		[]int{http.StatusOK, http.StatusNotFound}, &r, func() error {
			if r.Releases == nil && r.Message == nil {
				return errors.New("no releases field")
			}
			return nil
		})
	if err != nil {
		return nil, err
	}
	if r.Releases == nil {
		return []Instance{}, nil
	}
	return *r.Releases, nil
}

// AddToFolder adds release id to folder of the collection of username
// and returns the new instance's ID.
func (c *Client) AddToFolder(ctx context.Context, username string, folder, id int64) (int64, error) {
	var r struct {
		InstanceID int64 `json:"instance_id"`
	}
	err := c.send(ctx, http.MethodPost, folderReleasePath(username, folder, id),
		[]int{http.StatusOK, http.StatusCreated}, &r, func() error {
			if r.InstanceID <= 0 {
				return errors.New("no instance_id")
			}
			return nil
		})
	if err != nil {
		return 0, fmt.Errorf("add release %d: %w", id, err)
	}
	return r.InstanceID, nil
}

// SetInstanceField sets field of instance, of release id in folder of the
// collection of username, to value.
func (c *Client) SetInstanceField(ctx context.Context, username string, folder, id, instance, field int64, value string) error {
	q := url.Values{"value": {value}}
	path := folderReleasePath(username, folder, id) + "/instances/" + strconv.FormatInt(instance, 10) +
		"/fields/" + strconv.FormatInt(field, 10) + "?" + q.Encode()
	err := c.send(ctx, http.MethodPost, path, []int{http.StatusOK, http.StatusNoContent}, nil, nil)
	if err != nil {
		return fmt.Errorf("set field %d of instance %d: %w", field, instance, err)
	}
	return nil
}

func collectionPath(username string) string {
	return "/users/" + url.PathEscape(username) + "/collection"
}

func folderReleasePath(username string, folder, id int64) string {
	return collectionPath(username) + "/folders/" + strconv.FormatInt(folder, 10) + "/releases/" + strconv.FormatInt(id, 10)
}
