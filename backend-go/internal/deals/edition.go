package deals

import (
	"slices"
	"strings"
)

// Edition details a listing title can name, as price_observation's
// edition_details stores them: the packaging values, the two Polish
// language tracks and the two bindings a want of only its edition needs.
const (
	detailVoiceover = "pl_voiceover"
	detailSubtitles = "pl_subtitles"
)

// detailPhrases are the listing phrases naming each detail. Each phrase
// word starts a listing word in a row (findPhrase), so "lektor" finds
// "lektorem" and "twarda oprawa" finds "twardej oprawie".
var detailPhrases = map[string][][]string{
	"steelbook":     {{"steelbook"}, {"steel", "book"}},
	"slipcover":     {{"slipcover"}, {"slip", "cover"}, {"obwolut"}, {"obwoluc"}},
	"digipak":       {{"digipak"}, {"digipack"}, {"digi", "pak"}, {"digi", "pack"}},
	"mediabook":     {{"mediabook"}, {"media", "book"}},
	detailVoiceover: {{"lektor"}, {"dubbing"}},
	detailSubtitles: {{"napis"}, {"subtitles"}},
	"hardcover": {
		{"hardcover"},
		{"hardback"},
		{"hard", "cover"},
		{"tward", "opraw"},
		{"tward", "oklad"},
		{"opraw", "tward"},
		{"oklad", "tward"},
	},
	"paperback": {
		{"paperback"},
		{"softcover"},
		{"soft", "cover"},
		{"broszurow"},
		{"miekk", "opraw"},
		{"miekk", "oklad"},
		{"opraw", "miekk"},
		{"oklad", "miekk"},
	},
}

// negations deny the detail named after them: "bez lektora", "brak
// polskich napisów".
var negations = map[string]bool{"bez": true, "brak": true}

// namedDetails returns the details a listing's words name, sorted. A
// phrase after "bez" or "brak", directly or past one "polski" or "pl"
// word, names nothing.
func namedDetails(text []string) []string {
	var out []string
	for detail, phrases := range detailPhrases {
		if slices.ContainsFunc(phrases, func(p []string) bool { return namesPhrase(text, p) }) {
			out = append(out, detail)
		}
	}
	slices.Sort(out)
	return out
}

// namesPhrase reports whether text holds phrase where no negation denies
// it.
func namesPhrase(text, phrase []string) bool {
	for i := 0; i+len(phrase) <= len(text); i++ {
		if findPhrase(text[i:i+len(phrase)], phrase) == 0 && !negated(text, i) {
			return true
		}
	}
	return false
}

// negated reports whether a negation denies the phrase starting at
// text[i]: it stands right before it, or before a "polski" or "pl" word
// that does.
func negated(text []string, i int) bool {
	if i >= 1 && negations[text[i-1]] {
		return true
	}
	return i >= 2 && negations[text[i-2]] && (text[i-1] == "pl" || strings.HasPrefix(text[i-1], "polsk"))
}

// requiredDetails are the details a want of only its edition
// (ExactEdition) needs a listing to name: each packaging, a Polish
// voice-over or subtitles set to "yes", and a hardcover or paperback
// binding. "no", "unknown" and another binding need nothing. Albums and
// wants of any edition need nothing.
func requiredDetails(w *Want) []string {
	if !w.ExactEdition || w.Kind == "music" {
		return nil
	}
	out := slices.Clone(w.Packaging)
	if w.PLVoiceover == "yes" {
		out = append(out, detailVoiceover)
	}
	if w.PLSubtitles == "yes" {
		out = append(out, detailSubtitles)
	}
	if w.Binding != nil && (*w.Binding == "hardcover" || *w.Binding == "paperback") {
		out = append(out, *w.Binding)
	}
	return out
}

// editionNamed reports whether a listing naming details names every
// detail w needs (requiredDetails), so such a want of an edition without
// those details matches as any other. An unknown detail is never named.
func editionNamed(w *Want, details []string) bool {
	for _, d := range requiredDetails(w) {
		if !slices.Contains(details, d) {
			return false
		}
	}
	return true
}

// wishNamedBy is SQL that holds when listing o names every detail wish
// w needs, as editionNamed decides from the details stored with the
// listing (edition_details). A listing stored before those were kept
// names none, so it no longer belongs to a wish that needs one.
func wishNamedBy(w string) string {
	return `(NOT ` + w + `.exact_edition OR EXISTS (SELECT FROM edition xe JOIN title xt ON xt.id = xe.title_id
		WHERE xe.id = ` + w + `.edition_id AND (xt.kind = 'music'
			OR coalesce(o.edition_details, '{}') @> (xe.packaging || array_remove(ARRAY[
				CASE WHEN xe.pl_voiceover = 'yes' THEN '` + detailVoiceover + `' END,
				CASE WHEN xe.pl_subtitles = 'yes' THEN '` + detailSubtitles + `' END,
				CASE WHEN xe.binding IN ('hardcover', 'paperback') THEN xe.binding END], NULL)))))`
}
