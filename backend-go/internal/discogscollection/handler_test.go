package discogscollection_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Automaat/ohara/backend-go/internal/collection"
	"github.com/Automaat/ohara/backend-go/internal/dbtest"
	"github.com/Automaat/ohara/backend-go/internal/discogs"
	"github.com/Automaat/ohara/backend-go/internal/discogscollection"
	"github.com/Automaat/ohara/backend-go/internal/server"
)

const (
	token    = "secret-discogs-token"
	owner    = "vinyl owner"
	release  = 1873013
	mediaID  = 7
	sleeveID = 9
)

type obj = map[string]any

// grades are the condition options Discogs lists for both fields.
var grades = []string{
	"Mint (M)", "Near Mint (NM or M-)", "Very Good Plus (VG+)", "Very Good (VG)",
	"Good Plus (G+)", "Good (G)", "Fair (F)", "Poor (P)",
}

// fieldEdit is one add or field change the double received.
type fieldEdit struct {
	folder, release, instance, field int64
	value                            string
}

// double is a Discogs collection: the releases its owner holds, with
// each instance's folder, the adds and field edits it received, and a
// status every request under failPath answers with when fail is set.
type double struct {
	mu        sync.Mutex
	t         *testing.T
	instances map[int64][]discogs.Instance
	fields    []obj
	adds      []fieldEdit
	edits     []fieldEdit
	requests  int
	fail      int
	failPath  string
	nextID    int64
	// cutAdd makes the next adds answer 201 with a body cut short, and
	// cutList the release lookups after such an add fail with 502.
	cutAdd, cutList int
	cutDone         bool
	// beforeLookup runs, unlocked, before every release lookup.
	beforeLookup func()
	// onAdd runs before an add is made.
	onAdd func()
	// lateAdds answer the next adds 503 and make their instance only when
	// commitLate runs.
	lateAdds int
	late     []func()
	// hidden releases answer their lookups 404, as a lagging listing.
	hidden map[int64]bool
}

func newDouble(t *testing.T) (*double, *httptest.Server) {
	t.Helper()
	d := &double{
		t:         t,
		instances: map[int64][]discogs.Instance{},
		nextID:    900,
		fields: []obj{
			{"id": mediaID, "name": "Media Condition", "type": "dropdown", "options": grades, "position": 1, "public": true},
			{
				"id": sleeveID, "name": "Sleeve Condition", "type": "dropdown",
				"options": append(append([]string{}, grades...), "Generic", "Not Graded", "No Cover"), "position": 2, "public": true,
			},
			{"id": 3, "name": "Notes", "type": "textarea", "lines": 3, "position": 3, "public": false},
		},
	}
	user := "/users/" + strings.ReplaceAll(owner, " ", "%20") + "/collection"
	mux := http.NewServeMux()
	mux.HandleFunc("GET /oauth/identity", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, obj{"id": 1, "username": owner, "resource_url": "x", "consumer_name": "ohara"})
	})
	mux.HandleFunc("GET "+user+"/fields", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, obj{"fields": d.fields})
	})
	mux.HandleFunc("GET "+user+"/releases/{release}", func(w http.ResponseWriter, r *http.Request) {
		if d.cutList > 0 && d.cutDone {
			d.cutList--
			writeJSON(w, http.StatusBadGateway, obj{"message": "down"})
			return
		}
		id := pathInt(r, "release")
		held := d.instances[id]
		if len(held) == 0 || d.hidden[id] {
			writeJSON(w, http.StatusNotFound, obj{"message": "Release not found in collection."})
			return
		}
		out := []obj{}
		for _, h := range held {
			out = append(out, obj{
				"id": id, "instance_id": h.InstanceID, "folder_id": h.FolderID, "rating": 0, "date_added": h.DateAdded,
			})
		}
		writeJSON(w, http.StatusOK, obj{"pagination": obj{"page": 1, "pages": 1}, "releases": out})
	})
	mux.HandleFunc("POST "+user+"/folders/{folder}/releases/{release}", func(w http.ResponseWriter, r *http.Request) {
		if d.onAdd != nil {
			d.onAdd()
		}
		folder, id := pathInt(r, "folder"), pathInt(r, "release")
		if d.lateAdds > 0 {
			d.lateAdds--
			d.late = append(d.late, func() {
				d.nextID++
				d.instances[id] = append(d.instances[id], discogs.Instance{InstanceID: d.nextID, FolderID: folder})
				d.adds = append(d.adds, fieldEdit{folder: folder, release: id, instance: d.nextID})
			})
			writeJSON(w, http.StatusServiceUnavailable, obj{"message": "busy"})
			return
		}
		d.nextID++
		d.instances[id] = append(d.instances[id], discogs.Instance{
			InstanceID: d.nextID, FolderID: folder, DateAdded: time.Now().Format(time.RFC3339),
		})
		d.adds = append(d.adds, fieldEdit{folder: folder, release: id, instance: d.nextID})
		if d.cutAdd > 0 {
			d.cutAdd--
			d.cutDone = true
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Length", "100")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"instance_id": 9`))
			return
		}
		writeJSON(w, http.StatusCreated, obj{"instance_id": d.nextID, "resource_url": "x"})
	})
	mux.HandleFunc("POST "+user+"/folders/{folder}/releases/{release}/instances/{instance}/fields/{field}",
		func(w http.ResponseWriter, r *http.Request) {
			d.edits = append(d.edits, fieldEdit{
				folder: pathInt(r, "folder"), release: pathInt(r, "release"),
				instance: pathInt(r, "instance"), field: pathInt(r, "field"), value: r.URL.Query().Get("value"),
			})
			w.WriteHeader(http.StatusNoContent)
		})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/collection/releases/") {
			d.mu.Lock()
			hook := d.beforeLookup
			d.mu.Unlock()
			if hook != nil {
				hook()
			}
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		d.requests++
		if r.Header.Get("Authorization") != "Discogs token="+token {
			t.Errorf("%s %s: Authorization = %q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		}
		if d.fail != 0 && strings.HasPrefix(r.Method+" "+r.URL.EscapedPath(), d.failPath) {
			writeJSON(w, d.fail, obj{"message": "failed"})
			return
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return d, srv
}

func pathInt(r *http.Request, name string) int64 {
	n, _ := strconv.ParseInt(r.PathValue(name), 10, 64)
	return n
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func (d *double) snapshot() ([]fieldEdit, []fieldEdit, int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]fieldEdit{}, d.adds...), append([]fieldEdit{}, d.edits...), d.requests
}

func (d *double) hold(id int64, inst discogs.Instance) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.instances[id] = append(d.instances[id], inst)
}

// moveTo puts every instance of release id in folder, as the owner can
// on Discogs.
func (d *double) moveTo(id, folder int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i := range d.instances[id] {
		d.instances[id][i].FolderID = folder
	}
}

// hide makes the test release's lookups answer 404, or answer again.
func (d *double) hide(hidden bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.hidden == nil {
		d.hidden = map[int64]bool{}
	}
	d.hidden[release] = hidden
}

// commitLate makes the instances of the adds answered 503.
func (d *double) commitLate() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, f := range d.late {
		f()
	}
	d.late = nil
}

// removeRelease takes release id out of the collection, as the owner can.
func (d *double) removeRelease(id int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.instances, id)
}

func (d *double) failWith(status int, pathPrefix string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.fail, d.failPath = status, pathPrefix
}

// api is the app with the collection and the Discogs push mounted, and
// what it logged.
type api struct {
	t    *testing.T
	url  string
	pool *pgxpool.Pool
	logs *lockedBuffer
}

// newAPI pushes to folder through the double at base; an empty base
// leaves the push without a token.
func newAPI(t *testing.T, base string, folder int64) *api {
	t.Helper()
	pool := dbtest.Migrated(t)
	logs := &lockedBuffer{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	var client *discogs.Client
	if base != "" {
		client = &discogs.Client{BaseURL: base, Token: token}
	}
	push := discogscollection.NewHandler(
		discogscollection.NewPusher(discogscollection.NewStore(pool), client, folder), logger)
	coll := collection.NewHandler(collection.NewStore(pool), logger)
	srv := httptest.NewServer(server.New(server.Config{AccessLog: true}, logger,
		server.Deps{DB: pool, API: []server.Mounter{coll, push}}))
	t.Cleanup(srv.Close)
	return &api{t: t, url: srv.URL, pool: pool, logs: logs}
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// do sends body as JSON, decodes a successful answer into out when not
// nil and returns its status and raw body.
func (a *api) do(method, path string, body, out any) (int, string) {
	a.t.Helper()
	var r io.Reader = http.NoBody
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			a.t.Fatal(err)
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, a.url+path, r)
	if err != nil {
		a.t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		a.t.Fatal(err)
	}
	if out != nil && resp.StatusCode < 300 {
		if err := json.Unmarshal(raw, out); err != nil {
			a.t.Fatalf("%s %s: decode %s: %v", method, path, raw, err)
		}
	}
	return resp.StatusCode, string(raw)
}

func (a *api) must(method, path string, body, out any, want int) {
	a.t.Helper()
	if code, raw := a.do(method, path, body, out); code != want {
		a.t.Fatalf("%s %s = %d %s, want %d", method, path, code, raw, want)
	}
}

func (a *api) id(path string, body obj) int64 {
	a.t.Helper()
	var out struct {
		ID int64 `json:"id"`
	}
	a.must(http.MethodPost, path, body, &out, http.StatusCreated)
	return out.ID
}

// copyOf makes a title of kind, an edition with edition's fields and a
// copy with copyFields, and returns the copy's ID.
func (a *api) copyOf(kind string, edition, copyFields obj) int64 {
	a.t.Helper()
	edition["title_id"] = a.id("/api/titles", obj{"kind": kind, "name": "Blue Train " + kind})
	copyFields["edition_id"] = a.id("/api/editions", edition)
	return a.id("/api/copies", copyFields)
}

func (a *api) album(copyFields obj) int64 {
	a.t.Helper()
	return a.copyOf("music", obj{"format": "vinyl", "discogs_release_id": release}, copyFields)
}

func (a *api) push(id int64) (int, string, discogscollection.State) {
	a.t.Helper()
	var s discogscollection.State
	code, raw := a.do(http.MethodPost, fmt.Sprintf("/api/copies/%d/discogs", id), nil, &s)
	return code, raw, s
}

// settle dates copy id's link and unfinished add past the settle time, as
// if they were made long ago.
func (a *api) settle(id int64) {
	a.t.Helper()
	for _, q := range []string{
		`UPDATE copy SET discogs_instance_linked_at = now() - interval '11 minutes'
			WHERE id = $1 AND discogs_instance_id IS NOT NULL`,
		`UPDATE discogs_add SET sent_at = now() - interval '11 minutes' WHERE copy_id = $1`,
	} {
		if _, err := a.pool.Exec(context.Background(), q, id); err != nil {
			a.t.Fatal(err)
		}
	}
}

// sibling adds another owned copy of copy id's edition with copyFields.
func (a *api) sibling(id int64, copyFields obj) int64 {
	a.t.Helper()
	var edition int64
	if err := a.pool.QueryRow(context.Background(), `SELECT edition_id FROM copy WHERE id = $1`, id).Scan(&edition); err != nil {
		a.t.Fatal(err)
	}
	copyFields["edition_id"] = edition
	return a.id("/api/copies", copyFields)
}

func (a *api) stored(id int64) (*int64, *int64) {
	a.t.Helper()
	var instance, rel *int64
	if err := a.pool.QueryRow(context.Background(),
		`SELECT discogs_instance_id, discogs_instance_release_id FROM copy WHERE id = $1`, id).
		Scan(&instance, &rel); err != nil {
		a.t.Fatal(err)
	}
	return instance, rel
}

func TestPushAddsGradedAlbumToFolder(t *testing.T) {
	d, srv := newDouble(t)
	a := newAPI(t, srv.URL, 5)
	id := a.album(obj{"status": "owned", "media_grade": "near_mint", "sleeve_grade": "very_good_plus"})

	var before discogscollection.State
	a.must(http.MethodGet, fmt.Sprintf("/api/copies/%d/discogs", id), nil, &before, http.StatusOK)
	if !before.Enabled || !before.Pushable || before.InstanceID != nil || before.ReleaseURL != nil {
		t.Fatalf("state before push = %+v", before)
	}

	code, raw, s := a.push(id)
	if code != http.StatusOK {
		t.Fatalf("push = %d %s", code, raw)
	}
	adds, edits, _ := d.snapshot()
	if len(adds) != 1 || adds[0].folder != 5 || adds[0].release != release {
		t.Fatalf("adds = %+v, want one to folder 5", adds)
	}
	made := adds[0].instance
	if s.InstanceID == nil || *s.InstanceID != made || s.ReleaseURL == nil ||
		*s.ReleaseURL != "https://www.discogs.com/release/1873013" {
		t.Fatalf("state = %+v, want instance %d with its release page", s, made)
	}
	inst, rel := a.stored(id)
	if inst == nil || *inst != made || rel == nil || *rel != release {
		t.Fatalf("stored = %v %v, want %d of %d", inst, rel, made, release)
	}
	want := []fieldEdit{
		{folder: 5, release: release, instance: made, field: mediaID, value: "Near Mint (NM or M-)"},
		{folder: 5, release: release, instance: made, field: sleeveID, value: "Very Good Plus (VG+)"},
	}
	if fmt.Sprint(edits) != fmt.Sprint(want) {
		t.Fatalf("edits = %+v, want %+v", edits, want)
	}

	var after discogscollection.State
	a.must(http.MethodGet, fmt.Sprintf("/api/copies/%d/discogs", id), nil, &after, http.StatusOK)
	if after.InstanceID == nil || *after.InstanceID != made {
		t.Fatalf("state after push = %+v", after)
	}
}

func TestPushWithoutGradesSetsNoField(t *testing.T) {
	d, srv := newDouble(t)
	a := newAPI(t, srv.URL, discogscollection.DefaultFolder)
	bare := a.album(obj{"status": "owned"})
	if code, raw, _ := a.push(bare); code != http.StatusOK {
		t.Fatalf("push = %d %s", code, raw)
	}
	adds, edits, _ := d.snapshot()
	if len(adds) != 1 || adds[0].folder != 1 || len(edits) != 0 {
		t.Fatalf("adds %+v, edits %+v; want one add to Uncategorized and no edit", adds, edits)
	}

	sleeveOnly := a.copyOf("music", obj{"format": "cd", "discogs_release_id": 42}, obj{"status": "owned", "sleeve_grade": "generic"})
	if code, raw, _ := a.push(sleeveOnly); code != http.StatusOK {
		t.Fatalf("push = %d %s", code, raw)
	}
	_, edits, _ = d.snapshot()
	if len(edits) != 1 || edits[0].field != sleeveID || edits[0].value != "Generic" || edits[0].release != 42 {
		t.Fatalf("edits = %+v, want the sleeve only", edits)
	}
}

func TestPushAgainUpdatesItsInstance(t *testing.T) {
	d, srv := newDouble(t)
	a := newAPI(t, srv.URL, discogscollection.DefaultFolder)
	id := a.album(obj{"status": "owned", "media_grade": "mint"})
	if code, raw, _ := a.push(id); code != http.StatusOK {
		t.Fatalf("first push = %d %s", code, raw)
	}
	adds, _, _ := d.snapshot()
	made := adds[0].instance
	d.moveTo(release, 3)

	var c collection.CopyDetail
	a.must(http.MethodGet, fmt.Sprintf("/api/copies/%d", id), nil, &c, http.StatusOK)
	a.must(http.MethodPut, fmt.Sprintf("/api/copies/%d", id), obj{
		"edition_id": c.EditionID, "status": "owned", "media_grade": "good", "sleeve_grade": "poor",
	}, nil, http.StatusOK)
	code, raw, s := a.push(id)
	if code != http.StatusOK || s.InstanceID == nil || *s.InstanceID != made {
		t.Fatalf("second push = %d %s", code, raw)
	}
	adds, edits, _ := d.snapshot()
	if len(adds) != 1 {
		t.Fatalf("adds = %+v, want only the first", adds)
	}
	want := []fieldEdit{
		{folder: 1, release: release, instance: made, field: mediaID, value: "Mint (M)"},
		{folder: 3, release: release, instance: made, field: mediaID, value: "Good (G)"},
		{folder: 3, release: release, instance: made, field: sleeveID, value: "Poor (P)"},
	}
	if fmt.Sprint(edits) != fmt.Sprint(want) {
		t.Fatalf("edits = %+v, want %+v", edits, want)
	}
}

func TestPushRefusesReleaseHeldElsewhere(t *testing.T) {
	d, srv := newDouble(t)
	a := newAPI(t, srv.URL, discogscollection.DefaultFolder)
	d.hold(release, discogs.Instance{InstanceID: 555, FolderID: 1})
	id := a.album(obj{"status": "owned", "media_grade": "mint"})

	code, raw, _ := a.push(id)
	if code != http.StatusConflict || !strings.Contains(raw, "To wydanie jest już w Twojej kolekcji Discogs.") ||
		!strings.Contains(raw, `"field":"discogs_collection"`) {
		t.Fatalf("push = %d %s, want 409 naming the collection", code, raw)
	}
	adds, edits, _ := d.snapshot()
	if len(adds) != 0 || len(edits) != 0 {
		t.Fatalf("adds %+v, edits %+v; want none", adds, edits)
	}
	if inst, _ := a.stored(id); inst != nil {
		t.Fatalf("stored instance %d", *inst)
	}
}

func TestPushReaddsInstanceGoneFromDiscogs(t *testing.T) {
	d, srv := newDouble(t)
	a := newAPI(t, srv.URL, discogscollection.DefaultFolder)
	id := a.album(obj{"status": "owned"})
	if code, raw, _ := a.push(id); code != http.StatusOK {
		t.Fatalf("push = %d %s", code, raw)
	}
	d.removeRelease(release)
	a.settle(id)
	code, raw, s := a.push(id)
	adds, _, _ := d.snapshot()
	if code != http.StatusOK || len(adds) != 2 || s.InstanceID == nil || *s.InstanceID != adds[1].instance {
		t.Fatalf("push after removal = %d %s, adds %+v", code, raw, adds)
	}
}

func TestPushWithoutToken(t *testing.T) {
	a := newAPI(t, "", discogscollection.DefaultFolder)
	id := a.album(obj{"status": "owned"})
	var s discogscollection.State
	a.must(http.MethodGet, fmt.Sprintf("/api/copies/%d/discogs", id), nil, &s, http.StatusOK)
	if s.Enabled || s.Pushable {
		t.Fatalf("state = %+v, want disabled", s)
	}
	if code, raw, _ := a.push(id); code != http.StatusServiceUnavailable {
		t.Fatalf("push = %d %s, want 503", code, raw)
	}
}

func TestPushDiscogsFailures(t *testing.T) {
	for _, tt := range []struct {
		status int
		path   string
		want   string
	}{
		{http.StatusUnauthorized, "GET /oauth/identity", "odrzucił token"},
		{http.StatusForbidden, "POST /users/", "odrzucił token"},
		{http.StatusTooManyRequests, "GET /users/", "ogranicza liczbę zapytań"},
		{http.StatusInternalServerError, "POST /users/", "HTTP 500"},
		{http.StatusBadGateway, "GET /users/", "HTTP 502"},
		{http.StatusServiceUnavailable, "GET /oauth/identity", "HTTP 503"},
	} {
		t.Run(fmt.Sprint(tt.status, " ", tt.path), func(t *testing.T) {
			d, srv := newDouble(t)
			a := newAPI(t, srv.URL, discogscollection.DefaultFolder)
			id := a.album(obj{"status": "owned", "media_grade": "mint"})
			d.failWith(tt.status, tt.path)
			code, raw, _ := a.push(id)
			if code != http.StatusBadGateway || !strings.Contains(raw, tt.want) {
				t.Fatalf("push = %d %s, want 502 with %q", code, raw, tt.want)
			}
			if inst, _ := a.stored(id); inst != nil {
				t.Fatalf("stored instance %d", *inst)
			}
			logs := a.logs.String()
			if !strings.Contains(logs, "discogs collection push") {
				t.Fatalf("no warning logged: %s", logs)
			}
			if strings.Contains(raw, token) || strings.Contains(logs, token) {
				t.Fatalf("token leaked: answer %s, logs %s", raw, logs)
			}
		})
	}
}

// state reads the copy's Discogs state.
func (a *api) state(id int64) discogscollection.State {
	a.t.Helper()
	var s discogscollection.State
	a.must(http.MethodGet, fmt.Sprintf("/api/copies/%d/discogs", id), nil, &s, http.StatusOK)
	return s
}

func TestPushFieldEditFailureStoresNoInstance(t *testing.T) {
	d, srv := newDouble(t)
	a := newAPI(t, srv.URL, discogscollection.DefaultFolder)
	id := a.album(obj{"status": "owned", "media_grade": "mint"})
	d.failWith(http.StatusInternalServerError,
		"POST /users/vinyl%20owner/collection/folders/1/releases/1873013/instances/")
	if code, raw, _ := a.push(id); code != http.StatusBadGateway {
		t.Fatalf("push = %d %s, want 502", code, raw)
	}
	if inst, _ := a.stored(id); inst != nil {
		t.Fatalf("stored instance %d after a failed grade write", *inst)
	}
	if s := a.state(id); s.InstanceID != nil || s.AddUnknown {
		t.Fatalf("state = %+v, want no instance and no add waiting for the owner", s)
	}
	d.failWith(0, "")
	code, raw, s := a.push(id)
	adds, _, _ := d.snapshot()
	if code != http.StatusOK || len(adds) != 1 || s.InstanceID == nil || *s.InstanceID != adds[0].instance {
		t.Fatalf("retry = %d %s, adds %+v; want the made instance finished, none added", code, raw, adds)
	}
}

// R1: Discogs' listing can lag behind its answer; an instance it named
// and the listing omits is not given up, so no second one is made.
func TestPushAnsweredInstanceMissingFromListing(t *testing.T) {
	d, srv := newDouble(t)
	a := newAPI(t, srv.URL, discogscollection.DefaultFolder)
	id := a.album(obj{"status": "owned", "media_grade": "mint"})
	d.failWith(http.StatusInternalServerError,
		"POST /users/vinyl%20owner/collection/folders/1/releases/1873013/instances/")
	if code, raw, _ := a.push(id); code != http.StatusBadGateway {
		t.Fatalf("push = %d %s, want 502", code, raw)
	}
	d.failWith(0, "")
	d.hide(true)
	code, raw, _ := a.push(id)
	if adds, _, _ := d.snapshot(); code != http.StatusConflict || !strings.Contains(raw, "nie rozstrzygnęło") || len(adds) != 1 {
		t.Fatalf("retry while the listing lags = %d %s, adds %+v; want 409 and no second add", code, raw, adds)
	}
	d.hide(false)
	code, raw, s := a.push(id)
	adds, _, _ := d.snapshot()
	if code != http.StatusOK || len(adds) != 1 || s.InstanceID == nil || *s.InstanceID != adds[0].instance {
		t.Fatalf("retry once listed = %d %s, adds %+v; want the answered instance linked", code, raw, adds)
	}
}

func TestPushAnsweredInstanceGoneLongAfter(t *testing.T) {
	d, srv := newDouble(t)
	a := newAPI(t, srv.URL, discogscollection.DefaultFolder)
	id := a.album(obj{"status": "owned", "media_grade": "mint"})
	d.failWith(http.StatusInternalServerError,
		"POST /users/vinyl%20owner/collection/folders/1/releases/1873013/instances/")
	if code, _, _ := a.push(id); code != http.StatusBadGateway {
		t.Fatalf("push = %d, want 502", code)
	}
	d.failWith(0, "")
	d.removeRelease(release)
	if _, err := a.pool.Exec(context.Background(),
		`UPDATE discogs_add SET sent_at = now() - interval '11 minutes' WHERE copy_id = $1`, id); err != nil {
		t.Fatal(err)
	}
	code, raw, s := a.push(id)
	adds, _, _ := d.snapshot()
	if code != http.StatusOK || len(adds) != 2 || s.InstanceID == nil || *s.InstanceID != adds[1].instance {
		t.Fatalf("push = %d %s, adds %+v; want a new instance once the old one is long gone", code, raw, adds)
	}
}

func TestPushFieldsWithoutGradeOption(t *testing.T) {
	d, srv := newDouble(t)
	a := newAPI(t, srv.URL, discogscollection.DefaultFolder)
	d.mu.Lock()
	d.fields = d.fields[:1]
	d.mu.Unlock()
	id := a.album(obj{"status": "owned", "sleeve_grade": "no_cover"})
	code, raw, _ := a.push(id)
	if code != http.StatusBadGateway || !strings.Contains(raw, "Sleeve Condition") {
		t.Fatalf("push = %d %s, want 502 naming the field", code, raw)
	}
	if adds, edits, _ := d.snapshot(); len(adds) != 0 || len(edits) != 0 {
		t.Fatalf("adds %+v, edits %+v; want none", adds, edits)
	}
}

func TestPushRefusesWhatIsNoOwnedAlbum(t *testing.T) {
	d, srv := newDouble(t)
	a := newAPI(t, srv.URL, discogscollection.DefaultFolder)
	for _, tt := range []struct {
		name string
		id   int64
	}{
		{"wishlist album", a.album(obj{"status": "wishlist"})},
		{"sold album", a.album(obj{"status": "sold", "sold_on": "2026-01-02"})},
		{"ordered album", a.album(obj{"status": "ordered"})},
		{"film", a.copyOf("movie", obj{"format": "dvd"}, obj{"status": "owned"})},
		{"book", a.copyOf("book", obj{}, obj{"status": "owned"})},
		{"album without release", a.copyOf("music", obj{"format": "cd"}, obj{"status": "owned"})},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var s discogscollection.State
			a.must(http.MethodGet, fmt.Sprintf("/api/copies/%d/discogs", tt.id), nil, &s, http.StatusOK)
			if !s.Enabled || s.Pushable {
				t.Fatalf("state = %+v, want enabled and not pushable", s)
			}
			if code, raw, _ := a.push(tt.id); code != http.StatusConflict {
				t.Fatalf("push = %d %s, want 409", code, raw)
			}
		})
	}
	if _, _, requests := d.snapshot(); requests != 0 {
		t.Fatalf("Discogs got %d requests, want none", requests)
	}
	if code, _, _ := a.push(999999); code != http.StatusNotFound {
		t.Fatalf("push of a missing copy = %d, want 404", code)
	}
}

// relink moves copy id's edition to release.
func (a *api) relink(id, rel int64) {
	a.t.Helper()
	var c collection.CopyDetail
	a.must(http.MethodGet, fmt.Sprintf("/api/copies/%d", id), nil, &c, http.StatusOK)
	a.must(http.MethodPut, fmt.Sprintf("/api/editions/%d", c.EditionID), obj{
		"title_id": c.Title.ID, "format": "vinyl", "discogs_release_id": rel,
	}, nil, http.StatusOK)
}

func TestPushAfterEditionNamesAnotherRelease(t *testing.T) {
	d, srv := newDouble(t)
	a := newAPI(t, srv.URL, discogscollection.DefaultFolder)
	id := a.album(obj{"status": "owned", "media_grade": "mint"})
	if code, raw, _ := a.push(id); code != http.StatusOK {
		t.Fatalf("push = %d %s", code, raw)
	}
	adds, _, _ := d.snapshot()
	first := adds[0].instance

	a.relink(id, 77)
	if s := a.state(id); s.InstanceID != nil || !s.Pushable {
		t.Fatalf("state = %+v, want no instance of the new release", s)
	}
	code, raw, _ := a.push(id)
	if code != http.StatusConflict || !strings.Contains(raw, "jako inne wydanie") {
		t.Fatalf("push of release 77 = %d %s, want 409 while the first instance is there", code, raw)
	}
	if inst, rel := a.stored(id); inst == nil || *inst != first || rel == nil || *rel != release {
		t.Fatalf("stored %v of %v, want the first link kept", inst, rel)
	}

	a.relink(id, release)
	code, raw, s := a.push(id)
	adds, edits, _ := d.snapshot()
	if code != http.StatusOK || len(adds) != 1 || s.InstanceID == nil || *s.InstanceID != first {
		t.Fatalf("push back on release %d = %d %s, adds %+v", release, code, raw, adds)
	}
	if last := edits[len(edits)-1]; last.instance != first {
		t.Fatalf("last edit = %+v, want the first instance", last)
	}

	a.relink(id, 77)
	d.removeRelease(release)
	a.settle(id)
	code, raw, s = a.push(id)
	adds, _, _ = d.snapshot()
	if code != http.StatusOK || len(adds) != 2 || adds[1].release != 77 || s.InstanceID == nil ||
		*s.InstanceID != adds[1].instance || *s.ReleaseURL != "https://www.discogs.com/release/77" {
		t.Fatalf("push after the first instance is gone = %d %s, adds %+v", code, raw, adds)
	}
}

func TestPushLeavesCutAddToOwner(t *testing.T) {
	d, srv := newDouble(t)
	a := newAPI(t, srv.URL, discogscollection.DefaultFolder)
	id := a.album(obj{"status": "owned", "media_grade": "mint"})
	d.mu.Lock()
	d.cutAdd = 1
	d.mu.Unlock()
	if code, raw, _ := a.push(id); code != http.StatusBadGateway || !strings.Contains(raw, "Nie wiadomo") {
		t.Fatalf("push = %d %s, want 502 saying the outcome is unknown", code, raw)
	}
	if inst, _ := a.stored(id); inst != nil {
		t.Fatalf("stored instance %d", *inst)
	}
	if s := a.state(id); !s.AddUnknown || s.InstanceID != nil {
		t.Fatalf("state = %+v, want the add waiting for the owner", s)
	}
	code, raw, _ := a.push(id)
	if _, edits, _ := d.snapshot(); code != http.StatusConflict || !strings.Contains(raw, "potwierdź") || len(edits) != 0 {
		t.Fatalf("push = %d %s, edits %+v; want 409 asking the owner, nothing edited", code, raw, edits)
	}
	var s discogscollection.State
	a.must(http.MethodPost, fmt.Sprintf("/api/copies/%d/discogs/adopt", id), nil, &s, http.StatusOK)
	adds, edits, _ := d.snapshot()
	if len(adds) != 1 || s.InstanceID == nil || *s.InstanceID != adds[0].instance || s.AddUnknown ||
		len(edits) != 1 || edits[0].instance != adds[0].instance {
		t.Fatalf("adopt = %+v, adds %+v, edits %+v", s, adds, edits)
	}
	if code, raw := a.do(http.MethodPost, fmt.Sprintf("/api/copies/%d/discogs/adopt", id), nil, nil); code != http.StatusConflict {
		t.Fatalf("second adopt = %d %s, want 409", code, raw)
	}
}

func TestPushDiscardedAddLeavesInstanceAlone(t *testing.T) {
	d, srv := newDouble(t)
	a := newAPI(t, srv.URL, discogscollection.DefaultFolder)
	id := a.album(obj{"status": "owned", "media_grade": "mint"})
	d.mu.Lock()
	d.cutAdd = 1
	d.mu.Unlock()
	if code, _, _ := a.push(id); code != http.StatusBadGateway {
		t.Fatalf("push = %d, want 502", code)
	}
	var s discogscollection.State
	a.must(http.MethodDelete, fmt.Sprintf("/api/copies/%d/discogs/add", id), nil, &s, http.StatusOK)
	if s.AddUnknown {
		t.Fatalf("state after discard = %+v", s)
	}
	code, raw, _ := a.push(id)
	adds, edits, _ := d.snapshot()
	if code != http.StatusConflict || !strings.Contains(raw, discogscollection.ForeignInstanceMessage) ||
		len(adds) != 1 || len(edits) != 0 {
		t.Fatalf("push = %d %s, adds %+v, edits %+v; want 409 and the instance left alone", code, raw, adds, edits)
	}
	if code, _ := a.do(http.MethodDelete, fmt.Sprintf("/api/copies/%d/discogs/add", id), nil, nil); code != http.StatusConflict {
		t.Fatalf("second discard = %d, want 409", code)
	}
}

// S1: a refused add leaves nothing pending, so a manual instance added
// before the retry is the owner's and is never edited.
func TestPushRefusedAddThenManualInstance(t *testing.T) {
	d, srv := newDouble(t)
	a := newAPI(t, srv.URL, discogscollection.DefaultFolder)
	id := a.album(obj{"status": "owned", "media_grade": "mint"})
	d.failWith(http.StatusTooManyRequests, "POST /users/")
	if code, _, _ := a.push(id); code != http.StatusBadGateway {
		t.Fatalf("push = %d, want 502", code)
	}
	if s := a.state(id); s.AddUnknown {
		t.Fatalf("state = %+v, want nothing pending after a refusal", s)
	}
	d.failWith(0, "")
	d.hold(release, discogs.Instance{InstanceID: 723, FolderID: 1, DateAdded: time.Now().Format(time.RFC3339)})
	code, raw, _ := a.push(id)
	if _, edits, _ := d.snapshot(); code != http.StatusConflict || len(edits) != 0 {
		t.Fatalf("retry = %d %s, edits %+v; want 409 and the manual instance untouched", code, raw, edits)
	}
}

// S2: an add of unknown outcome blocks every other add of the copy, also
// after its edition names another release.
func TestPushUnknownAddBlocksAddOfAnotherRelease(t *testing.T) {
	d, srv := newDouble(t)
	a := newAPI(t, srv.URL, discogscollection.DefaultFolder)
	id := a.album(obj{"status": "owned"})
	d.mu.Lock()
	d.cutAdd, d.cutList = 1, 1
	d.mu.Unlock()
	if code, _, _ := a.push(id); code != http.StatusBadGateway {
		t.Fatalf("push = %d, want 502", code)
	}
	d.mu.Lock()
	d.cutList = 0
	d.mu.Unlock()
	a.relink(id, 77)
	code, raw, _ := a.push(id)
	if adds, _, _ := d.snapshot(); code != http.StatusConflict || len(adds) != 1 {
		t.Fatalf("push of release 77 = %d %s, adds %+v; want 409 and no second instance", code, raw, adds)
	}
}

// S3: an add answered 5xx may still be made later; an empty collection
// right after does not settle it.
func TestPushLateAddMakesNoDuplicate(t *testing.T) {
	d, srv := newDouble(t)
	a := newAPI(t, srv.URL, discogscollection.DefaultFolder)
	id := a.album(obj{"status": "owned"})
	d.mu.Lock()
	d.lateAdds = 1
	d.mu.Unlock()
	if code, _, _ := a.push(id); code != http.StatusBadGateway {
		t.Fatalf("push = %d, want 502", code)
	}
	code, raw, _ := a.push(id)
	if code != http.StatusConflict || !strings.Contains(raw, "nie rozstrzygnęło") {
		t.Fatalf("retry = %d %s, want 409 while the add is unsettled", code, raw)
	}
	d.commitLate()
	if code, raw, _ := a.push(id); code != http.StatusConflict || !strings.Contains(raw, "potwierdź") {
		t.Fatalf("push after the late add = %d %s, want 409 asking the owner", code, raw)
	}
	if adds, _, _ := d.snapshot(); len(adds) != 1 {
		t.Fatalf("adds = %+v, want one", adds)
	}
}

func TestPushSettlesOldUnknownAddWithEmptyCollection(t *testing.T) {
	d, srv := newDouble(t)
	a := newAPI(t, srv.URL, discogscollection.DefaultFolder)
	id := a.album(obj{"status": "owned"})
	d.mu.Lock()
	d.lateAdds = 1
	d.mu.Unlock()
	if code, _, _ := a.push(id); code != http.StatusBadGateway {
		t.Fatalf("push = %d, want 502", code)
	}
	if _, err := a.pool.Exec(context.Background(),
		`UPDATE discogs_add SET sent_at = now() - interval '11 minutes' WHERE copy_id = $1`, id); err != nil {
		t.Fatal(err)
	}
	code, raw, s := a.push(id)
	adds, _, _ := d.snapshot()
	if code != http.StatusOK || len(adds) != 1 || s.InstanceID == nil || *s.InstanceID != adds[0].instance {
		t.Fatalf("push = %d %s, adds %+v; want one new instance", code, raw, adds)
	}
}

// S4: the add is recorded before it is sent, outside the push's
// transaction, so a lost database connection does not lose it.
func TestPushLostConnectionKeepsAdd(t *testing.T) {
	d, srv := newDouble(t)
	a := newAPI(t, srv.URL, discogscollection.DefaultFolder)
	id := a.album(obj{"status": "owned", "media_grade": "mint"})
	var once sync.Once
	d.mu.Lock()
	d.onAdd = func() {
		once.Do(func() {
			if _, err := a.pool.Exec(context.Background(), `SELECT pg_terminate_backend(pid) FROM pg_locks
				WHERE relation = 'copy'::regclass AND mode = 'RowShareLock' AND pid <> pg_backend_pid()`); err != nil {
				t.Errorf("terminate: %v", err)
			}
		})
	}
	d.mu.Unlock()
	if code, raw, _ := a.push(id); code != http.StatusInternalServerError {
		t.Fatalf("push = %d %s, want 500 after the lost connection", code, raw)
	}
	code, raw, s := a.push(id)
	adds, _, _ := d.snapshot()
	if code != http.StatusOK || len(adds) != 1 || s.InstanceID == nil || *s.InstanceID != adds[0].instance {
		t.Fatalf("retry = %d %s, adds %+v; want the made instance linked", code, raw, adds)
	}
}

func TestPushRefusesForeignBesideOwn(t *testing.T) {
	d, srv := newDouble(t)
	a := newAPI(t, srv.URL, discogscollection.DefaultFolder)
	id := a.album(obj{"status": "owned", "media_grade": "mint"})
	if code, raw, _ := a.push(id); code != http.StatusOK {
		t.Fatalf("push = %d %s", code, raw)
	}
	d.hold(release, discogs.Instance{InstanceID: 812, FolderID: 1})
	_, before, _ := d.snapshot()
	code, raw, _ := a.push(id)
	adds, after, _ := d.snapshot()
	if code != http.StatusConflict || !strings.Contains(raw, discogscollection.ForeignInstanceMessage) ||
		len(adds) != 1 || len(after) != len(before) {
		t.Fatalf("push = %d %s, adds %+v, edits %d then %d; want 409 and no edit", code, raw, adds, len(before), len(after))
	}
}

func TestDeleteWaitsForPush(t *testing.T) {
	d, srv := newDouble(t)
	a := newAPI(t, srv.URL, discogscollection.DefaultFolder)
	id := a.album(obj{"status": "owned"})
	paused, resume := make(chan struct{}), make(chan struct{})
	var once sync.Once
	d.mu.Lock()
	d.beforeLookup = func() {
		once.Do(func() {
			close(paused)
			<-resume
		})
	}
	d.mu.Unlock()
	pushed := make(chan int, 1)
	go func() {
		code, _, _ := a.push(id)
		pushed <- code
	}()
	<-paused
	deleted := make(chan int, 1)
	go func() {
		code, _ := a.do(http.MethodDelete, fmt.Sprintf("/api/copies/%d", id), nil, nil)
		deleted <- code
	}()
	select {
	case code := <-deleted:
		t.Fatalf("delete answered %d while the push held the copy", code)
	case <-time.After(300 * time.Millisecond):
	}
	close(resume)
	if code := <-pushed; code != http.StatusOK {
		t.Fatalf("push = %d", code)
	}
	if code := <-deleted; code != http.StatusNoContent {
		t.Fatalf("delete = %d", code)
	}
}

// Review F1: a linked instance the lagging listing omits right after the
// push that made it is not given up, so no second one is made.
func TestPushLinkedInstanceDuringListingLag(t *testing.T) {
	d, srv := newDouble(t)
	a := newAPI(t, srv.URL, discogscollection.DefaultFolder)
	id := a.album(obj{"status": "owned", "media_grade": "mint"})
	d.hide(true)
	if code, raw, _ := a.push(id); code != http.StatusOK {
		t.Fatalf("push = %d %s", code, raw)
	}
	code, raw, _ := a.push(id)
	if adds, _, _ := d.snapshot(); code != http.StatusConflict || !strings.Contains(raw, "nie rozstrzygnęło") || len(adds) != 1 {
		t.Fatalf("update while the listing lags = %d %s, adds %+v; want 409 and no second add", code, raw, adds)
	}
	d.hide(false)
	code, raw, s := a.push(id)
	adds, _, _ := d.snapshot()
	if code != http.StatusOK || len(adds) != 1 || s.InstanceID == nil || *s.InstanceID != adds[0].instance {
		t.Fatalf("update once listed = %d %s, adds %+v", code, raw, adds)
	}
}

// Review F2: an add of unknown outcome cannot be discarded while it is
// recent and the listing does not show the release yet.
func TestDiscardRefusedDuringListingLag(t *testing.T) {
	d, srv := newDouble(t)
	a := newAPI(t, srv.URL, discogscollection.DefaultFolder)
	id := a.album(obj{"status": "owned", "media_grade": "mint"})
	d.mu.Lock()
	d.cutAdd = 1
	d.mu.Unlock()
	d.hide(true)
	if code, _, _ := a.push(id); code != http.StatusBadGateway {
		t.Fatalf("push = %d, want 502", code)
	}
	path := fmt.Sprintf("/api/copies/%d/discogs/add", id)
	if code, raw := a.do(http.MethodDelete, path, nil, nil); code != http.StatusConflict || !strings.Contains(raw, "nie rozstrzygnęło") {
		t.Fatalf("discard while the listing lags = %d %s, want 409", code, raw)
	}
	if code, _, _ := a.push(id); code != http.StatusConflict {
		t.Fatalf("push = %d, want 409", code)
	}
	if adds, _, _ := d.snapshot(); len(adds) != 1 {
		t.Fatalf("adds = %+v, want one", adds)
	}
	d.hide(false)
	a.must(http.MethodDelete, path, nil, nil, http.StatusOK)
}

// Review F3: an adoption never takes an instance another copy is linked
// to, and two copies never share one.
func TestAdoptSkipsOtherCopysInstance(t *testing.T) {
	d, srv := newDouble(t)
	a := newAPI(t, srv.URL, discogscollection.DefaultFolder)
	copyB := a.album(obj{"status": "owned", "media_grade": "mint"})
	copyA := a.sibling(copyB, obj{"status": "owned", "media_grade": "poor"})
	if code, raw, _ := a.push(copyB); code != http.StatusOK {
		t.Fatalf("push B = %d %s", code, raw)
	}
	if _, err := a.pool.Exec(context.Background(),
		`INSERT INTO discogs_add (copy_id, release_id) VALUES ($1, $2)`, copyA, release); err != nil {
		t.Fatal(err)
	}
	_, before, _ := d.snapshot()
	code, raw := a.do(http.MethodPost, fmt.Sprintf("/api/copies/%d/discogs/adopt", copyA), nil, nil)
	if _, after, _ := d.snapshot(); code != http.StatusConflict || len(after) != len(before) {
		t.Fatalf("adopt A = %d %s, edits %d then %d; want 409 and B's instance untouched", code, raw, len(before), len(after))
	}
	if ia, _ := a.stored(copyA); ia != nil {
		t.Fatalf("copy A linked to %d", *ia)
	}
	ib, _ := a.stored(copyB)
	if ib == nil {
		t.Fatal("copy B not linked")
	}
	if _, err := a.pool.Exec(context.Background(), `UPDATE copy SET discogs_instance_id = $2,
		discogs_instance_release_id = $3, discogs_instance_linked_at = now() WHERE id = $1`, copyA, *ib, release); err == nil {
		t.Fatal("two copies linked to one instance")
	}
}

// Review F4: a push of a release waits while another copy's add of it has
// an unknown outcome, so that add's instance stays adoptable.
func TestSiblingUnknownAddBlocksPush(t *testing.T) {
	d, srv := newDouble(t)
	a := newAPI(t, srv.URL, discogscollection.DefaultFolder)
	copyA := a.album(obj{"status": "owned"})
	copyB := a.sibling(copyA, obj{"status": "owned"})
	d.mu.Lock()
	d.cutAdd = 1
	d.mu.Unlock()
	d.hide(true)
	if code, _, _ := a.push(copyA); code != http.StatusBadGateway {
		t.Fatalf("push A = %d, want 502", code)
	}
	code, raw, _ := a.push(copyB)
	if adds, _, _ := d.snapshot(); code != http.StatusConflict || !strings.Contains(raw, "Inny egzemplarz") || len(adds) != 1 {
		t.Fatalf("push B = %d %s, adds %+v; want 409 and no add", code, raw, adds)
	}
	d.hide(false)
	var s discogscollection.State
	a.must(http.MethodPost, fmt.Sprintf("/api/copies/%d/discogs/adopt", copyA), nil, &s, http.StatusOK)
	if adds, _, _ := d.snapshot(); s.InstanceID == nil || *s.InstanceID != adds[0].instance {
		t.Fatalf("adopt A = %+v, adds %+v", s, adds)
	}
}
