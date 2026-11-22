package collection

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Export reads every title with its editions and their copies, sold ones
// too, through tx: titles by name, editions and copies in the order a title
// page lists them, and the date in Poland (YYYY-MM-DD) of tx's snapshot.
func Export(ctx context.Context, tx pgx.Tx) ([]TitleDetail, string, error) {
	var day string
	if err := tx.QueryRow(ctx, `SELECT to_char(now() AT TIME ZONE 'Europe/Warsaw', 'YYYY-MM-DD')`).
		Scan(&day); err != nil {
		return nil, "", fmt.Errorf("export day: %w", err)
	}
	rows, err := tx.Query(ctx, `SELECT `+titleColumns+` FROM title t ORDER BY lower(t.name), t.id`)
	if err != nil {
		return nil, "", fmt.Errorf("export titles: %w", err)
	}
	all, err := collect(rows, scanTitle, "export titles")
	if err != nil {
		return nil, "", err
	}
	rows, err = tx.Query(ctx, `SELECT `+editionColumns+` FROM edition e ORDER BY e.year NULLS LAST, e.id`)
	if err != nil {
		return nil, "", fmt.Errorf("export editions: %w", err)
	}
	editions, err := collect(rows, scanEdition, "export editions")
	if err != nil {
		return nil, "", err
	}
	rows, err = tx.Query(ctx, `SELECT `+copyColumns+` FROM copy c ORDER BY c.id`)
	if err != nil {
		return nil, "", fmt.Errorf("export copies: %w", err)
	}
	copies, err := collect(rows, scanCopy, "export copies")
	if err != nil {
		return nil, "", err
	}
	return nest(all, editions, copies), day, nil
}

// nest hangs each copy under its edition and each edition under its title,
// keeping the order of every list.
func nest(titles []Title, editions []Edition, copies []Copy) []TitleDetail {
	byEdition := make(map[int64][]Copy, len(editions))
	for i := range copies {
		byEdition[copies[i].EditionID] = append(byEdition[copies[i].EditionID], copies[i])
	}
	byTitle := make(map[int64][]EditionWithCopies, len(titles))
	for i := range editions {
		e := &editions[i]
		own := byEdition[e.ID]
		if own == nil {
			own = []Copy{}
		}
		byTitle[e.TitleID] = append(byTitle[e.TitleID], EditionWithCopies{Edition: *e, Copies: own})
	}
	out := make([]TitleDetail, 0, len(titles))
	for i := range titles {
		own := byTitle[titles[i].ID]
		if own == nil {
			own = []EditionWithCopies{}
		}
		out = append(out, TitleDetail{Title: titles[i], Editions: own})
	}
	return out
}
