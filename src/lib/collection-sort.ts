import { options, type CollectionItem, type Title } from './collection';

export const collectionSortLabels = {
	newest: 'Ostatnio dodane',
	title: 'Tytuł A–Z',
	priceAsc: 'Cena zakupu rosnąco',
	priceDesc: 'Cena zakupu malejąco'
};

export type CollectionSort = keyof typeof collectionSortLabels;
export const collectionSortOptions = options(collectionSortLabels);

type SortableCopy = Pick<
	CollectionItem,
	'copy_id' | 'created_at_us' | 'name' | 'price_cents' | 'title_id' | 'edition_id'
>;

const titleOrder = new Intl.Collator('pl-PL', { sensitivity: 'base' });

function comparePrice(a: number | null, b: number | null, descending: boolean): number {
	if (a === b) return 0;
	if (a === null) return 1;
	if (b === null) return -1;
	return descending ? b - a : a - b;
}

export function sortCopies<T extends SortableCopy>(items: readonly T[], sort: CollectionSort): T[] {
	return [...items].sort((a, b) => {
		const tie = a.copy_id - b.copy_id;
		switch (sort) {
			case 'newest':
				return b.created_at_us - a.created_at_us || tie;
			case 'title':
				return (
					titleOrder.compare(a.name, b.name) ||
					a.title_id - b.title_id ||
					a.edition_id - b.edition_id ||
					tie
				);
			case 'priceAsc':
			case 'priceDesc':
				return comparePrice(a.price_cents, b.price_cents, sort === 'priceDesc') || tie;
		}
	});
}

/**
 * Titles in a collection order: newest first by id, else A–Z. A title without copies has no price,
 * so both price orders tie and fall back to A–Z.
 */
export function sortTitles<T extends Pick<Title, 'id' | 'name'>>(
	items: readonly T[],
	sort: CollectionSort
): T[] {
	return [...items].sort((a, b) =>
		sort === 'newest' ? b.id - a.id : titleOrder.compare(a.name, b.name) || a.id - b.id
	);
}
