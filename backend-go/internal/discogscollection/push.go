package discogscollection

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"sync"

	"github.com/Automaat/ohara/backend-go/internal/discogs"
)

// DefaultFolder is the folder a push adds to: Discogs' "Uncategorized".
const DefaultFolder = 1

// mediaField is the collection field of the disc's or tape's grade, as
// Discogs names it.
const mediaField = "Media Condition"

// sleeveField is the collection field of the sleeve's grade.
const sleeveField = "Sleeve Condition"

// gradeOptions names each Goldmine grade a copy stores as the option of
// the Discogs condition fields.
var gradeOptions = map[string]string{
	"mint":           "Mint (M)",
	"near_mint":      "Near Mint (NM or M-)",
	"very_good_plus": "Very Good Plus (VG+)",
	"very_good":      "Very Good (VG)",
	"good_plus":      "Good Plus (G+)",
	"good":           "Good (G)",
	"fair":           "Fair (F)",
	"poor":           "Poor (P)",
	"generic":        "Generic",
	"not_graded":     "Not Graded",
	"no_cover":       "No Cover",
}

// ErrDisabled is a push without OHARA_DISCOGS_TOKEN.
var ErrDisabled = errors.New("discogs collection disabled")

// ErrForeignInstance is a release already in the collection in an
// instance the copy did not make.
var ErrForeignInstance = errors.New("release already in the discogs collection")

// ForeignInstanceMessage is what the page shows for ErrForeignInstance.
const ForeignInstanceMessage = "To wydanie jest już w Twojej kolekcji Discogs."

// RefusedError is a copy that cannot be pushed; Reason says why, in
// Polish.
type RefusedError struct{ Reason string }

func (e *RefusedError) Error() string { return e.Reason }

// FieldError is a collection whose condition field lacks the field or the
// option a grade needs.
type FieldError struct{ Field, Option string }

func (e *FieldError) Error() string {
	return fmt.Sprintf("discogs collection has no field %q with option %q", e.Field, e.Option)
}

// UpstreamError is a Discogs request that failed.
type UpstreamError struct{ Err error }

func (e *UpstreamError) Error() string { return e.Err.Error() }

func (e *UpstreamError) Unwrap() error { return e.Err }

// upstream marks a client call's failure as Discogs'.
func upstream(err error) error {
	if err == nil {
		return nil
	}
	return &UpstreamError{Err: err}
}

// State is the GET and POST /api/copies/{id}/discogs body. Enabled says a
// token is set; Pushable that the copy is an owned album copy whose
// edition names a Discogs release. InstanceID is the instance the copy
// made of that release, with ReleaseURL its page on Discogs; both null
// before the first push.
type State struct {
	Enabled    bool    `json:"enabled"`
	Pushable   bool    `json:"pushable"`
	InstanceID *int64  `json:"instance_id"`
	ReleaseURL *string `json:"release_url"`
}

// Pusher adds copies to the Discogs collection, one push at a time, so
// two pushes of a copy cannot each add an instance.
type Pusher struct {
	client *discogs.Client
	store  *Store
	folder int64

	mu       sync.Mutex
	username string
}

// NewPusher pushes through client to folder; a nil client disables it.
func NewPusher(store *Store, client *discogs.Client, folder int64) *Pusher {
	return &Pusher{client: client, store: store, folder: folder}
}

// refusal says why t cannot be pushed, empty when it can.
func refusal(t *Target) string {
	switch {
	case t.Kind != "music":
		return "Do kolekcji Discogs można dodać tylko album."
	case t.Status != "owned":
		return "Do kolekcji Discogs można dodać tylko posiadany egzemplarz."
	case t.ReleaseID == nil:
		return "To wydanie nie ma numeru wydania Discogs."
	}
	return ""
}

func (p *Pusher) state(t *Target) State {
	s := State{Enabled: p.client != nil}
	s.Pushable = s.Enabled && refusal(t) == ""
	if id := t.Instance(); id != nil && t.ReleaseID != nil {
		s.InstanceID = id
		u := "https://www.discogs.com/release/" + strconv.FormatInt(*t.ReleaseID, 10)
		s.ReleaseURL = &u
	}
	return s
}

// State returns the Discogs state of copy id, or ErrNotFound.
func (p *Pusher) State(ctx context.Context, id int64) (State, error) {
	t, err := p.store.Target(ctx, id)
	if err != nil {
		return State{}, err
	}
	return p.state(&t), nil
}

// edit is one condition field a push sets.
type edit struct {
	field int64
	value string
}

// edits are the condition fields t's grades set, from the field IDs and
// options fields lists; a grade the copy lacks sets nothing.
func edits(fields []discogs.Field, t *Target) ([]edit, error) {
	var out []edit
	for _, g := range []struct {
		field string
		grade *string
	}{{mediaField, t.MediaGrade}, {sleeveField, t.SleeveGrade}} {
		if g.grade == nil {
			continue
		}
		option := gradeOptions[*g.grade]
		i := slices.IndexFunc(fields, func(f discogs.Field) bool { return f.Name == g.field })
		if option == "" || i < 0 || !slices.Contains(fields[i].Options, option) {
			return nil, &FieldError{Field: g.field, Option: option}
		}
		out = append(out, edit{field: fields[i].ID, value: option})
	}
	return out, nil
}

// Push adds copy id to the collection, or updates the conditions of the
// instance it made before, and returns its new state. It answers
// ErrDisabled without a client, ErrNotFound for a missing copy, a
// RefusedError for a copy it cannot push, ErrForeignInstance for a
// release the collection already holds in an instance the copy did not
// make, a FieldError when the collection's fields do not fit its grades
// and an UpstreamError when Discogs fails.
func (p *Pusher) Push(ctx context.Context, id int64) (State, error) {
	if p.client == nil {
		return State{}, ErrDisabled
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	t, err := p.store.Target(ctx, id)
	if err != nil {
		return State{}, err
	}
	if reason := refusal(&t); reason != "" {
		return State{}, &RefusedError{Reason: reason}
	}
	if err := p.push(ctx, &t); err != nil {
		return State{}, err
	}
	return p.state(&t), nil
}

func (p *Pusher) push(ctx context.Context, t *Target) error {
	release := *t.ReleaseID
	if p.username == "" {
		user, err := p.client.Identity(ctx)
		if err != nil {
			return upstream(err)
		}
		p.username = user
	}
	fields, err := p.client.CollectionFields(ctx, p.username)
	if err != nil {
		return upstream(err)
	}
	sets, err := edits(fields, t)
	if err != nil {
		return err
	}
	held, err := p.client.ReleaseInstances(ctx, p.username, release)
	if err != nil {
		return upstream(err)
	}
	var inst discogs.Instance
	if own := t.Instance(); own != nil {
		if i := slices.IndexFunc(held, func(h discogs.Instance) bool { return h.InstanceID == *own }); i >= 0 {
			inst = held[i]
		}
	}
	if inst.InstanceID == 0 {
		if inst, err = p.add(ctx, t, held); err != nil {
			return err
		}
	}
	for _, e := range sets {
		err := p.client.SetInstanceField(ctx, p.username, inst.FolderID, release, inst.InstanceID, e.field, e.value)
		if err != nil {
			return upstream(err)
		}
	}
	return nil
}

// add makes a new instance of t's release in the folder, unless held,
// the release's instances, has one; an instance t stored before that is
// gone from Discogs, or is of another release, is forgotten first. The
// new instance is stored even when the request has ended, since Discogs
// has made it.
func (p *Pusher) add(ctx context.Context, t *Target, held []discogs.Instance) (discogs.Instance, error) {
	release := *t.ReleaseID
	if t.InstanceID != nil {
		if err := p.store.ForgetInstance(ctx, t.CopyID, *t.InstanceID); err != nil {
			return discogs.Instance{}, err
		}
		t.InstanceID, t.InstanceReleaseID = nil, nil
	}
	if len(held) > 0 {
		return discogs.Instance{}, ErrForeignInstance
	}
	made, err := p.client.AddToFolder(ctx, p.username, p.folder, release)
	if err != nil {
		return discogs.Instance{}, upstream(err)
	}
	if err := p.store.SaveInstance(context.WithoutCancel(ctx), t.CopyID, release, made); err != nil {
		return discogs.Instance{}, err
	}
	t.InstanceID, t.InstanceReleaseID = &made, &release
	return discogs.Instance{InstanceID: made, FolderID: p.folder}, nil
}
