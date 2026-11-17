package movie

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// Hints is what a shop's product name tells about a movie edition, for
// example "Incepcja (Inception) [Blu-Ray 4K] Lektor PL". Title is the name
// without format, language and packaging noise; AltTitle is a parenthesized
// alternative, usually the original title. Format is "dvd", "bluray" or
// "uhd", nil when the name does not say.
type Hints struct {
	Title       string
	AltTitle    string
	Year        *int
	Format      *string
	PLSubtitles string
	PLVoiceover string
	Packaging   []string
}

// Everything from the first bracket or format/language keyword on is
// metadata, not title; shop names may also lead with boilerplate such as
// "Film Blu-ray Incepcja".
var (
	marker  = regexp.MustCompile(`(?i)[\[(]|(?:^|[^\p{L}\p{N}])(?:(?:\d+\s?x\s?|\d+)?dvd|(?:\d+\s?x\s?|\d+)?blu[- ]?ray|bd|4k|uhd|ultra\s?hd|lektor|napisy|dubbing|steelbook|digipa[ck]k?|mediabook|p[łl]yta|wydanie|edycja|pl|\+)(?:[^\p{L}\p{N}]|$)`)
	prefix  = regexp.MustCompile(`(?i)^(?:\s*(?:\[[^\]]*\]|film(?:y)?|dvd|blu[- ]?ray|4k|uhd|p[łl]yta)(?:\s+|$|[:\-–]\s*))+`)
	parens  = regexp.MustCompile(`[\[(]([^\])]*)[\])]`)
	yearRe  = regexp.MustCompile(`^\s*((?:19|20)\d{2})\s*$`)
	uhdRe   = regexp.MustCompile(`(?i)(?:^|[^\p{L}\p{N}])(?:4k|uhd|ultra\s?hd)(?:[^\p{L}\p{N}]|$)`)
	bluRe   = regexp.MustCompile(`(?i)(?:^|[^\p{L}])(?:\d*x?\s?blu[- ]?ray|bd)(?:[^\p{L}\p{N}]|$)`)
	dvdRe   = regexp.MustCompile(`(?i)(?:^|[^\p{L}])\d*x?\s?dvd(?:[^\p{L}\p{N}]|$)`)
	voiceRe = regexp.MustCompile(`(?i)(?:^|[^\p{L}])(?:lektor|dubbing)(?:[^\p{L}]|$)`)
	subsRe  = regexp.MustCompile(`(?i)(?:^|[^\p{L}])napisy(?:[^\p{L}]|$)`)
	packRes = []struct {
		re   *regexp.Regexp
		name string
	}{
		{regexp.MustCompile(`(?i)steelbook`), "steelbook"},
		{regexp.MustCompile(`(?i)slip\s?cover|o-ring`), "slipcover"},
		{regexp.MustCompile(`(?i)digipa[ck]k?`), "digipak"},
		{regexp.MustCompile(`(?i)mediabook`), "mediabook"},
	}
)

// ParseName extracts edition hints and a searchable title from a product
// name.
func ParseName(name string) Hints {
	name = strings.Join(strings.Fields(name), " ")
	h := Hints{PLSubtitles: "unknown", PLVoiceover: "unknown"}

	var format string
	switch {
	case uhdRe.MatchString(name):
		format = "uhd"
	case bluRe.MatchString(name):
		format = "bluray"
	case dvdRe.MatchString(name):
		format = "dvd"
	}
	if format != "" {
		h.Format = &format
	}
	if voiceRe.MatchString(name) {
		h.PLVoiceover = "yes"
	}
	if subsRe.MatchString(name) {
		h.PLSubtitles = "yes"
	}
	for _, p := range packRes {
		if p.re.MatchString(name) {
			h.Packaging = append(h.Packaging, p.name)
		}
	}

	for _, m := range parens.FindAllStringSubmatch(name, -1) {
		inner := strings.TrimSpace(m[1])
		if y := yearRe.FindStringSubmatch(inner); y != nil {
			if v, err := strconv.Atoi(y[1]); err == nil && h.Year == nil {
				h.Year = &v
			}
			continue
		}
		if h.AltTitle == "" && hasLetter(inner) && !marker.MatchString(inner) {
			h.AltTitle = inner
		}
	}

	body := prefix.ReplaceAllString(name, "")
	if loc := marker.FindStringIndex(body); loc != nil {
		body = body[:loc[0]]
	}
	h.Title = strings.TrimFunc(body, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune("-–:,/|.;", r)
	})
	if h.Title == "" {
		h.Title, h.AltTitle = h.AltTitle, ""
	}
	return h
}

func hasLetter(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

var fold = strings.NewReplacer(
	"ą", "a", "ć", "c", "ę", "e", "ł", "l", "ń", "n", "ó", "o", "ś", "s", "ź", "z", "ż", "z",
	"á", "a", "à", "a", "ä", "a", "é", "e", "è", "e", "ë", "e", "í", "i", "ï", "i",
	"ö", "o", "ô", "o", "ü", "u", "ú", "u", "ñ", "n", "ç", "c", "&", " and ",
)

// normalize lowercases s, folds Polish and common Latin diacritics and keeps
// only letters and digits separated by single spaces.
func normalize(s string) string {
	s = fold.Replace(strings.ToLower(s))
	var b strings.Builder
	space := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
			continue
		}
		space = true
	}
	return b.String()
}

// digitPenalty scales titles whose numbers differ: "Listy do M. 2" is
// another film than "Listy do M. 4", however close the spelling.
const digitPenalty = 0.7

// similarity scores two titles in [0, 1]: 1 for equal normalized titles,
// otherwise the edit-distance ratio, lowered when their numbers differ.
func similarity(a, b string) float64 {
	a, b = normalize(a), normalize(b)
	if a == "" || b == "" {
		return 0
	}
	if a == b {
		return 1
	}
	ra, rb := []rune(a), []rune(b)
	longest := max(len(ra), len(rb))
	sim := 1 - float64(levenshtein(ra, rb))/float64(longest)
	if !slices.Equal(numbers(a), numbers(b)) {
		sim *= digitPenalty
	}
	return sim
}

func numbers(s string) []string {
	var out []string
	for f := range strings.FieldsSeq(s) {
		if strings.IndexFunc(f, unicode.IsDigit) >= 0 {
			out = append(out, f)
		}
	}
	return out
}

var subtitleSep = regexp.MustCompile(`[:.]\s|\s[-–]\s`)

// mainTitle is the title before a subtitle separator ("Władca Pierścieni"
// for "Władca Pierścieni: Drużyna Pierścienia"), or "" without one.
func mainTitle(title string) string {
	loc := subtitleSep.FindStringIndex(title)
	if loc == nil {
		return ""
	}
	main := strings.TrimSpace(title[:loc[0]])
	if !hasLetter(main) {
		return ""
	}
	return main
}

func levenshtein(a, b []rune) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}
