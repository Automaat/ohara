package movie

import (
	"net/http"
	"testing"
)

func TestTextSearch(t *testing.T) {
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("query") != "Blade Runner" || r.URL.Query().Get("year") != "" {
			t.Errorf("query %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"results":[
			{"id":78,"title":"Łowca androidów","original_title":"Blade Runner","release_date":"1982-06-25","poster_path":"/br.jpg"},
			{"id":335984,"title":"Blade Runner 2049","original_title":"Blade Runner 2049","release_date":"2017-10-04"}]}`))
	})
	tmdb := TMDB{Client: s.Client(), BaseURL: s.URL, Key: "k"}
	for _, tc := range []struct{ format, want string }{{"bluray", "bluray"}, {"cd", ""}, {"", ""}} {
		got, err := TextSearch{TMDB: tmdb}.SearchText(t.Context(), "Blade Runner", tc.format)
		if err != nil || len(got) != 2 {
			t.Fatalf("format %q: %+v, %v", tc.format, got, err)
		}
		c := got[0]
		if c.Type != "movie" || c.Title.Name != "Łowca androidów" || *c.Title.OriginalTitle != "Blade Runner" ||
			*c.Title.TMDBID != 78 || *c.Title.Year != 1982 || c.CoverURL != "https://image.tmdb.org/t/p/w342/br.jpg" ||
			got[1].CoverURL != "" || c.Confidence >= 0.8 {
			t.Fatalf("candidate = %+v", c)
		}
		format := ""
		if c.Edition.Format != nil {
			format = *c.Edition.Format
		}
		if format != tc.want {
			t.Fatalf("format %q kept as %q", tc.format, format)
		}
	}
}
