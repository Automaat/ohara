package discogscollection

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"time"

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

// ErrLinkedElsewhere is a copy whose instance of another release, the one
// its edition named before, is still in the collection: one copy makes
// one instance.
var ErrLinkedElsewhere = errors.New("copy already in the discogs collection as another release")

// LinkedElsewhereMessage is what the page shows for ErrLinkedElsewhere.
const LinkedElsewhereMessage = "Ten egzemplarz jest już w kolekcji Discogs jako inne wydanie."

// addSlack is how much earlier than Ohara's clock Discogs may date an
// instance an add made, so that the instance still counts as that add's.
const addSlack = 5 * time.Minute

// lockTimeout bounds how long a push holds its copy, whatever becomes of
// the request.
const lockTimeout = 2 * time.Minute

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
// until a push has added it and set its conditions.
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
	if id := t.Instance(); id != nil && t.Synced && t.ReleaseID != nil {
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
// RefusedError for a copy it cannot push, ErrForeignInstance when the
// release is in the collection in an instance the copy did not make,
// ErrLinkedElsewhere when the copy's instance of another release is still
// there, a FieldError when the collection's fields do not fit its grades
// and an UpstreamError when Discogs fails. The copy stays locked
// throughout; what the push learned of Discogs is kept whatever its
// outcome.
func (p *Pusher) Push(ctx context.Context, id int64) (State, error) {
	if p.client == nil {
		return State{}, ErrDisabled
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	dbctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), lockTimeout)
	defer cancel()
	lk, t, err := p.store.Lock(dbctx, id)
	if err != nil {
		return State{}, err
	}
	defer lk.Rollback(dbctx)
	if reason := refusal(&t); reason != "" {
		return State{}, &RefusedError{Reason: reason}
	}
	pushErr := p.push(ctx, dbctx, lk, &t)
	if _, ok := errors.AsType[*FieldError](pushErr); ok {
		return State{}, pushErr
	}
	if err := lk.Commit(dbctx); err != nil {
		return State{}, err
	}
	if pushErr != nil {
		return State{}, pushErr
	}
	return p.state(&t), nil
}

func (p *Pusher) push(ctx, dbctx context.Context, lk *Locked, t *Target) error {
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
	own, held, err := p.own(ctx, dbctx, lk, t)
	if err != nil {
		return err
	}
	for _, h := range held {
		if own == nil || h.InstanceID != own.InstanceID {
			return ErrForeignInstance
		}
	}
	if own == nil {
		if own, err = p.add(ctx, dbctx, lk, t); err != nil {
			return err
		}
	}
	for _, e := range sets {
		err := p.client.SetInstanceField(ctx, p.username, own.FolderID, release, own.InstanceID, e.field, e.value)
		if err != nil {
			return upstream(err)
		}
	}
	if err := lk.Synced(dbctx); err != nil {
		return err
	}
	t.Synced = true
	return nil
}

// own finds the instance t made of its edition's release among held, the
// release's instances in the collection, which it also returns. A link to
// an instance Discogs no longer has is dropped; a link to another
// release's instance that is still there is ErrLinkedElsewhere. An add
// whose answer never came is adopted when its instance is provably the
// one it made: alone and added after it was sent.
func (p *Pusher) own(ctx, dbctx context.Context, lk *Locked, t *Target) (*discogs.Instance, []discogs.Instance, error) {
	release := *t.ReleaseID
	if t.InstanceID != nil {
		linked := *t.InstanceReleaseID
		held, err := p.client.ReleaseInstances(ctx, p.username, linked)
		if err != nil {
			return nil, nil, upstream(err)
		}
		if i := slices.IndexFunc(held, func(h discogs.Instance) bool { return h.InstanceID == *t.InstanceID }); i >= 0 {
			if linked != release {
				return nil, nil, ErrLinkedElsewhere
			}
			return &held[i], held, nil
		}
		if err := lk.Forget(dbctx); err != nil {
			return nil, nil, err
		}
		t.InstanceID, t.InstanceReleaseID, t.Synced = nil, nil, false
	}
	held, err := p.client.ReleaseInstances(ctx, p.username, release)
	if err != nil {
		return nil, nil, upstream(err)
	}
	if t.AddReleaseID == nil {
		return nil, held, nil
	}
	pending := *t.AddReleaseID == release
	since := *t.AddSince
	t.AddReleaseID, t.AddSince = nil, nil
	if pending {
		if made := provenAdd(held, since); made != nil {
			return made, held, p.link(dbctx, lk, t, made.InstanceID)
		}
	}
	return nil, held, lk.ClearAdd(dbctx)
}

// provenAdd is the instance an add sent at since made: the release's only
// instance, added no earlier than since.
func provenAdd(held []discogs.Instance, since time.Time) *discogs.Instance {
	if len(held) != 1 {
		return nil
	}
	added, err := time.Parse(time.RFC3339, held[0].DateAdded)
	if err != nil || added.Before(since.Add(-addSlack)) {
		return nil
	}
	return &held[0]
}

func (p *Pusher) link(dbctx context.Context, lk *Locked, t *Target, instance int64) error {
	release := *t.ReleaseID
	if err := lk.Link(dbctx, release, instance); err != nil {
		return err
	}
	t.InstanceID, t.InstanceReleaseID, t.Synced = &instance, &release, false
	return nil
}

// add makes a new instance of t's release in the folder and links it. The
// add is marked before it is sent; when its answer does not come, the
// release's instances are read again and the add's instance adopted if
// provably its own, else the mark stays for a later push to settle.
func (p *Pusher) add(ctx, dbctx context.Context, lk *Locked, t *Target) (*discogs.Instance, error) {
	release := *t.ReleaseID
	since, err := lk.MarkAdd(dbctx, release)
	if err != nil {
		return nil, err
	}
	made, addErr := p.client.AddToFolder(ctx, p.username, p.folder, release)
	if addErr == nil {
		return &discogs.Instance{InstanceID: made, FolderID: p.folder}, p.link(dbctx, lk, t, made)
	}
	held, err := p.client.ReleaseInstances(dbctx, p.username, release)
	if err != nil {
		return nil, upstream(addErr)
	}
	adopted := provenAdd(held, since)
	if adopted == nil {
		return nil, upstream(addErr)
	}
	return adopted, p.link(dbctx, lk, t, adopted.InstanceID)
}
