/**
 * The collection's filter, applied in the browser to the whole collection loaded once, so a
 * chip, a sheet choice or a search answers without asking the server. It lists what
 * `GET /api/collection` would for the same filter: the `WHERE` clause of `Store.Collection` in
 * backend-go/internal/collection/store.go, with the search normalized as the handler does.
 */
import type { CollectionFilter, CollectionItem } from './collection';
import { canonicalBarcode, checkDigitMatches } from './scan';

/**
 * The code the server's barcode.Normalize makes of a search: spaces, tabs and hyphens dropped,
 * an ISBN-10 as its ISBN-13, a UPC-A as its EAN-13 and zero padding trimmed. Null when the
 * server would not take it for a barcode, a wrong check digit included.
 */
export function searchBarcode(raw: string): string | null {
	const code = raw.replace(/[ \t-]/g, '').toUpperCase();
	return checkDigitMatches(code) ? canonicalBarcode(code) : null;
}

/** What the server searches for: the normalized barcode, else the trimmed text. */
export function searchTerm(q: string): string {
	return searchBarcode(q) ?? q.trim();
}

/**
 * The text a search looks in: title, original and Polish title, authors, label and shop, one
 * space apart, as `concat_ws` joins them; a missing title is left out, an empty field is not.
 */
function searchedText(item: CollectionItem): string {
	return [
		item.name,
		item.original_title,
		item.polish_title,
		(item.authors ?? []).join(' '),
		item.label,
		item.shop
	]
		.filter((field) => field !== null)
		.join(' ')
		.toLowerCase();
}

/**
 * The copies the filter lists, in their order: of its kind, medium and status, whose barcode is
 * the searched code or whose text holds the search, case aside.
 */
export function filterCollection(
	items: readonly CollectionItem[],
	f: CollectionFilter
): CollectionItem[] {
	const term = searchTerm(f.q);
	const words = term.toLowerCase();
	return items.filter(
		(item) =>
			(!f.kind || item.kind === f.kind) &&
			(!f.format || item.format === f.format) &&
			(!f.status || item.status === f.status) &&
			(!term || item.barcode === term || searchedText(item).includes(words))
	);
}
