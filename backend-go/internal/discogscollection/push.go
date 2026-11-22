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

// settleAfter is how long after an add whose outcome is unknown Discogs
// can no longer make its instance: far past RequestTimeout. Until then an
// empty collection does not prove the add made nothing.
const settleAfter = 10 * time.Minute

// ErrAddUnknown is an add of the copy whose outcome is unknown while the
// release is in the collection: only the owner can say whether Ohara made
// that instance.
var ErrAddUnknown = errors.New("outcome of an earlier discogs add unknown")

// AddUnknownMessage is what the page shows for ErrAddUnknown.
const AddUnknownMessage = "Nie wiadomo, czy poprzednie dodanie do Discogs się udało. " +
	"Jeśli to wydanie w kolekcji Discogs dodała Ohara, potwierdź je; jeśli nie, porzuć to dodanie."

// ErrAddUnsettled is an add of the copy whose outcome is unknown, sent
// less than settleAfter ago, while the release is not in the collection.
var ErrAddUnsettled = errors.New("earlier discogs add not settled yet")

// AddUnsettledMessage is what the page shows for ErrAddUnsettled.
const AddUnsettledMessage = "Poprzednie dodanie do Discogs jeszcze się nie rozstrzygnęło. Spróbuj za kilka minut."

// ErrAdoptNotOne is an owner's confirmation of an add when the release is
// not in the collection exactly once.
var ErrAdoptNotOne = errors.New("release not held exactly once")

// AdoptNotOneMessage is what the page shows for ErrAdoptNotOne.
const AdoptNotOneMessage = "Nie można potwierdzić: tego wydania nie ma w kolekcji Discogs dokładnie raz."

// ErrNoUnknownAdd is a confirmation or discard without an add whose
// outcome is unknown.
var ErrNoUnknownAdd = errors.New("no discogs add with an unknown outcome")

// NoUnknownAddMessage is what the page shows for ErrNoUnknownAdd.
const NoUnknownAddMessage = "Nie ma dodania do Discogs, które czeka na potwierdzenie."

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

// UpstreamError is a Discogs request that failed. Unknown marks an add
// whose outcome is unknown: Discogs may have made the instance.
type UpstreamError struct {
	Err     error
	Unknown bool
}

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
	// AddUnknown says an earlier add's outcome is unknown: the owner
	// confirms or discards it.
	AddUnknown bool `json:"add_unknown"`
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
	s.AddUnknown = t.Pending != nil && t.Pending.InstanceID == nil
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
// instance it made before, and returns its new state; with adopt, the
// owner confirms that the release's only instance is the one an add of
// unknown outcome made. It answers ErrDisabled without a client,
// ErrNotFound for a missing copy, a RefusedError for a copy it cannot
// push, ErrForeignInstance when the release is in the collection in an
// instance the copy did not make, ErrLinkedElsewhere when the copy's
// instance of another release is still there, ErrAddUnknown,
// ErrAddUnsettled, ErrAdoptNotOne or ErrNoUnknownAdd while an earlier add
// is unsettled, a FieldError when the collection's fields do not fit its
// grades and an UpstreamError when Discogs fails. The copy stays locked
// throughout and is linked only once its instance's conditions are set.
func (p *Pusher) Push(ctx context.Context, id int64, adopt bool) (State, error) {
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
	linked, pushErr := p.push(ctx, dbctx, lk, &t, adopt)
	if _, ok := errors.AsType[*FieldError](pushErr); ok {
		return State{}, pushErr
	}
	if err := lk.Commit(dbctx); err != nil {
		return State{}, err
	}
	if linked && t.Pending != nil {
		if err := p.store.EndAdd(dbctx, id); err != nil {
			return State{}, err
		}
		t.Pending = nil
	}
	if pushErr != nil {
		return State{}, pushErr
	}
	return p.state(&t), nil
}

// Discard drops copy id's add whose outcome is unknown, at the owner's
// word that it made nothing of theirs, and returns its state.
func (p *Pusher) Discard(ctx context.Context, id int64) (State, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	dropped, err := p.store.DiscardUnknownAdd(ctx, id)
	if err != nil {
		return State{}, err
	}
	t, err := p.store.Target(ctx, id)
	if err != nil {
		return State{}, err
	}
	if !dropped {
		return State{}, ErrNoUnknownAdd
	}
	return p.state(&t), nil
}

// push sets t's grades on its instance, adding one when the copy has
// none, and reports whether it linked the copy.
func (p *Pusher) push(ctx, dbctx context.Context, lk *Locked, t *Target, adopt bool) (bool, error) {
	if p.username == "" {
		user, err := p.client.Identity(ctx)
		if err != nil {
			return false, upstream(err)
		}
		p.username = user
	}
	fields, err := p.client.CollectionFields(ctx, p.username)
	if err != nil {
		return false, upstream(err)
	}
	sets, err := edits(fields, t)
	if err != nil {
		return false, err
	}
	own, release, held, err := p.own(ctx, dbctx, lk, t, adopt)
	if err != nil {
		return false, err
	}
	for _, h := range held {
		if own == nil || h.InstanceID != own.InstanceID {
			return false, ErrForeignInstance
		}
	}
	if own == nil {
		if own, err = p.add(ctx, dbctx, t); err != nil {
			return false, err
		}
	}
	for _, e := range sets {
		err := p.client.SetInstanceField(ctx, p.username, own.FolderID, release, own.InstanceID, e.field, e.value)
		if err != nil {
			return false, upstream(err)
		}
	}
	if err := lk.Link(dbctx, release, own.InstanceID); err != nil {
		return false, err
	}
	t.InstanceID, t.InstanceReleaseID = &own.InstanceID, &release
	if release != *t.ReleaseID {
		return true, ErrLinkedElsewhere
	}
	return true, nil
}

// own finds the copy's instance, the release it is of and that release's
// instances in the collection. An unfinished add comes first: the
// instance Discogs answered it with, or, when its outcome is unknown, the
// release's only instance at the owner's word (adopt). Discogs' listing
// can lag behind its answer, so an answered instance missing from it, or
// an empty collection after an add of unknown outcome, settles only
// settleAfter after the add was sent: then the owner removed it, or it
// made nothing. Then the
// linked instance: one Discogs no longer has is dropped, one of another
// release still there is ErrLinkedElsewhere. With none, it returns the
// instances of the edition's release, which any add must find empty.
func (p *Pusher) own(ctx, dbctx context.Context, lk *Locked, t *Target, adopt bool) (*discogs.Instance, int64, []discogs.Instance, error) {
	release := *t.ReleaseID
	if pending := t.Pending; pending != nil {
		held, err := p.client.ReleaseInstances(ctx, p.username, pending.ReleaseID)
		if err != nil {
			return nil, 0, nil, upstream(err)
		}
		switch made := pending.InstanceID; {
		case made != nil:
			if i := slices.IndexFunc(held, func(h discogs.Instance) bool { return h.InstanceID == *made }); i >= 0 {
				return &held[i], pending.ReleaseID, held, nil
			}
			if time.Since(pending.Since) < settleAfter {
				return nil, 0, nil, ErrAddUnsettled
			}
		case adopt:
			if len(held) != 1 {
				return nil, 0, nil, ErrAdoptNotOne
			}
			return &held[0], pending.ReleaseID, held, nil
		case len(held) > 0:
			return nil, 0, nil, ErrAddUnknown
		case time.Since(pending.Since) < settleAfter:
			return nil, 0, nil, ErrAddUnsettled
		}
		if err := p.store.EndAdd(dbctx, t.CopyID); err != nil {
			return nil, 0, nil, err
		}
		t.Pending = nil
	}
	if adopt {
		return nil, 0, nil, ErrNoUnknownAdd
	}
	if t.InstanceID != nil {
		linked := *t.InstanceReleaseID
		held, err := p.client.ReleaseInstances(ctx, p.username, linked)
		if err != nil {
			return nil, 0, nil, upstream(err)
		}
		if i := slices.IndexFunc(held, func(h discogs.Instance) bool { return h.InstanceID == *t.InstanceID }); i >= 0 {
			if linked != release {
				return nil, 0, nil, ErrLinkedElsewhere
			}
			return &held[i], release, held, nil
		}
		if err := lk.Forget(dbctx); err != nil {
			return nil, 0, nil, err
		}
		t.InstanceID, t.InstanceReleaseID = nil, nil
	}
	held, err := p.client.ReleaseInstances(ctx, p.username, release)
	if err != nil {
		return nil, 0, nil, upstream(err)
	}
	return nil, release, held, nil
}

// add makes a new instance of t's release in the folder. The add is
// recorded and committed before it is sent. Discogs refusing it (a 4xx)
// drops the record; any other failure leaves the outcome unknown and the
// record in place, so no other add of the copy is sent until it is
// settled. The instance Discogs answers with is recorded at once; the
// copy is linked only after its conditions are set.
func (p *Pusher) add(ctx, dbctx context.Context, t *Target) (*discogs.Instance, error) {
	release := *t.ReleaseID
	if err := p.store.BeginAdd(dbctx, t.CopyID, release); err != nil {
		return nil, err
	}
	t.Pending = &Pending{ReleaseID: release, Since: time.Now()}
	made, err := p.client.AddToFolder(ctx, p.username, p.folder, release)
	if err != nil {
		if se, ok := errors.AsType[discogs.StatusError](err); ok && se.Code >= 400 && se.Code < 500 {
			if endErr := p.store.EndAdd(dbctx, t.CopyID); endErr != nil {
				return nil, endErr
			}
			t.Pending = nil
			return nil, upstream(err)
		}
		return nil, &UpstreamError{Err: err, Unknown: true}
	}
	if err := p.store.AddMade(dbctx, t.CopyID, made); err != nil {
		return nil, err
	}
	t.Pending.InstanceID = &made
	return &discogs.Instance{InstanceID: made, FolderID: p.folder}, nil
}
