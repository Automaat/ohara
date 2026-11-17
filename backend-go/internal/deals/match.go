package deals

import (
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Want is a wishlist copy the hunter searches for, with its edition and
// title. MaxPriceCents is nil when the owner set no limit: its listings are
// recorded but never alerted.
type Want struct {
	CopyID        int64
	EditionID     int64
	TitleID       int64
	Kind          string
	Name          string
	OriginalTitle *string
	PolishTitle   *string
	Authors       []string
	Format        *string
	MaxPriceCents *int64
}

// Rules are the editable reject and accept phrases.
type Rules struct {
	Reject []string
	Accept []string
}

var polishFold = strings.NewReplacer(
	"ą", "a", "ć", "c", "ę", "e", "ł", "l", "ń", "n", "ó", "o", "ś", "s", "ź", "z", "ż", "z",
)

// words lowercases s, folds Polish diacritics and splits it into runs of
// letters and digits.
func words(s string) []string {
	folded := polishFold.Replace(strings.ToLower(s))
	return strings.FieldsFunc(folded, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// findPhrase returns where phrase starts in text, or -1. Each phrase word
// must start the text word at its position, so "uszkodzon" finds
// "uszkodzona".
func findPhrase(text, phrase []string) int {
	if len(phrase) == 0 {
		return -1
	}
	for i := 0; i+len(phrase) <= len(text); i++ {
		match := true
		for j, w := range phrase {
			if !strings.HasPrefix(text[i+j], w) {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// Rejected reports whether title contains a reject phrase once every accept
// phrase is cut from it.
func (r Rules) Rejected(title string) bool {
	text := words(title)
	for _, a := range r.Accept {
		phrase := words(a)
		for {
			i := findPhrase(text, phrase)
			if i < 0 {
				break
			}
			text = slices.Delete(slices.Clone(text), i, i+len(phrase))
		}
	}
	for _, reject := range r.Reject {
		if findPhrase(text, words(reject)) >= 0 {
			return true
		}
	}
	return false
}

// stopWords are left out of the words a listing must contain: sellers drop
// them, and "Część V" is often just "V" or nothing.
var stopWords = map[string]bool{
	"the": true, "of": true, "and": true, "a": true, "an": true,
	"i": true, "w": true, "z": true, "na": true, "czesc": true, "part": true,
}

// required is the set of words a listing must contain to match name: its
// words without stop words and single letters. Numbers stay, even single
// digits, so "Rocky 2" does not match "Rocky 3".
func required(name string) []string {
	var out []string
	for _, w := range words(name) {
		if (len([]rune(w)) > 1 || unicode.IsDigit([]rune(w)[0])) && !stopWords[w] {
			out = append(out, w)
		}
	}
	return out
}

// names are the titles a want is sold under: Polish, original and the name
// it was entered with, without duplicates.
func (w *Want) names() []string {
	var out []string
	seen := map[string]bool{}
	for _, n := range []*string{w.PolishTitle, w.OriginalTitle, &w.Name} {
		if n == nil {
			continue
		}
		key := strings.Join(words(*n), " ")
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, *n)
	}
	return out
}

// surname is the last word of the first author, which book listings carry
// far more often than first names.
func (w *Want) surname() string {
	if len(w.Authors) == 0 {
		return ""
	}
	parts := words(w.Authors[0])
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

// formatKeyword is the format word added to a movie search query.
var formatKeyword = map[string]string{"dvd": "dvd", "bluray": "blu-ray", "uhd": "4k"}

// Queries are the search phrases for w: each name with the format keyword
// for a movie, or with the author's surname for a book.
func (w *Want) Queries() []string {
	suffix := ""
	switch {
	case w.Kind == "movie" && w.Format != nil:
		suffix = formatKeyword[*w.Format]
	case w.Kind == "book":
		suffix = w.surname()
	}
	var out []string
	for _, n := range w.names() {
		q := strings.TrimSpace(n)
		if suffix != "" && !slices.Contains(words(q), suffix) {
			q += " " + suffix
		}
		out = append(out, q)
	}
	return out
}

func containsAll(text, want []string) bool {
	for _, w := range want {
		if !slices.Contains(text, w) {
			return false
		}
	}
	return true
}

// Formats a listing title names. A 4K release ships with a Blu-ray disc, so
// "4K + Blu-ray" is uhd only, and a Blu-ray title that also names DVD is a
// Blu-ray.
func listingFormat(text []string) string {
	joined := " " + strings.Join(text, " ") + " "
	has := func(subs ...string) bool {
		for _, s := range subs {
			if strings.Contains(joined, s) {
				return true
			}
		}
		return false
	}
	switch {
	case has(" 4k ", "uhd", " ultra hd "):
		return "uhd"
	case has("bluray", " blu ray ", " bd "):
		return "bluray"
	case has("dvd"):
		return "dvd"
	}
	return ""
}

// romanParts are the sequel numerals a movie title can carry.
var romanParts = map[string]bool{
	"ii": true, "iii": true, "iv": true, "v": true, "vi": true, "vii": true, "viii": true, "ix": true, "x": true,
}

// sequelAfter reports whether text names a sequel of name: right after
// name's last word comes a number or numeral that name lacks and that is no
// release year, as in "Blade Runner 2049" or "Rocky II" for "Rocky".
func sequelAfter(text []string, name string) bool {
	nameWords := words(name)
	if len(nameWords) == 0 {
		return false
	}
	last := nameWords[len(nameWords)-1]
	for i, t := range text[:max(len(text)-1, 0)] {
		next := text[i+1]
		if t != last || slices.Contains(nameWords, next) {
			continue
		}
		if romanParts[next] {
			return true
		}
		if n, err := strconv.Atoi(next); err == nil && (n < 1900 || n > time.Now().Year()+1) {
			return true
		}
	}
	return false
}

// Matches reports whether a listing title is w: it contains every required
// word of one of w's names, the edition's format for a movie, and the
// author's surname for a book. A movie listing naming a sequel of that name
// does not match.
func (w *Want) Matches(title string) bool {
	text := words(title)
	if w.Kind == "movie" && w.Format != nil && listingFormat(text) != *w.Format {
		return false
	}
	if s := w.surname(); w.Kind == "book" && s != "" && !slices.Contains(text, s) {
		return false
	}
	for _, n := range w.names() {
		if req := required(n); len(req) > 0 && containsAll(text, req) &&
			(w.Kind != "movie" || !sequelAfter(text, n)) {
			return true
		}
	}
	return false
}

// pick returns the want a listing belongs to, or nil. Among matching wants
// it prefers one whose maximum price the listing meets, so a cheap listing
// alerts even when an earlier want for the same title has a lower limit.
func pick(wants []Want, l *Listing, rules Rules) *Want {
	if rules.Rejected(l.Title) {
		return nil
	}
	var first *Want
	for i := range wants {
		w := &wants[i]
		if !w.Matches(l.Title) {
			continue
		}
		if w.MaxPriceCents != nil && l.PriceCents <= *w.MaxPriceCents {
			return w
		}
		if first == nil {
			first = w
		}
	}
	return first
}
