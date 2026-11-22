package lookup

import "slices"

// mergeMinConfidence keeps placeholders and fuzzy matches out of a merge:
// only catalog hits for the exact ISBN are folded together.
const mergeMinConfidence = 0.5

// mergeBooks folds the book candidates for code from several providers, the
// most confident one of each, into one candidate. cands is in provider
// registration order, which is the field priority: each field comes from
// the first provider that has it, so the national library's title and
// authors win while later catalogs fill in the cover and description. The
// merged candidate keeps the highest confidence of its parts.
func mergeBooks(code string, cands []Candidate) []Candidate {
	best := map[string]int{}
	var providers []string
	for i := range cands {
		c := &cands[i]
		if c.Type != "book" || c.Edition.Barcode != code || c.Confidence < mergeMinConfidence {
			continue
		}
		j, seen := best[c.Provider]
		switch {
		case !seen:
			providers = append(providers, c.Provider)
			best[c.Provider] = i
		case c.Confidence > cands[j].Confidence:
			best[c.Provider] = i
		}
	}
	if len(providers) < 2 {
		return cands
	}

	merged := cands[best[providers[0]]]
	merged.Sources = []string{providers[0]}
	for _, name := range providers[1:] {
		part := cands[best[name]]
		fillCandidate(&merged, &part)
		merged.Sources = append(merged.Sources, name)
		merged.Confidence = max(merged.Confidence, part.Confidence)
	}

	parts := make([]int, 0, len(providers))
	for _, name := range providers {
		parts = append(parts, best[name])
	}
	first := slices.Min(parts)
	out := make([]Candidate, 0, len(cands)-len(parts)+1)
	for i := range cands {
		switch {
		case i == first:
			out = append(out, merged)
		case !slices.Contains(parts, i):
			out = append(out, cands[i])
		}
	}
	return out
}

// fillCandidate copies into dst every field it lacks from src.
func fillCandidate(dst, src *Candidate) {
	fill(&dst.Title.Name, src.Title.Name)
	fillPtr(&dst.Title.OriginalTitle, src.Title.OriginalTitle)
	fillPtr(&dst.Title.PolishTitle, src.Title.PolishTitle)
	fillPtr(&dst.Title.Year, src.Title.Year)
	fillPtr(&dst.Title.TMDBID, src.Title.TMDBID)
	fillSlice(&dst.Title.Authors, src.Title.Authors)
	fillPtr(&dst.Title.BNID, src.Title.BNID)
	fillPtr(&dst.Title.OpenLibraryID, src.Title.OpenLibraryID)
	// A series' name and number come from one source, so a name never
	// takes another source's number.
	if dst.Title.SeriesName == nil {
		dst.Title.SeriesName, dst.Title.SeriesNumber = src.Title.SeriesName, src.Title.SeriesNumber
	}

	fill(&dst.Edition.Label, src.Edition.Label)
	fill(&dst.Edition.Publisher, src.Edition.Publisher)
	fillPtr(&dst.Edition.Year, src.Edition.Year)
	fillPtr(&dst.Edition.Format, src.Edition.Format)
	fill(&dst.Edition.PLSubtitles, src.Edition.PLSubtitles)
	fill(&dst.Edition.PLVoiceover, src.Edition.PLVoiceover)
	fillSlice(&dst.Edition.Packaging, src.Edition.Packaging)
	fillPtr(&dst.Edition.Binding, src.Edition.Binding)
	fillPtr(&dst.Edition.DiscogsReleaseID, src.Edition.DiscogsReleaseID)

	fill(&dst.CoverURL, src.CoverURL)
	fill(&dst.Description, src.Description)
}

func fill(dst *string, src string) {
	if *dst == "" {
		*dst = src
	}
}

func fillPtr[T any](dst **T, src *T) {
	if *dst == nil {
		*dst = src
	}
}

func fillSlice[T any](dst *[]T, src []T) {
	if len(*dst) == 0 {
		*dst = src
	}
}
