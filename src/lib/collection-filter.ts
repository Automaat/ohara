/**
 * The collection's filter, applied in the browser to the whole collection loaded once, so a
 * chip, a sheet choice or a search answers without asking the server. It lists what
 * `GET /api/collection` would for the same filter: the `WHERE` clause of `Store.Collection` in
 * backend-go/internal/collection/store.go, with the search normalized as the handler does.
 * The `unpriced` status, the owned copies without a purchase price, exists only here, and
 * without a status the page leaves out the sold copies the server lists, which only "Sprzedane"
 * shows.
 */
import type { CollectionFilter, CollectionItem } from './collection';
import { trimSpace } from './go-text';
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

/**
 * What the server searches for when the address carries `q`: the handler's barcode.Normalize,
 * else the text the store trims by Go's rules.
 */
export function searchTerm(q: string): string {
	return searchBarcode(q) ?? trimSpace(q);
}

/**
 * Lower case one character at a time, as PostgreSQL's `lower` and Go's unicode.ToLower map each
 * character on its own: a final capital sigma becomes σ, not ς, and a character whose lower case
 * is longer keeps only its first.
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
 * The key locations match and merge by, as the server's foldCase makes it: lower case one
 * character at a time, with a final sigma as σ, since ΟΣ and ος name one place.
 */
export function foldLocation(text: string): string {
	return lowerCase(text).replaceAll('ς', 'σ');
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

/**
 * Whether a copy has the filter's status; `unpriced` is an owned copy without a price, and no
 * status is any copy not sold.
 */
function hasStatus(item: CollectionItem, status: CollectionFilter['status']): boolean {
	if (status === 'unpriced') return item.status === 'owned' && item.price_cents === null;
	if (!status) return item.status !== 'sold';
	return item.status === status;
}

/**
 * The copies the filter lists, in their order: of its kind, medium, status and location, whose
 * barcode is the searched code or whose text holds the search, case aside. A search that reads
 * as a barcode still finds a location holding it as typed, as the server does.
 */
export function filterCollection(
	items: readonly CollectionItem[],
	f: CollectionFilter
): CollectionItem[] {
	const term = searchTerm(f.q);
	const words = lowerCase(term);
	const typed = lowerCase(trimSpace(f.q));
	const place = foldLocation(trimSpace(f.location ?? ''));
	return items.filter(
		(item) =>
			(!f.kind || item.kind === f.kind) &&
			(!f.format || item.format === f.format) &&
			hasStatus(item, f.status) &&
			(!place || (item.location != null && foldLocation(item.location) === place)) &&
			(!term ||
				item.barcode === term ||
				searchedText(item).includes(words) ||
				(item.location != null && lowerCase(item.location).includes(typed)))
	);
}

/** Orders text by code point, as Go compares strings, so ties sort as the server sorts them. */
function byCodePoint(a: string, b: string): number {
	const [x, y] = [
		[...a].map((c) => c.codePointAt(0) ?? 0),
		[...b].map((c) => c.codePointAt(0) ?? 0)
	];
	for (let i = 0; i < Math.min(x.length, y.length); i++) {
		if (x[i] !== y[i]) return (x[i] ?? 0) - (y[i] ?? 0);
	}
	return x.length - y.length;
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
		const key = foldLocation(location);
		const place = places.get(key) ?? { copies: 0, spellings: new Map<string, number>() };
		place.copies++;
		place.spellings.set(location, (place.spellings.get(location) ?? 0) + 1);
		places.set(key, place);
	}
	const byUse = (a: [string, number], b: [string, number]) =>
		b[1] - a[1] || byCodePoint(a[0], b[0]);
	return [...places.entries()]
		.sort(([a, x], [b, y]) => y.copies - x.copies || byCodePoint(a, b))
		.map(([, place]) => [...place.spellings.entries()].sort(byUse)[0]?.[0] ?? '');
}
