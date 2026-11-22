package deals

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// strokes maps the Latin letters whose diacritic is no combining mark
// (a stroke, hook, bar or middle dot), which canonical decomposition leaves
// whole, to their base letter, with the dotless i and j and the barred o
// (the small letter of "O WITH MIDDLE TILDE"). Every small Latin letter
// named "... WITH ..." without a canonical decomposition is here;
// TestWordsFoldEveryLatinDiacritic checks that against the Unicode names.
var strokes = map[rune]rune{
	'ø': 'o', 'đ': 'd', 'ħ': 'h', 'ı': 'i', 'ŀ': 'l', 'ł': 'l', 'ŧ': 't', 'ƀ': 'b', 'ɵ': 'o',
	'ƃ': 'b', 'ƈ': 'c', 'ƌ': 'd', 'ƒ': 'f', 'ƙ': 'k', 'ƚ': 'l', 'ƞ': 'n', 'ƥ': 'p',
	'ƫ': 't', 'ƭ': 't', 'ƴ': 'y', 'ƶ': 'z', 'ǥ': 'g', 'ȡ': 'd', 'ȥ': 'z', 'ȴ': 'l',
	'ȵ': 'n', 'ȶ': 't', 'ȷ': 'j', 'ȼ': 'c', 'ȿ': 's', 'ɀ': 'z', 'ɇ': 'e', 'ɉ': 'j',
	'ɋ': 'q', 'ɍ': 'r', 'ɏ': 'y', 'ɓ': 'b', 'ɕ': 'c', 'ɖ': 'd', 'ɗ': 'd', 'ɠ': 'g',
	'ɦ': 'h', 'ɨ': 'i', 'ɫ': 'l', 'ɬ': 'l', 'ɭ': 'l', 'ɱ': 'm', 'ɲ': 'n', 'ɳ': 'n',
	'ɼ': 'r', 'ɽ': 'r', 'ɾ': 'r', 'ʂ': 's', 'ʈ': 't', 'ʋ': 'v', 'ʐ': 'z', 'ʑ': 'z',
	'ʝ': 'j', 'ʠ': 'q', 'ᵬ': 'b', 'ᵭ': 'd', 'ᵮ': 'f', 'ᵯ': 'm', 'ᵰ': 'n', 'ᵱ': 'p',
	'ᵲ': 'r', 'ᵳ': 'r', 'ᵴ': 's', 'ᵵ': 't', 'ᵶ': 'z', 'ᵽ': 'p', 'ᶀ': 'b', 'ᶁ': 'd',
	'ᶂ': 'f', 'ᶃ': 'g', 'ᶄ': 'k', 'ᶅ': 'l', 'ᶆ': 'm', 'ᶇ': 'n', 'ᶈ': 'p', 'ᶉ': 'r',
	'ᶊ': 's', 'ᶌ': 'v', 'ᶍ': 'x', 'ᶎ': 'z', 'ᶏ': 'a', 'ᶑ': 'd', 'ᶒ': 'e', 'ᶖ': 'i',
	'ᶙ': 'u', 'ẚ': 'a', 'ỿ': 'y', 'ⱡ': 'l', 'ⱥ': 'a', 'ⱦ': 't', 'ⱨ': 'h', 'ⱪ': 'k',
	'ⱬ': 'z', 'ⱱ': 'v', 'ⱳ': 'w', 'ⱴ': 'v', 'ⱸ': 'e', 'ⱺ': 'o', 'ꝁ': 'k', 'ꝃ': 'k',
	'ꝅ': 'k', 'ꝉ': 'l', 'ꝋ': 'o', 'ꝍ': 'o', 'ꝑ': 'p', 'ꝓ': 'p', 'ꝕ': 'p', 'ꝗ': 'q',
	'ꝙ': 'q', 'ꝟ': 'v', 'ꞎ': 'l', 'ꞑ': 'n', 'ꞓ': 'c', 'ꞔ': 'c', 'ꞕ': 'h', 'ꞗ': 'b',
	'ꞙ': 'f', 'ꞡ': 'g', 'ꞣ': 'k', 'ꞥ': 'n', 'ꞧ': 'r', 'ꞩ': 's', 'ꞹ': 'u', 'ꟈ': 'd',
	'ꟊ': 's', 'ꟍ': 's', 'ꬴ': 'e', 'ꬷ': 'l', 'ꬸ': 'l', 'ꬹ': 'l', 'ꬺ': 'm', 'ꬻ': 'n',
	'ꭉ': 'r', 'ꭎ': 'u', 'ꭒ': 'u', 'ꭖ': 'x', 'ꭗ': 'x', 'ꭘ': 'x', 'ꭙ': 'x', 'ꭚ': 'y',
	'𝼉': 't', '𝼑': 'l', '𝼓': 'l', '𝼖': 'r', '𝼚': 'i', '𝼛': 'o', '𝼝': 'c', '𝼞': 's',
	'𝼥': 'd', '𝼦': 'l', '𝼧': 'n', '𝼨': 'r', '𝼩': 's', '𝼪': 't',
}

// fold lowercases s and folds every Latin letter with a diacritic to its
// base letter: "Björk" and "Bjork", "Márquez" and "Marquez", "Łódź" and
// "Lodz" fold alike. Marks on letters of other scripts stay.
func fold(s string) string {
	s = strings.ToLower(s)
	if isASCII(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	latin := false
	for _, r := range norm.NFD.String(s) {
		if unicode.Is(unicode.Mn, r) {
			if latin {
				continue
			}
		} else {
			latin = unicode.Is(unicode.Latin, r)
		}
		if base, ok := strokes[r]; ok {
			r = base
		}
		b.WriteRune(r)
	}
	return norm.NFC.String(b.String())
}

func isASCII(s string) bool {
	for i := range len(s) {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// words lowercases s, folds diacritics and splits it into runs of letters,
// digits and marks.
func words(s string) []string {
	return split(fold(s))
}

// spelledWords lowercases s and splits it as words does, keeping its
// diacritics.
func spelledWords(s string) []string {
	return split(norm.NFC.String(strings.ToLower(s)))
}

// split cuts s into words, dropping marks that start one: a mark with no
// letter before it belongs to no word.
func split(s string) []string {
	fields := strings.FieldsFunc(s, notWordRune)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f = strings.TrimLeftFunc(f, unicode.IsMark); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// gap is what stands between two words of a listing, before its first or
// after its last. gapJoined is punctuation that ties words together
// ("X-Men", "M:I", "It's", an initial's "M."), gapSpace is spaces alone
// or nothing, and gapApart sets words apart: a bracket, slash, bar or
// double quote, or a dash, colon, semicolon or comma next to a space.
type gap uint8

const (
	gapJoined gap = iota
	gapSpace
	gapApart
)

// gapOf tells what kind of gap s, the text between two words, is.
func gapOf(s string) gap {
	switch {
	case strings.TrimSpace(s) == "":
		return gapSpace
	case strings.ContainsAny(s, "()[]{}/|\"„”“«»"):
		return gapApart
	case strings.ContainsAny(s, ":;,-–—") && strings.ContainsFunc(s, unicode.IsSpace):
		return gapApart
	}
	return gapJoined
}

// splitGaps is split, also telling the gap before each word: gaps[i]
// stands before words[i], and gaps[len(words)] after the last word.
// Between the end of one word and the next stand only separators and the
// marks split drops, and a word starts with neither, so the next word's
// first occurrence past the previous one is where it stands.
func splitGaps(s string) ([]string, []gap) {
	ws := split(s)
	gaps := make([]gap, len(ws)+1)
	at := 0
	for i, w := range ws {
		start := at + strings.Index(s[at:], w)
		gaps[i] = gapOf(s[at:start])
		at = start + len(w)
	}
	gaps[len(ws)] = gapOf(s[at:])
	return ws, gaps
}

// spelledKey is s's words in its own spelling, to tell names and queries
// apart that differ in more than case and punctuation: "Amélie" and
// "Amelie" are two spellings to search for.
func spelledKey(s string) string {
	return strings.Join(spelledWords(s), " ")
}

// notWordRune splits words: anything but a letter, a digit or a mark. A
// mark no precomposed letter takes stays with its letter, so "Adébáyọ̀"
// keeps its final grave in its own spelling.
func notWordRune(r rune) bool {
	return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.IsMark(r)
}
