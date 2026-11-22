package deals

// Listing phrases naming an edition's details. Each phrase word starts a
// listing word in a row (findPhrase), so "lektor" finds "lektorem" and
// "twarda oprawa" finds "twardej oprawie".
var (
	packagingPhrases = map[string][][]string{
		"steelbook": {{"steelbook"}, {"steel", "book"}},
		"slipcover": {{"slipcover"}, {"slip", "cover"}, {"obwolut"}, {"obwoluc"}},
		"digipak":   {{"digipak"}, {"digipack"}, {"digi", "pak"}, {"digi", "pack"}},
		"mediabook": {{"mediabook"}, {"media", "book"}},
	}
	voiceoverPhrases = [][]string{{"lektor"}, {"dubbing"}}
	subtitlePhrases  = [][]string{{"napis"}, {"subtitles"}}
	bindingPhrases   = map[string][][]string{
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
)

// editionNamed reports whether a listing's words name every detail of the
// edition a want of only its edition (ExactEdition) has: each packaging,
// a Polish voice-over or subtitles set to "yes", and a hardcover or
// paperback binding. "no", "unknown" and another binding require nothing,
// so such a want of an edition without these details matches as any
// other. Albums and wants of any edition require nothing.
func editionNamed(w *Want, text []string) bool {
	if !w.ExactEdition || w.Kind == "music" {
		return true
	}
	for _, p := range w.Packaging {
		if !namesAny(text, packagingPhrases[p]) {
			return false
		}
	}
	if w.PLVoiceover == "yes" && !namesAny(text, voiceoverPhrases) {
		return false
	}
	if w.PLSubtitles == "yes" && !namesAny(text, subtitlePhrases) {
		return false
	}
	if w.Binding != nil {
		if phrases, ok := bindingPhrases[*w.Binding]; ok && !namesAny(text, phrases) {
			return false
		}
	}
	return true
}

// namesAny reports whether text holds one of phrases (findPhrase). An
// unknown detail has no phrases and is never named.
func namesAny(text []string, phrases [][]string) bool {
	for _, phrase := range phrases {
		if findPhrase(text, phrase) >= 0 {
			return true
		}
	}
	return false
}
