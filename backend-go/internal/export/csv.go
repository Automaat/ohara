package export

import (
	"encoding/csv"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/Automaat/ohara/backend-go/internal/collection"
	"github.com/Automaat/ohara/backend-go/internal/valuation"
)

// The Polish names the app shows for each enum value (src/lib/collection.ts
// and src/lib/valuation.ts); a value without one is written as it is.
var (
	kindLabels = map[string]string{
		collection.KindMovie: "Film", collection.KindBook: "Książka", collection.KindMusic: "Muzyka",
	}
	formatLabels = map[string]string{
		"dvd": "DVD", "bluray": "Blu-ray", "uhd": "4K UHD", "cd": "CD", "vinyl": "Winyl", "cassette": "Kaseta",
	}
	bindingLabels = map[string]string{
		"hardcover": "Twarda oprawa", "paperback": "Miękka oprawa", "other": "Inna oprawa",
	}
	statusLabels = map[string]string{
		collection.StatusOwned: "Na półce", collection.StatusWishlist: "Lista życzeń", collection.StatusSold: "Sprzedany",
	}
	acquiredLabels  = map[string]string{"new": "Nowy", "used": "Używany"}
	conditionLabels = map[string]string{
		"mint": "Idealny", "very_good": "Bardzo dobry", "good": "Dobry", "fair": "Dostateczny", "poor": "Słaby",
	}
	sourceLabels = map[string]string{
		valuation.SourceOLX: "OLX", valuation.SourceSkupSzop: "SkupSzop", valuation.SourceDiscogs: "Discogs",
	}
)

// header names the CSV's columns, in the order row fills them.
var header = []string{
	"Rodzaj", "Tytuł", "Tytuł oryginalny", "Tytuł polski", "Twórcy", "Rok", "Tagi",
	"Nośnik lub oprawa", "Wydanie", "Wydawca", "Kod kreskowy",
	"Status", "Cena zakupu (zł)", "Data zakupu", "Sklep", "Nowy czy używany", "Stan", "Miejsce", "Notatki",
	"Cena maksymalna (zł)", "Maks. % ceny rynkowej", "Data sprzedaży", "Cena sprzedaży (zł)",
	"Wartość: skup (zł)", "Źródło: skup", "Wartość: używany (zł)", "Źródło: używany",
	"Wartość: nowy (zł)", "Źródło: nowy",
}

// writeCSV writes titles for Polish Excel: a UTF-8 byte order mark, ";"
// between cells, one row per copy and one per title without copies. Lines
// end in LF: the writer's CRLF mode would drop a lone CR inside a cell.
func writeCSV(w io.Writer, titles []Title) error {
	if _, err := io.WriteString(w, "\uFEFF"); err != nil {
		return fmt.Errorf("write csv: %w", err)
	}
	out := csv.NewWriter(w)
	out.Comma = ';'
	write := func(cells []string) error {
		for i := range cells {
			cells[i] = defuse(cells[i])
		}
		return out.Write(cells)
	}
	if err := write(slices.Clone(header)); err != nil {
		return fmt.Errorf("write csv: %w", err)
	}
	for i := range titles {
		t := &titles[i]
		wrote := false
		for j := range t.Editions {
			e := &t.Editions[j]
			for k := range e.Copies {
				if err := write(copyRow(t, e, &e.Copies[k])); err != nil {
					return fmt.Errorf("write csv: %w", err)
				}
				wrote = true
			}
		}
		if !wrote {
			if err := write(titleRow(t)); err != nil {
				return fmt.Errorf("write csv: %w", err)
			}
		}
	}
	out.Flush()
	if err := out.Error(); err != nil {
		return fmt.Errorf("write csv: %w", err)
	}
	return nil
}

// titleCells are the cells of the title's columns.
func titleCells(t *Title) []string {
	return []string{
		label(kindLabels, t.Kind), t.Name, deref(t.OriginalTitle), deref(t.PolishTitle),
		strings.Join(t.Authors, ", "), intText(t.Year), strings.Join(t.Tags, ", "),
	}
}

// titleRow is the line of a title without copies: the rest is blank.
func titleRow(t *Title) []string {
	cells := titleCells(t)
	return append(cells, make([]string, len(header)-len(cells))...)
}

// copyRow is the line of copy c of edition e of title t.
func copyRow(t *Title, e *Edition, c *Copy) []string {
	cells := titleCells(t)
	medium := ""
	switch {
	case e.Format != nil:
		medium = label(formatLabels, *e.Format)
	case e.Binding != nil:
		medium = label(bindingLabels, *e.Binding)
	}
	cells = append(cells, medium, e.Label, e.Publisher, deref(e.Barcode))
	var purchased, sold string
	if c.PurchasedOn != nil {
		purchased = c.PurchasedOn.Format("2006-01-02")
	}
	if c.SoldOn != nil {
		sold = c.SoldOn.Format("2006-01-02")
	}
	cells = append(cells,
		label(statusLabels, c.Status), zloty(c.PriceCents), purchased, c.Shop,
		optLabel(acquiredLabels, c.AcquiredAs), optLabel(conditionLabels, c.Condition),
		deref(c.Location), c.Notes,
		zloty(c.MaxPriceCents), intText(c.MaxMarketPercent), sold, zloty(c.SalePriceCents),
	)
	for _, bound := range []*valuation.Estimate{c.Floor, c.Mid, c.Ceiling} {
		if bound == nil {
			cells = append(cells, "", "")
			continue
		}
		cells = append(cells, zloty(&bound.Cents), label(sourceLabels, bound.Source))
	}
	return cells
}

// defuse keeps a spreadsheet from reading a cell as a formula: a cell
// starting with "=", "+", "-" or "@", or a tab or carriage return some
// spreadsheets skip before one, gets a leading "'".
func defuse(cell string) string {
	if cell != "" && strings.ContainsRune("=+-@\t\r", rune(cell[0])) {
		return "'" + cell
	}
	return cell
}

// zloty writes grosze as złoty with a decimal comma, "49,99"; nil is blank.
func zloty(cents *int64) string {
	if cents == nil {
		return ""
	}
	sign, v := "", *cents
	if v < 0 {
		sign, v = "-", -v
	}
	return fmt.Sprintf("%s%d,%02d", sign, v/100, v%100)
}

func intText(v *int) string {
	if v == nil {
		return ""
	}
	return strconv.Itoa(*v)
}

func label(labels map[string]string, value string) string {
	if l, ok := labels[value]; ok {
		return l
	}
	return value
}

func optLabel(labels map[string]string, value *string) string {
	if value == nil {
		return ""
	}
	return label(labels, *value)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
