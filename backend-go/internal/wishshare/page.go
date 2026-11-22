package wishshare

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"html/template"
	"strconv"
	"strings"
)

// pageStyle is the page's only style sheet; the CSP admits it by its hash.
const pageStyle = `
:root { color-scheme: light dark; --bg: #f7f5f2; --card: #fff; --fg: #1c1b1a; --muted: #5f5b57; --line: #e3dfda; }
@media (prefers-color-scheme: dark) { :root { --bg: #161514; --card: #201f1d; --fg: #f1eeea; --muted: #b3ada6; --line: #36332f; } }
* { box-sizing: border-box; }
body { margin: 0; background: var(--bg); color: var(--fg); font: 1rem/1.45 system-ui, -apple-system, "Segoe UI", Roboto, sans-serif; }
main { max-width: 48rem; margin: 0 auto; padding: max(env(safe-area-inset-top), 1.5rem) 1rem 2rem; }
h1 { margin: 0 0 1rem; font-size: 1.75rem; }
ul { display: grid; gap: 0.75rem; margin: 0; padding: 0; list-style: none; }
li { display: flex; gap: 0.75rem; padding: 0.75rem; border: 1px solid var(--line); border-radius: 0.75rem; background: var(--card); }
.cover { flex: none; width: 4rem; height: 5.75rem; border-radius: 0.375rem; background: var(--line); object-fit: cover; }
h2 { margin: 0; font-size: 1.05rem; overflow-wrap: anywhere; }
p { margin: 0.25rem 0 0; color: var(--muted); font-size: 0.9rem; overflow-wrap: anywhere; }
.empty { margin: 0; color: var(--fg); }
`

// pageCSP lets the page load only its own style and outside cover images;
// it runs no script, submits no form and is framed by nobody.
var pageCSP = "default-src 'none'; img-src https: http: data:; style-src 'sha256-" + styleHash() +
	"'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

func styleHash() string {
	sum := sha256.Sum256([]byte(pageStyle))
	return base64.StdEncoding.EncodeToString(sum[:])
}

var pageTemplate = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="pl">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
<meta name="robots" content="noindex, nofollow">
<meta name="referrer" content="no-referrer">
<link rel="icon" href="data:,">
<title>Lista życzeń</title>
<style>` + pageStyle + `</style>
</head>
<body>
<main>
<h1>Lista życzeń</h1>
{{if .}}<ul aria-label="Życzenia">
{{range .}}<li>
{{if .CoverURL}}<img class="cover" src="{{.CoverURL}}" alt="" loading="lazy" referrerpolicy="no-referrer">{{else}}<span class="cover" aria-hidden="true"></span>{{end}}
<div>
<h2>{{.Title}}</h2>
{{if .Creators}}<p>{{.Creators}}</p>{{end}}
<p>{{.Details}}</p>
</div>
</li>
{{end}}</ul>
{{else}}<p class="empty">Lista życzeń jest pusta.</p>
{{end}}</main>
</body>
</html>
`))

var kindLabels = map[string]string{"movie": "Film", "book": "Książka", "music": "Muzyka", "tv": "Serial"}

var formatLabels = map[string]string{
	"dvd": "DVD", "bluray": "Blu-ray", "uhd": "4K UHD",
	"cd": "CD", "vinyl": "Winyl", "cassette": "Kaseta",
	"hardcover": "Twarda oprawa", "paperback": "Miękka oprawa", "other": "Inna oprawa",
}

// pageWish is a wish as the page's text: creators joined and the kind,
// year and format or binding on one line.
type pageWish struct {
	Title    string
	Creators string
	Details  string
	CoverURL string
}

func label(labels map[string]string, v string) string {
	if l, ok := labels[v]; ok {
		return l
	}
	return v
}

func pageWishes(wishes []Wish) []pageWish {
	out := make([]pageWish, 0, len(wishes))
	for _, w := range wishes {
		details := []string{label(kindLabels, w.Kind)}
		if w.Year != nil {
			details = append(details, strconv.Itoa(*w.Year))
		}
		for _, v := range []*string{w.Format, w.Binding} {
			if v != nil {
				details = append(details, label(formatLabels, *v))
			}
		}
		pw := pageWish{Title: w.Title, Creators: strings.Join(w.Creators, ", "), Details: strings.Join(details, " · ")}
		if w.CoverURL != nil {
			pw.CoverURL = *w.CoverURL
		}
		out = append(out, pw)
	}
	return out
}

// renderPage renders the whole page before anything is sent, so a failed
// render still answers a clean 500.
func renderPage(wishes []Wish) (*bytes.Buffer, error) {
	var b bytes.Buffer
	if err := pageTemplate.Execute(&b, pageWishes(wishes)); err != nil {
		return nil, err
	}
	return &b, nil
}
