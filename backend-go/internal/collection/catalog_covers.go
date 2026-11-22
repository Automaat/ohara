package collection

import (
	"context"
	"strings"
	"time"

	"github.com/Automaat/ohara/backend-go/internal/lookup"
)

const coverLookupTimeout = 3 * time.Second

type catalogCovers struct {
	searchers map[string]lookup.TextSearcher
	byBarcode func(context.Context, string) lookup.Result
}

// WithCatalogCovers enables optional cover lookup during title creation and scans.
func (h *Handler) WithCatalogCovers(searchers map[string]lookup.TextSearcher, service *lookup.Service) *Handler {
	h.covers = &catalogCovers{searchers: searchers, byBarcode: service.Lookup}
	return h
}

func emptyCover(cover *string) bool {
	return cover == nil || *cover == ""
}

func (h *Handler) titleCover(ctx context.Context, title *TitleInput) *string {
	searcher, ok := h.covers.searchers[title.Kind]
	if !ok {
		return nil
	}
	query := title.Name
	if len(title.Authors) > 0 {
		query += " " + title.Authors[0]
	}
	lookupCtx, cancel := context.WithTimeout(ctx, coverLookupTimeout)
	defer cancel()
	candidates, err := searcher.SearchText(lookupCtx, query, "")
	if err != nil {
		h.logger.WarnContext(ctx, "title cover lookup failed", "source", searcher.Name(), "err", err)
		return nil
	}
	return uniqueCover(candidates, title, nil)
}

func (h *Handler) fillScannedCover(ctx context.Context, editionID int64) {
	edition, err := h.store.GetEdition(ctx, editionID)
	if err != nil {
		h.logger.WarnContext(ctx, "load edition for cover lookup", "edition_id", editionID, "err", err)
		return
	}
	if edition.Barcode == nil || !emptyCover(edition.CoverURL) || !emptyCover(edition.CoverOverrideURL) {
		return
	}
	title, err := h.store.GetTitle(ctx, edition.TitleID)
	if err != nil {
		h.logger.WarnContext(ctx, "load title for cover lookup", "edition_id", editionID, "err", err)
		return
	}
	lookupCtx, cancel := context.WithTimeout(ctx, coverLookupTimeout)
	defer cancel()
	result := h.covers.byBarcode(lookupCtx, *edition.Barcode)
	if result.NeedsConfirmation {
		return
	}
	cover := uniqueCover(result.Candidates, &title.TitleInput, &edition.EditionInput)
	if cover == nil {
		return
	}
	if err := h.store.fillEditionCover(ctx, &edition, *cover); err != nil {
		h.logger.WarnContext(ctx, "save scanned edition cover", "edition_id", editionID, "err", err)
	}
}

func uniqueCover(candidates []lookup.Candidate, title *TitleInput, edition *EditionInput) *string {
	var chosen string
	for i := range candidates {
		candidate := &candidates[i]
		if candidate.Type != title.Kind || !matchesTitle(title, &candidate.Title) {
			continue
		}
		if edition != nil && (candidate.Confidence < lookup.ConfirmThreshold || !matchesEdition(edition, &candidate.Edition)) {
			continue
		}
		cover := candidate.CoverURL
		if normalizeCoverURL(&cover) != nil || cover == "" {
			continue
		}
		if chosen != "" && chosen != cover {
			return nil
		}
		chosen = cover
	}
	if chosen == "" {
		return nil
	}
	return &chosen
}

func matchesEdition(edition *EditionInput, candidate *lookup.EditionFields) bool {
	return edition.Barcode != nil && *edition.Barcode == candidate.Barcode &&
		(edition.Format == nil || candidate.Format == nil || *edition.Format == *candidate.Format) &&
		(edition.Binding == nil || candidate.Binding == nil || *edition.Binding == *candidate.Binding) &&
		(edition.DiscogsReleaseID == nil ||
			candidate.DiscogsReleaseID != nil && *edition.DiscogsReleaseID == *candidate.DiscogsReleaseID)
}

func matchesTitle(title *TitleInput, candidate *lookup.TitleFields) bool {
	if title.Season != nil && (candidate.Season == nil || *title.Season != *candidate.Season) {
		return false
	}
	if title.TMDBID != nil {
		return candidate.TMDBID != nil && *title.TMDBID == *candidate.TMDBID
	}
	if title.Year != nil && candidate.Year != nil && *title.Year != *candidate.Year {
		return false
	}
	if !sameTitleName(title, candidate) {
		return false
	}
	for _, author := range title.Authors {
		if !matchesAuthor(author, candidate.Authors) {
			return false
		}
	}
	return true
}

func sameTitleName(title *TitleInput, candidate *lookup.TitleFields) bool {
	names := []*string{&candidate.Name, candidate.PolishTitle, candidate.OriginalTitle}
	for _, name := range []*string{&title.Name, title.PolishTitle, title.OriginalTitle} {
		if name == nil || *name == "" {
			continue
		}
		for _, other := range names {
			if other != nil && sameCatalogText(*name, *other) {
				return true
			}
		}
	}
	return false
}

func matchesAuthor(author string, options []string) bool {
	for _, option := range options {
		if sameCatalogText(author, option) {
			return true
		}
	}
	return false
}

func sameCatalogText(a, b string) bool {
	return strings.EqualFold(strings.Join(strings.Fields(a), " "), strings.Join(strings.Fields(b), " "))
}
