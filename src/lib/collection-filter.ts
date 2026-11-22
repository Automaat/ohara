/**
 * The collection's filter, applied in the browser to the whole collection loaded once, so a
 * chip, a sheet choice or a search answers without asking the server. It lists what
 * `GET /api/collection` would for the same filter: the `WHERE` clause of `Store.Collection` in
 * backend-go/internal/collection/store.go, with the search normalized as the handler does.
 * The `unpriced` status, the owned copies without a purchase price, exists only here.
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

/** The white space Go's strings.TrimSpace drops: unicode.IsSpace, U+0085 included, U+FEFF not. */
const goSpace =
	'[\\t\\n\\v\\f\\r \\u0085\\u00a0\\u1680\\u2000-\\u200a\\u2028\\u2029\\u202f\\u205f\\u3000]';
const goTrim = new RegExp(`^${goSpace}+|${goSpace}+$`, 'g');

/**
 * What the server searches for when the address carries `q`: the handler's barcode.Normalize,
 * else the text the store trims by Go's rules.
 */
export function searchTerm(q: string): string {
	return searchBarcode(q) ?? q.replace(goTrim, '');
}

/**
 * Lower case one character at a time, as PostgreSQL's `lower` maps each character on its own:
 * a final capital sigma becomes σ, not ς, and a character whose lower case is longer keeps only
 * its first.
 */
export function lowerCase(text: string): string {
	let out = '';
	for (const char of text) {
		const lower = char.toLowerCase();
		out += [...lower][0] ?? lower;
	}
	return out;
}

/**
 * The text a search looks in: title, original and Polish title, authors, label, shop and
 * location, one space apart, as `concat_ws` joins them; a missing field is left out, an empty
 * one is not.
 */
function searchedText(item: CollectionItem): string {
	return lowerCase(
		[
			item.name,
			item.original_title,
			item.polish_title,
			(item.authors ?? []).join(' '),
			item.label,
			item.shop,
			item.location
		]
			.filter((field) => field != null)
			.join(' ')
	);
}

/** Whether a copy has the filter's status; `unpriced` is an owned copy without a price. */
function hasStatus(item: CollectionItem, status: CollectionFilter['status']): boolean {
	if (status === 'unpriced') return item.status === 'owned' && item.price_cents === null;
	return !status || item.status === status;
}

/**
 * The copies the filter lists, in their order: of its kind, medium, status and location, whose
 * barcode is the searched code or whose text holds the search, case aside.
 */
export function filterCollection(
	items: readonly CollectionItem[],
	f: CollectionFilter
): CollectionItem[] {
	const term = searchTerm(f.q);
	const words = lowerCase(term);
	const place = lowerCase((f.location ?? '').replace(goTrim, ''));
	return items.filter(
		(item) =>
			(!f.kind || item.kind === f.kind) &&
			(!f.format || item.format === f.format) &&
			hasStatus(item, f.status) &&
			(!place || (item.location != null && lowerCase(item.location) === place)) &&
			(!term || item.barcode === term || searchedText(item).includes(words))
	);
}

/**
 * The locations the copies are stored at, each once whatever its letter case, the most used
 * first, as `GET /api/copies/locations` lists them: a place spelled several ways goes by its most
 * used spelling.
 */
export function locationsInUse(items: readonly Pick<CollectionItem, 'location'>[]): string[] {
	const places = new Map<string, { copies: number; spellings: Map<string, number> }>();
	for (const { location } of items) {
		if (!location) continue;
		const key = lowerCase(location);
		const place = places.get(key) ?? { copies: 0, spellings: new Map<string, number>() };
		place.copies++;
		place.spellings.set(location, (place.spellings.get(location) ?? 0) + 1);
		places.set(key, place);
	}
	const byUse = (a: [string, number], b: [string, number]) =>
		b[1] - a[1] || a[0].localeCompare(b[0], 'pl');
	return [...places.entries()]
		.sort(([a, x], [b, y]) => y.copies - x.copies || a.localeCompare(b, 'pl'))
		.map(([, place]) => [...place.spellings.entries()].sort(byUse)[0]?.[0] ?? '');
}
