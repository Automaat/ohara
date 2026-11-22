package collection_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/Automaat/ohara/backend-go/internal/collection"
)

func TestCopyMarks(t *testing.T) {
	a := newAPI(t)
	book := a.title("book", "Solaris")
	limited := a.edition(obj{"title_id": book, "limited_to": 500})
	open := a.edition(obj{"title_id": book})

	for _, tt := range []struct {
		name  string
		body  obj
		field string
	}{
		{"zero", obj{"edition_id": open, "status": "owned", "copy_number": 0}, "copy_number"},
		{"negative", obj{"edition_id": open, "status": "owned", "copy_number": -3}, "copy_number"},
		{"past any run", obj{"edition_id": open, "status": "owned", "copy_number": 1_000_001}, "copy_number"},
		{"fraction", obj{"edition_id": open, "status": "owned", "copy_number": 1.5}, "copy_number"},
		{"text number", obj{"edition_id": open, "status": "owned", "copy_number": "12"}, "copy_number"},
		{"text signed", obj{"edition_id": open, "status": "owned", "signed": "yes"}, "signed"},
		{"above the run", obj{"edition_id": limited, "status": "owned", "copy_number": 501}, "copy_number"},
		{"signed wish", obj{"edition_id": open, "status": "wishlist", "signed": true}, "signed"},
		{"numbered wish", obj{"edition_id": open, "status": "wishlist", "copy_number": 3}, "copy_number"},
		{"missing edition", obj{"edition_id": 999999, "status": "owned", "copy_number": 3}, "edition_id"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sub := &api{t: t, url: a.url}
			sub.fails(http.MethodPost, "/api/copies", tt.body, http.StatusUnprocessableEntity, tt.field)
		})
	}

	var plain collection.Copy
	a.mustDo(http.MethodPost, "/api/copies", obj{"edition_id": open, "status": "owned"}, &plain, http.StatusCreated)
	if plain.Signed || plain.CopyNumber != nil {
		t.Fatalf("plain copy marks = %v, %v", plain.Signed, plain.CopyNumber)
	}

	var c collection.Copy
	a.mustDo(http.MethodPost, "/api/copies", obj{"edition_id": limited, "status": "owned", "signed": true, "copy_number": 500},
		&c, http.StatusCreated)
	a.mustDo(http.MethodPut, fmt.Sprintf("/api/copies/%d", c.ID),
		obj{"edition_id": limited, "status": "owned", "signed": true, "copy_number": 123}, &c, http.StatusOK)
	wantMarks := func(where string, signed bool, number *int) {
		t.Helper()
		if !signed || number == nil || *number != 123 {
			t.Fatalf("%s marks = %v, %v", where, signed, number)
		}
	}
	wantMarks("PUT answer", c.Signed, c.CopyNumber)

	var detail collection.CopyDetail
	a.mustDo(http.MethodGet, fmt.Sprintf("/api/copies/%d", c.ID), nil, &detail, http.StatusOK)
	wantMarks("GET /api/copies/{id}", detail.Signed, detail.CopyNumber)

	var title collection.TitleDetail
	a.mustDo(http.MethodGet, fmt.Sprintf("/api/titles/%d", book), nil, &title, http.StatusOK)
	var found bool
	for _, e := range title.Editions {
		for _, got := range e.Copies {
			if got.ID == c.ID {
				found = true
				wantMarks("GET /api/titles/{id}", got.Signed, got.CopyNumber)
			}
		}
	}
	if !found {
		t.Fatalf("title editions lack copy %d: %+v", c.ID, title.Editions)
	}

	var view collection.CollectionView
	a.mustDo(http.MethodGet, "/api/collection?kind=book", nil, &view, http.StatusOK)
	found = false
	for _, it := range view.Items {
		switch it.CopyID {
		case c.ID:
			found = true
			wantMarks("GET /api/collection", it.Signed, it.CopyNumber)
		case plain.ID:
			if it.Signed || it.CopyNumber != nil {
				t.Fatalf("plain item marks = %v, %v", it.Signed, it.CopyNumber)
			}
		}
	}
	if !found {
		t.Fatalf("collection lacks copy %d: %+v", c.ID, view.Items)
	}

	a.fails(http.MethodPost, "/api/copies", obj{"edition_id": limited, "status": "owned", "copy_number": 123},
		http.StatusConflict, "copy_number")
	a.fails(http.MethodPut, fmt.Sprintf("/api/copies/%d", plain.ID),
		obj{"edition_id": limited, "status": "owned", "copy_number": 123}, http.StatusConflict, "copy_number")
	a.copyOf(obj{"edition_id": open, "status": "sold", "sold_on": "2026-01-02", "copy_number": 123, "signed": true})
	a.copyOf(obj{"edition_id": open, "status": "ordered", "copy_number": 1_000_000})

	small := a.edition(obj{"title_id": book, "limited_to": 100})
	a.fails(http.MethodPut, fmt.Sprintf("/api/copies/%d", c.ID),
		obj{"edition_id": small, "status": "owned", "copy_number": 123}, http.StatusUnprocessableEntity, "copy_number")

	a.fails(http.MethodPut, fmt.Sprintf("/api/editions/%d", limited), obj{"title_id": book, "limited_to": 122},
		http.StatusUnprocessableEntity, "limited_to")
	a.mustDo(http.MethodPut, fmt.Sprintf("/api/editions/%d", limited), obj{"title_id": book, "limited_to": 123},
		nil, http.StatusOK)

	var cleared collection.Copy
	a.mustDo(http.MethodPut, fmt.Sprintf("/api/copies/%d", c.ID), obj{"edition_id": limited, "status": "owned"},
		&cleared, http.StatusOK)
	if cleared.Signed || cleared.CopyNumber != nil {
		t.Fatalf("cleared marks = %v, %v", cleared.Signed, cleared.CopyNumber)
	}
	a.copyOf(obj{"edition_id": limited, "status": "owned", "copy_number": 123})
}
