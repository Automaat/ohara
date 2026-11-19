/**
 * What the collection last listed, kept in memory for the rest of the visit. Going back to the
 * collection draws it at once while the collection reloads, so the scroll position comes back
 * and a cover carried from a title lands on its tile however slowly the collection answers. A
 * title opened from the collection draws its hero from it while the title loads.
 */
import type { CollectionItem, CollectionView, Kind, Title, Totals } from '#lib/collection.js';

/** The whole collection's counts, whatever the filter. */
export type Overview = { totals: Totals; kinds: Record<Kind, number> };

/**
 * What the collection showed for the filter of `url`: the copies or the titles without copies.
 * `pending` marks rows the reader left on screen while the answer for `url` was still loading.
 */
export type Listing = ({ url: string; view: CollectionView } | { url: string; titles: Title[] }) & {
	pending?: boolean;
};

/** What the collection shows of a title: enough for its hero before the title loads. */
export type TitlePreview = Pick<
	Title,
	'kind' | 'name' | 'original_title' | 'polish_title' | 'year' | 'authors' | 'cover_url'
>;

let listing: Listing | null = null;
let overview: Overview | null = null;

/** Keeps the listing the collection just loaded in place of the one before. */
export function keepListing(next: Listing): void {
	listing = next;
}

/** The kept listing for `url`, or null when the collection last listed something else. */
export function keptListing(url: string): Listing | null {
	return listing?.url === url ? listing : null;
}

/** Keeps the collection's counts the collection just loaded. */
export function keepOverview(next: Overview): void {
	overview = next;
}

/** The collection's counts as last loaded, or null before they first load. */
export function keptOverview(): Overview | null {
	return overview;
}

function preview(shown: CollectionItem | Title): TitlePreview {
	return {
		kind: shown.kind,
		name: shown.name,
		original_title: shown.original_title,
		polish_title: shown.polish_title,
		year: shown.year,
		authors: shown.authors ?? [],
		cover_url: shown.cover_url
	};
}

/**
 * The title as the kept listing shows it: the tile of the copy with id `key` when the listing
 * holds it, else the title's first tile. Null when the listing does not hold the title.
 */
export function titlePreview(titleID: number, key: string | null): TitlePreview | null {
	if (!listing) return null;
	if ('titles' in listing) {
		const t = listing.titles.find((title) => title.id === titleID);
		return t ? preview(t) : null;
	}
	const tiles = listing.view.items.filter((item) => item.title_id === titleID);
	const tile = tiles.find((item) => String(item.copy_id) === key) ?? tiles[0];
	return tile ? preview(tile) : null;
}
