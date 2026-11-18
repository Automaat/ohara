package books_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Automaat/ohara/backend-go/internal/lookup/books"
)

func TestTitleSearch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch r.URL.Path {
		case "/books/v1/volumes":
			if q.Get("q") != "Solaris Lem" || q.Get("key") != "k" || q.Get("printType") != "books" {
				t.Errorf("google query %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"items":[
				{"id":"a","volumeInfo":{"title":"Solaris","authors":["Stanisław Lem"],"publisher":"Wydawnictwo Literackie","publishedDate":"2012","imageLinks":{"thumbnail":"http://books.google.com/s.jpg"}}},
				{"id":"b","volumeInfo":{"title":" "}}]}`))
		case "/search.json":
			if q.Get("q") != "Solaris Lem" {
				t.Errorf("openlibrary query %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"docs":[{"key":"/works/OL116522W","title":"Solaris","author_name":["Stanisław Lem"],"first_publish_year":1961,"cover_i":240726}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	g, err := books.GoogleBooks{APIKey: "k", BaseURL: srv.URL, Client: srv.Client()}.SearchText(t.Context(), "Solaris Lem", "")
	if err != nil || len(g) != 1 {
		t.Fatalf("google: %+v, %v", g, err)
	}
	if c := g[0]; c.Title.Name != "Solaris" || c.Title.Authors[0] != "Stanisław Lem" || c.Edition.Publisher != "Wydawnictwo Literackie" ||
		*c.Edition.Year != 2012 || c.CoverURL != "https://books.google.com/s.jpg" || c.Confidence >= 0.8 {
		t.Fatalf("google candidate = %+v", c)
	}
	if _, err := (books.GoogleBooks{BaseURL: srv.URL}).SearchText(t.Context(), "x", ""); err == nil {
		t.Fatal("google search without a key must fail")
	}

	o, err := books.OpenLibrary{BaseURL: srv.URL, Client: srv.Client()}.SearchText(t.Context(), "Solaris Lem", "")
	if err != nil || len(o) != 1 {
		t.Fatalf("openlibrary: %+v, %v", o, err)
	}
	if c := o[0]; c.Title.Name != "Solaris" || *c.Title.OpenLibraryID != "OL116522W" || *c.Title.Year != 1961 ||
		c.CoverURL == "" || c.Confidence >= 0.8 {
		t.Fatalf("openlibrary candidate = %+v", c)
	}
}
