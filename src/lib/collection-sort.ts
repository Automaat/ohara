import { options, type CollectionItem, type Title } from './collection';

export const collectionSortLabels = {
	newest: 'Ostatnio dodane',
	purchased: 'Ostatnio kupione',
	title: 'Tytuł A–Z',
	yearDesc: 'Rok malejąco',
	yearAsc: 'Rok rosnąco',
	priceAsc: 'Cena zakupu rosnąco',
	priceDesc: 'Cena zakupu malejąco',
	ratingDesc: 'Ocena malejąco'
};

export type CollectionSort = keyof typeof collectionSortLabels;
export const collectionSortOptions = options(collectionSortLabels);

type SortableCopy = Pick<
	CollectionItem,
	'copy_id' | 'created_at_us' | 'name' | 'price_cents' | 'title_id' | 'edition_id'
> &
	Partial<Pick<CollectionItem, 'status' | 'purchased_on' | 'year' | 'rating'>>;

const titleOrder = new Intl.Collator('pl-PL', { sensitivity: 'base' });

/** Orders two values, a missing one last whatever the direction. */
function compareMissingLast<V extends number | string>(
	a: V | null | undefined,
	b: V | null | undefined,
	descending: boolean
): number {
	if (a === b) return 0;
	if (a == null) return 1;
	if (b == null) return -1;
	if (a < b) return descending ? 1 : -1;
	return descending ? -1 : 1;
}

/** A wish was never bought, so it has no purchase date whatever the row holds. */
function purchaseDate(copy: SortableCopy): string | null {
	return copy.status === 'wishlist' ? null : (copy.purchased_on ?? null);
}

function compareTitles(a: SortableCopy, b: SortableCopy): number {
	return (
		titleOrder.compare(a.name, b.name) || a.title_id - b.title_id || a.edition_id - b.edition_id
	);
}

export function sortCopies<T extends SortableCopy>(items: readonly T[], sort: CollectionSort): T[] {
	return [...items].sort((a, b) => {
		const tie = a.copy_id - b.copy_id;
		switch (sort) {
			case 'newest':
				return b.created_at_us - a.created_at_us || tie;
			case 'purchased':
				return compareMissingLast(purchaseDate(a), purchaseDate(b), true) || tie;
			case 'title':
				return compareTitles(a, b) || tie;
			case 'yearDesc':
			case 'yearAsc':
				return (
					compareMissingLast(a.year, b.year, sort === 'yearDesc') || compareTitles(a, b) || tie
				);
			case 'priceAsc':
			case 'priceDesc':
				return compareMissingLast(a.price_cents, b.price_cents, sort === 'priceDesc') || tie;
			case 'ratingDesc':
				return (
					compareMissingLast(a.rating ?? null, b.rating ?? null, true) || compareTitles(a, b) || tie
				);
		}
	});
}

/**
 * Titles in a collection order: newest first by id, by year or by rating with titles without one
 * last, else A–Z. A title without copies has no price or purchase date, so those orders fall back
 * to A–Z.
 */
export function sortTitles<
	T extends Pick<Title, 'id' | 'name'> & Partial<Pick<Title, 'year' | 'rating'>>
>(items: readonly T[], sort: CollectionSort): T[] {
	return [...items].sort((a, b) => {
		if (sort === 'newest') return b.id - a.id;
		let first = 0;
		if (sort === 'yearDesc' || sort === 'yearAsc') {
			first = compareMissingLast(a.year, b.year, sort === 'yearDesc');
		} else if (sort === 'ratingDesc') {
			first = compareMissingLast(a.rating ?? null, b.rating ?? null, true);
		}
		return first || titleOrder.compare(a.name, b.name) || a.id - b.id;
	});
}
