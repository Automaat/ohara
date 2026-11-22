package deals

import (
	"math"
	"slices"
	"strings"
	"time"
	"unicode"
)

// Want is a wishlist copy the hunter searches for, with its edition and
// title. MaxPriceCents is nil when the owner set no limit and
// MaxMarketPercent when they set no share of the market price;
// MarketCents is the trusted used market price of the wished title and
// format, nil without one. A want with neither rule has its listings
// recorded but never alerted.
type Want struct {
	CopyID           int64
	EditionID        int64
	TitleID          int64
	Kind             string
	Name             string
	OriginalTitle    *string
	PolishTitle      *string
	Authors          []string
	Format           *string
	MaxPriceCents    *int64
	MaxMarketPercent *int
	MarketCents      *int64

	// prepared is the text work matching needs from the names and
	// authors, kept by Prepare.
	prepared *wantText
}

// Meets reports whether a listing at priceCents alerts for w: at or under
// its limit, or at or under its percent of the market price.
func (w *Want) Meets(priceCents int64) bool {
	if w.MaxPriceCents != nil && priceCents <= *w.MaxPriceCents {
		return true
	}
	return w.MaxMarketPercent != nil && w.MarketCents != nil &&
		priceCents*100 <= *w.MarketCents*int64(*w.MaxMarketPercent)
}

// Prepare does the text work matching needs from w's names and authors
// once, so matching many listings against w does not repeat it. A want
// whose names or authors change afterwards is matched as it is then.
func (w *Want) Prepare() {
	w.prepared = newWantText(w)
}

// wantText is a want's names and authors split into the words matching
// compares.
type wantText struct {
	// The fields it was made from, to notice a want changed since.
	name, polish, original string
	hasPolish, hasOriginal bool
	authors                []string

	names    []nameText // the titles the want is sold under
	ownWords [][]string // the words of each name, then of each author
	surname  string     // the last word of the first author
	artist   []string   // the required words of the first author
	set      bool       // a name says the want is a set ("Trylogia")
}

// nameText is one of a want's names with its words and the words a
// listing must contain to carry it.
type nameText struct {
	name  string
	words []string
	req   []string
}

// newWantText splits w's names and authors. Its names are the titles a
// want is sold under: Polish, original and the name it was entered with,
// without duplicates.
func newWantText(w *Want) *wantText {
	p := &wantText{name: w.Name, authors: slices.Clone(w.Authors)}
	if w.PolishTitle != nil {
		p.polish, p.hasPolish = *w.PolishTitle, true
	}
	if w.OriginalTitle != nil {
		p.original, p.hasOriginal = *w.OriginalTitle, true
	}
	seen := map[string]bool{}
	for _, n := range []*string{w.PolishTitle, w.OriginalTitle, &w.Name} {
		if n == nil {
			continue
		}
		ws := words(*n)
		key := strings.Join(ws, " ")
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		p.names = append(p.names, nameText{name: *n, words: ws, req: significant(ws)})
		p.ownWords = append(p.ownWords, ws)
		p.set = p.set || slices.ContainsFunc(ws, func(w string) bool { return setWords[w] })
	}
	for _, a := range w.Authors {
		p.ownWords = append(p.ownWords, words(a))
	}
	if len(w.Authors) > 0 {
		if ws := words(w.Authors[0]); len(ws) > 0 {
			p.surname = ws[len(ws)-1]
		}
		p.artist = required(w.Authors[0])
	}
	return p
}

// from reports whether p was made from w's current names and authors.
func (p *wantText) from(w *Want) bool {
	return p.name == w.Name &&
		sameText(p.hasPolish, p.polish, w.PolishTitle) &&
		sameText(p.hasOriginal, p.original, w.OriginalTitle) &&
		slices.Equal(p.authors, w.Authors)
}

func sameText(has bool, s string, p *string) bool {
	if p == nil {
		return !has
	}
	return has && s == *p
}

// text returns w's prepared text, or splits it now when w was not
// prepared or changed since.
func (w *Want) text() *wantText {
	if p := w.prepared; p != nil && p.from(w) {
		return p
	}
	return newWantText(w)
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
	return NewListingText(title, r).rejectedFor(nil)
}

// RejectedFor is Rejected for a listing of w: a reject phrase that w's own
// names or artist contain does not count, so "soundtrack", which keeps
// soundtrack CDs away from films, still finds a wanted soundtrack album.
func (r Rules) RejectedFor(title string, w *Want) bool {
	if w == nil {
		return r.Rejected(title)
	}
	return NewListingText(title, r).rejectedFor(w.text())
}

// rejects returns the reject phrases text contains once every accept
// phrase is cut from it.
func (r Rules) rejects(text []string) [][]string {
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
	var out [][]string
	for _, reject := range r.Reject {
		if phrase := words(reject); findPhrase(text, phrase) >= 0 {
			out = append(out, phrase)
		}
	}
	return out
}

// mentions reports whether one of the want's names or authors contains
// phrase.
func (p *wantText) mentions(phrase []string) bool {
	for _, ws := range p.ownWords {
		if findPhrase(ws, phrase) >= 0 {
			return true
		}
	}
	return false
}

// ListingText is a listing title readied for matching against many wants:
// its words, the format it names and the reject phrases it carries are
// worked out once.
type ListingText struct {
	words   []string
	format  string
	rejects [][]string
	// plain is words without disc counts, and significant that without
	// stop words and single letters, for Outranks.
	plain       []string
	significant []string
	// lastYear is the latest release year a number after a name can be:
	// next year, read once for the listing.
	lastYear int
	// titleCount is set when the listing counts titles ("3 filmy"), a
	// lot anywhere, and leads are where it names a set ("zestaw") or
	// counts five discs or volumes or more ("5 płyt"), a lot only before
	// the wanted name.
	titleCount bool
	leads      []int
}

// NewListingText readies a listing title for matching under rules.
func NewListingText(title string, rules Rules) *ListingText {
	text := words(title)
	plain := withoutDiscCounts(text)
	titleCount, leads := lotWords(text)
	return &ListingText{
		words:       text,
		format:      listingFormat(text),
		rejects:     rules.rejects(text),
		plain:       plain,
		significant: significant(plain),
		lastYear:    time.Now().Year() + 1,
		titleCount:  titleCount,
		leads:       leads,
	}
}

// Belongs reports whether the listing is a copy of w: w matches it
// (Want.Matches), it holds no reject phrase w's names and authors lack
// (Rules.RejectedFor) and it names no other title of titles more
// specifically (Titles.Outranks).
func (l *ListingText) Belongs(w *Want, titles Titles) bool {
	p := w.text()
	return l.matches(w, p) && !l.rejectedFor(p) && !titles.outranks(w, p, l)
}

// rejectedFor reports whether the listing holds a reject phrase the want
// of p does not mention; any reject phrase counts when p is nil.
func (l *ListingText) rejectedFor(p *wantText) bool {
	for _, phrase := range l.rejects {
		if p == nil || !p.mentions(phrase) {
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
	return significant(words(name))
}

// significant drops stop words and single letters from ws, keeping
// numbers.
func significant(ws []string) []string {
	out := make([]string, 0, len(ws))
	for _, w := range ws {
		if (len([]rune(w)) > 1 || unicode.IsDigit([]rune(w)[0])) && !stopWords[w] {
			out = append(out, w)
		}
	}
	return out
}

// artist is the first credited artist of an album.
func (w *Want) artist() string {
	if w.Kind != "music" || len(w.Authors) == 0 {
		return ""
	}
	return w.Authors[0]
}

// formatKeyword is the format word added to a movie search query.
var formatKeyword = map[string]string{"dvd": "dvd", "bluray": "blu-ray", "uhd": "4k"}

// Queries are the search phrases for w: each name with the format keyword
// for a movie, with the author's surname for a book, or after the artist
// for an album ("Pink Floyd The Wall"), as album titles alone are often
// generic. An album query names no medium: CD listings often do not.
func (w *Want) Queries() []string {
	p := w.text()
	suffix := ""
	switch {
	case w.Kind == "movie" && w.Format != nil:
		suffix = formatKeyword[*w.Format]
	case w.Kind == "book":
		// Book listings carry the author's surname far more often than
		// first names.
		suffix = p.surname
	}
	artist := strings.TrimSpace(w.artist())
	var out []string
	for _, n := range p.names {
		q := strings.TrimSpace(n.name)
		if suffix != "" && !slices.Contains(words(q), suffix) {
			q += " " + suffix
		}
		if artist != "" && !containsAll(words(q), required(artist)) {
			q = artist + " " + q
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
	return listingFormatSkipping(text, nil)
}

// listingFormatSkipping is listingFormat of text without the words skip
// reports, which a nil skip keeps. The words hold no spaces, so a
// "4k", "bd" or "ultra hd" counts as whole words in a row and a "uhd",
// "bluray" or "dvd" anywhere in a word.
func listingFormatSkipping(text []string, skip func(string) bool) string {
	var uhd, bluray, dvd bool
	prev := ""
	for _, t := range text {
		if skip != nil && skip(t) {
			continue
		}
		if t == "4k" || strings.Contains(t, "uhd") || (prev == "ultra" && t == "hd") {
			uhd = true
		}
		if strings.Contains(t, "bluray") || (prev == "blu" && t == "ray") || t == "bd" {
			bluray = true
		}
		if strings.Contains(t, "dvd") {
			dvd = true
		}
		prev = t
	}
	switch {
	case uhd:
		return "uhd"
	case bluray:
		return "bluray"
	case dvd:
		return "dvd"
	}
	return ""
}

// owns reports whether word is a word of one of the want's names or
// authors.
func (p *wantText) owns(word string) bool {
	for _, ws := range p.ownWords {
		if slices.Contains(ws, word) {
			return true
		}
	}
	return false
}

// albumFormat is the medium an album listing names: vinyl ("winyl", "LP"),
// cassette ("kaseta", "MC") or CD, else "".
func albumFormat(text []string, skip func(string) bool) string {
	for _, t := range text {
		if skip(t) {
			continue
		}
		switch t {
		case "winyl", "winyle", "winylowa", "winylowy", "vinyl", "vinyls", "lp", "2lp", "3lp", "gramofonowa":
			return "vinyl"
		case "kaseta", "kasety", "kasecie", "kasetowa", "cassette", "mc":
			return "cassette"
		}
	}
	for _, t := range text {
		if skip(t) {
			continue
		}
		if t == "cd" || t == "2cd" || t == "3cd" || t == "cdr" || t == "sacd" {
			return "cd"
		}
	}
	return ""
}

// albumFormatMatches reports whether a listing can be the wanted album
// format: a vinyl or cassette listing names its medium, a CD listing often
// does not, so a listing naming no medium counts as a CD unless it names a
// film disc (a concert DVD or the film of the album). The words of the
// want's names and artists name no medium, so "MC Hammer" or an album
// called "Vinyl" does not.
func (p *wantText) albumFormatMatches(want string, text []string) bool {
	got := albumFormat(text, p.owns)
	if want == "cd" && got == "" {
		return listingFormatSkipping(text, p.owns) == ""
	}
	return got == want
}

// romanParts are the sequel numerals a movie title can carry.
var romanParts = map[string]bool{
	"ii": true, "iii": true, "iv": true, "v": true, "vi": true, "vii": true, "viii": true, "ix": true, "x": true,
}

// ordinalParts are the sequel words that follow "Part" or "Część", as in
// "Dune: Part Two" or "Diuna: Część druga".
var ordinalParts = map[string]bool{
	"two": true, "three": true, "four": true, "five": true, "second": true, "third": true,
	"druga": true, "trzecia": true, "czwarta": true, "piata": true,
	"drugi": true, "trzeci": true, "czwarty": true, "piaty": true,
}

// partWords introduce a sequel number and are skipped before it.
var partWords = map[string]bool{"part": true, "czesc": true, "cz": true, "chapter": true, "rozdzial": true}

// sequelAfter reports whether text names a sequel of the name of
// nameWords: right after its last word, past a "Part" or "Część", comes a
// number or numeral the name lacks and that is no release year, as in
// "Blade Runner 2049", "Rocky II" or "Dune: Part Two" for "Dune". "Part 1"
// is the first film. Release years run from 1900 to lastYear.
func sequelAfter(text, nameWords []string, lastYear int) bool {
	if len(nameWords) == 0 {
		return false
	}
	last := nameWords[len(nameWords)-1]
	for i, t := range text {
		if t != last {
			continue
		}
		j, part := i+1, false
		for j < len(text) && partWords[text[j]] && !slices.Contains(nameWords, text[j]) {
			j, part = j+1, true
		}
		if j >= len(text) {
			continue
		}
		next := text[j]
		if slices.Contains(nameWords, next) {
			continue
		}
		if romanParts[next] || (part && ordinalParts[next]) {
			return true
		}
		if !part && j+1 < len(text) && titleCounts[text[j+1]] {
			// "Trylogia 3 filmy" counts films, it names no sequel, while
			// "Część 2 filmy" still names the second part. A disc count
			// stays a sequel number: "2049 płyty" names the sequel.
			continue
		}
		if n, ok := number(next); ok && (!part || n != 1) && (n < 1900 || n > lastYear) {
			return true
		}
	}
	return false
}

// Matches reports whether a listing title is w: it contains every required
// word of one of w's names, the edition's format for a movie, the author's
// surname for a book, and the artist's words and a medium that fits the
// edition's for an album. A movie listing naming a sequel of that name does
// not match, and neither does a lot of several titles
// (ListingText.lotFor).
func (w *Want) Matches(title string) bool {
	return NewListingText(title, Rules{}).matches(w, w.text())
}

// matches is Matches for the want of p.
func (l *ListingText) matches(w *Want, p *wantText) bool {
	return qualifies(w, p, l) && p.carries(w.Kind, l) && !l.lotFor(w.Kind, p)
}

// lotFor reports whether the listing sells several titles for the want
// of p. It counts titles ("3 filmy", "20 szt.") or a collection of them
// ("kolekcja filmów"), unless the want is a set itself ("Trylogia"); or,
// before the wanted name, where it starts a list, it names a set
// ("zestaw", "pakiet", "komplet") or counts five discs or volumes or more
// ("5 płyt", "8 tomów"), unless a name of the want holds that word. Past
// the name those words describe one title's edition ("zestaw 4K +
// Blu-ray", "6 płyt CD", "5 tomów"), and one title on a few discs ("2
// płyty", "5xDVD") or in a newspaper's collection is no lot.
func (l *ListingText) lotFor(kind string, p *wantText) bool {
	if l.titleCount && !p.set {
		return true
	}
	if len(l.leads) == 0 {
		return false
	}
	start := p.nameStart(kind, l)
	for _, i := range l.leads {
		if i < start && !p.owns(l.words[i]) {
			return true
		}
	}
	return false
}

// nameStart is where the first word of a name of p the listing carries
// stands in it, or -1 when it carries none.
func (p *wantText) nameStart(kind string, l *ListingText) int {
	start := -1
	for i := range p.names {
		n := &p.names[i]
		if !n.carriedBy(kind, l) {
			continue
		}
		for _, w := range n.req {
			if j := slices.Index(l.words, w); j >= 0 && (start < 0 || j < start) {
				start = j
			}
		}
	}
	return start
}

// setWords in a want's name make it a set, which counts of titles may be.
var setWords = map[string]bool{
	"trylogia": true, "trilogy": true, "tetralogia": true, "saga": true, "antologia": true, "anthology": true,
	"kolekcja": true, "collection": true, "komplet": true, "zestaw": true, "pakiet": true, "box": true, "boxset": true,
}

// lotNouns name a set, of several titles when they start a list.
var lotNouns = map[string]bool{
	"zestaw": true, "zestawy": true, "zestawu": true, "zestawie": true, "zestawow": true,
	"pakiet": true, "pakiety": true, "pakietu": true, "pakiecie": true, "pakietow": true,
	"komplet": true, "komplety": true, "kompletu": true, "komplecie": true, "kompletow": true,
}

// collections are a collection's forms, a lot when it holds the titles
// of a plural noun ("kolekcja filmów"), not a publisher ("Kolekcja
// Gazety Wyborczej").
var collections = map[string]bool{"kolekcja": true, "kolekcji": true, "kolekcje": true}

// collected are the plural nouns that make a collection a lot.
var collected = map[string]bool{
	"filmow": true, "ksiazek": true, "plyt": true, "albumow": true, "bajek": true, "tytulow": true,
}

// titleCounts follow a count of titles or pieces, as in "3 filmy" or "20
// szt.".
var titleCounts = map[string]bool{
	"filmy": true, "filmow": true, "ksiazki": true, "ksiazek": true, "albumy": true, "albumow": true,
	"tytuly": true, "tytulow": true, "szt": true, "sztuk": true, "sztuki": true,
}

// manyUnits follow counts of five discs or volumes or more: "5 płyt",
// "8 tomów", while "2 płyty" and "2 tomy" take other forms.
var manyUnits = map[string]bool{"plyt": true, "tomow": true}

// lotWords reports whether text counts titles, and where it names a set
// or counts five discs or volumes or more.
func lotWords(text []string) (bool, []int) {
	titleCount := false
	var leads []int
	for i, t := range text {
		if lotNouns[t] {
			leads = append(leads, i)
		}
		if i+1 >= len(text) {
			continue
		}
		next := text[i+1]
		if collections[t] && collected[next] {
			titleCount = true
		}
		if n, ok := number(t); ok && n >= 2 {
			titleCount = titleCount || titleCounts[next]
			if manyUnits[next] {
				leads = append(leads, i)
			}
		}
	}
	return titleCount, leads
}

// qualifies reports whether a listing fits w apart from its names: the
// edition's format for a movie, the author's surname for a book, and the
// artist's words and a fitting medium for an album.
func qualifies(w *Want, p *wantText, l *ListingText) bool {
	text := l.words
	if w.Kind == "movie" && w.Format != nil && l.format != *w.Format {
		return false
	}
	if w.Kind == "book" && p.surname != "" && !slices.Contains(text, p.surname) {
		return false
	}
	if w.Kind == "music" {
		if !containsAll(text, p.artist) {
			return false
		}
		if w.Format != nil && !p.albumFormatMatches(*w.Format, text) {
			return false
		}
	}
	return true
}

// carriedBy reports whether a listing carries n: its words hold every
// required word of n and, for a movie, name no sequel of n.
func (n *nameText) carriedBy(kind string, l *ListingText) bool {
	return len(n.req) > 0 && containsAll(l.words, n.req) &&
		(kind != "movie" || !sequelAfter(l.words, n.words, l.lastYear))
}

// carries reports whether a listing carries one of the want's names.
func (p *wantText) carries(kind string, l *ListingText) bool {
	for i := range p.names {
		if p.names[i].carriedBy(kind, l) {
			return true
		}
	}
	return false
}

// number parses a word of ASCII digits as strconv.Atoi would, failing
// for any other word or one past math.MaxInt, without the error Atoi
// allocates. Words never carry a sign.
func number(word string) (int, bool) {
	if word == "" {
		return 0, false
	}
	n := 0
	for i := range len(word) {
		d := int(word[i]) - '0'
		if d < 0 || d > 9 || n > (math.MaxInt-d)/10 {
			return 0, false
		}
		n = n*10 + d
	}
	return n, true
}

// maxNames is how many names a want has at most: Polish, original and
// the name it was entered with.
const maxNames = 3

// carried appends to out the required words of each of the want's names
// that a listing carries.
func (p *wantText) carried(kind string, l *ListingText, out [][]string) [][]string {
	for i := range p.names {
		if n := &p.names[i]; n.carriedBy(kind, l) {
			out = append(out, n.req)
		}
	}
	return out
}

// Titles are the collection's titles, wished, owned or neither, that a
// listing may name instead of the want it matched.
type Titles struct {
	byKind map[string][]knownTitle
}

type knownTitle struct {
	title Want
	names []nameText
}

// NewTitles readies titles, each a Want without an edition, for Outranks.
func NewTitles(titles []Want) Titles {
	t := Titles{byKind: map[string][]knownTitle{}}
	for i := range titles {
		w := titles[i]
		w.Format = nil
		w.Prepare()
		k := knownTitle{title: w}
		for _, n := range w.text().names {
			if len(n.req) > 0 {
				k.names = append(k.names, n)
			}
		}
		t.byKind[w.Kind] = append(t.byKind[w.Kind], k)
	}
	return t
}

// Outranks reports whether a listing w matches names another title of w's
// kind more specifically: one of that title's names holds every required
// word of a name of w the listing carries, as often, and more; the listing
// carries that name as one phrase; and it fits that title's author or
// artist. A phrase may skip stop words and media words ("Alien DVD
// Covenant"), and a disc count ("2 płyty") is no part of a title. So
// "Alien: Covenant DVD" leaves an "Alien" want when "Alien: Covenant" is
// known, while "Gladiator DVD 2 płyty" stays with "Gladiator" when
// "Gladiator 2" is.
func (t Titles) Outranks(w *Want, title string) bool {
	if len(t.byKind[w.Kind]) == 0 {
		return false
	}
	return t.outranks(w, w.text(), NewListingText(title, Rules{}))
}

func (t Titles) outranks(w *Want, p *wantText, l *ListingText) bool {
	known := t.byKind[w.Kind]
	if len(known) == 0 {
		return false
	}
	var buf [maxNames][]string
	mine := p.carried(w.Kind, l, buf[:0])
	if len(mine) == 0 {
		return false
	}
	for i := range known {
		k := &known[i]
		if k.title.TitleID == w.TitleID {
			continue
		}
		for j := range k.names {
			n := &k.names[j]
			if extendsAny(n.req, mine) && carriesPhrase(l.significant, n.req) &&
				(k.title.Kind != "movie" || !sequelAfter(l.plain, n.words, l.lastYear)) &&
				qualifies(&k.title, k.title.text(), l) {
				return true
			}
		}
	}
	return false
}

// extendsAny reports whether name holds every word of one of names, each
// at least as often, and more words besides.
func extendsAny(name []string, names [][]string) bool {
	for _, shorter := range names {
		if extends(name, shorter) {
			return true
		}
	}
	return false
}

func extends(name, shorter []string) bool {
	if len(name) <= len(shorter) {
		return false
	}
	for i, w := range shorter {
		if slices.Contains(shorter[:i], w) {
			continue
		}
		if occurrences(name, w) < occurrences(shorter, w) {
			return false
		}
	}
	return true
}

// occurrences is how often w occurs in ws.
func occurrences(ws []string, w string) int {
	n := 0
	for _, x := range ws {
		if x == w {
			n++
		}
	}
	return n
}

// mediaWords name a disc format or medium, which sellers put anywhere in
// a listing title, even inside the film's name.
var mediaWords = map[string]bool{
	"dvd": true, "bluray": true, "blu": true, "ray": true, "bd": true, "4k": true, "uhd": true, "ultra": true, "hd": true,
	"cd": true, "2cd": true, "3cd": true, "cdr": true, "sacd": true, "lp": true, "2lp": true, "3lp": true,
	"vinyl": true, "vinyls": true, "winyl": true, "winyle": true, "kaseta": true, "kasety": true, "cassette": true, "mc": true,
}

// discWords follow a disc count, as in "2 płyty" or "2 discs".
var discWords = map[string]bool{
	"plyty": true, "plyt": true, "plytach": true, "plytowe": true, "plytowa": true, "plytowy": true,
	"disc": true, "discs": true, "disk": true, "disks": true, "dyski": true, "dyskow": true,
}

// withoutDiscCounts drops each number followed by a disc word, and that
// word.
func withoutDiscCounts(text []string) []string {
	out := make([]string, 0, len(text))
	for i := 0; i < len(text); i++ {
		if i+1 < len(text) && discWords[text[i+1]] {
			if _, ok := number(text[i]); ok {
				i++
				continue
			}
		}
		out = append(out, text[i])
	}
	return out
}

// carriesPhrase reports whether significant, listing words without stop
// words and single letters, holds req as consecutive words once the media
// words req lacks are left out.
func carriesPhrase(significant, req []string) bool {
	if len(req) == 0 {
		return true
	}
	skipped := func(t string) bool { return mediaWords[t] && !slices.Contains(req, t) }
	for i := range significant {
		if skipped(significant[i]) {
			continue
		}
		j := 0
		for k := i; k < len(significant) && j < len(req); k++ {
			if t := significant[k]; !skipped(t) {
				if t != req[j] {
					break
				}
				j++
			}
		}
		if j == len(req) {
			return true
		}
	}
	return false
}

// pick returns the want a listing belongs to, or nil. A want loses a
// listing that names another known title more specifically (Outranks).
// Among the remaining matching wants it prefers one whose limit or market
// share the listing meets, so a cheap listing alerts even when an earlier
// want for the same title has a lower limit.
func pick(wants []Want, l *Listing, rules Rules, titles Titles) *Want {
	text := NewListingText(l.Title, rules)
	var first *Want
	for i := range wants {
		w := &wants[i]
		if !text.Belongs(w, titles) {
			continue
		}
		if w.Meets(l.PriceCents) {
			return w
		}
		if first == nil {
			first = w
		}
	}
	return first
}
