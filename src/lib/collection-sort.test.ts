import { describe, expect, it } from 'vitest';
import { sortCopies, sortTitles, type CollectionSort } from './collection-sort';

const copies = [
	{ copy_id: 4, created_at_us: 2001, name: 'Żaba', price_cents: 500, purchased_on: '1990-01-01' },
	{ copy_id: 2, created_at_us: 1000, name: 'Zebra', price_cents: null, purchased_on: null },
	{ copy_id: 3, created_at_us: 2000, name: 'Łąka', price_cents: 0, purchased_on: null },
	{ copy_id: 1, created_at_us: 1000, name: 'Lalka', price_cents: 500, purchased_on: '2020-01-01' }
].map((copy) => ({ ...copy, title_id: copy.copy_id, edition_id: copy.copy_id }));

describe('sortCopies', () => {
	it('keeps copies grouped by title and edition for identical names', () => {
		const copy = copies[0];
		const tied = [
			{ ...copy, copy_id: 1, title_id: 1, edition_id: 1 },
			{ ...copy, copy_id: 2, title_id: 2, edition_id: 3 },
			{ ...copy, copy_id: 3, title_id: 1, edition_id: 2 },
			{ ...copy, copy_id: 4, title_id: 1, edition_id: 1 }
		];
		expect(sortCopies(tied, 'title').map(({ copy_id }) => copy_id)).toEqual([1, 4, 3, 2]);
	});

	it.each<[CollectionSort, number[]]>([
		['newest', [4, 3, 1, 2]],
		['title', [1, 3, 2, 4]],
		['priceAsc', [3, 1, 4, 2]],
		['priceDesc', [1, 4, 3, 2]]
	])('sorts by %s with deterministic ties', (sort, expected) => {
		expect(sortCopies(copies, sort).map((copy) => copy.copy_id)).toEqual(expected);
		expect(copies.map((copy) => copy.copy_id)).toEqual([4, 2, 3, 1]);
	});

	const dated = [
		// Bought in 2023 but entered last, from an old receipt.
		{ copy_id: 5, created_at_us: 5000, status: 'owned', purchased_on: '2023-03-01', year: 1999 },
		{ copy_id: 2, created_at_us: 2000, status: 'owned', purchased_on: '2024-06-15', year: null },
		{ copy_id: 6, created_at_us: 6000, status: 'wishlist', purchased_on: '2025-01-01', year: 2010 },
		{ copy_id: 4, created_at_us: 4000, status: 'owned', purchased_on: null, year: 1999 },
		{ copy_id: 3, created_at_us: 3000, status: 'owned', purchased_on: '2024-06-15', year: 2020 },
		{ copy_id: 1, created_at_us: 1000, status: 'wishlist', purchased_on: null, year: null }
	].map((copy) => ({
		...copy,
		status: copy.status as 'owned' | 'wishlist',
		name: `Tytuł ${copy.copy_id}`,
		price_cents: null,
		title_id: copy.copy_id,
		edition_id: copy.copy_id
	}));

	it.each<[CollectionSort, number[]]>([
		['purchased', [2, 3, 5, 1, 4, 6]],
		['yearDesc', [3, 6, 4, 5, 1, 2]],
		['yearAsc', [4, 5, 6, 3, 1, 2]]
	])('sorts by %s with missing values last', (sort, expected) => {
		expect(sortCopies(dated, sort).map((copy) => copy.copy_id)).toEqual(expected);
		expect(dated.map((copy) => copy.copy_id)).toEqual([5, 2, 6, 4, 3, 1]);
	});

	it('orders copies of one year by title, then edition', () => {
		const copy = { ...dated[0], year: 2000 };
		const tied = [
			{ ...copy, copy_id: 1, name: 'Zebra', title_id: 1, edition_id: 1 },
			{ ...copy, copy_id: 2, name: 'Lalka', title_id: 2, edition_id: 3 },
			{ ...copy, copy_id: 3, name: 'Lalka', title_id: 2, edition_id: 2 }
		];
		expect(sortCopies(tied, 'yearDesc').map(({ copy_id }) => copy_id)).toEqual([3, 2, 1]);
		expect(sortCopies(tied, 'yearAsc').map(({ copy_id }) => copy_id)).toEqual([3, 2, 1]);
	});
});

describe('sortTitles', () => {
	const titles = [
		{ id: 3, name: 'Żaba' },
		{ id: 1, name: 'Łąka' },
		{ id: 4, name: 'Lalka' },
		{ id: 2, name: 'Lalka' }
	];

	it.each<[CollectionSort, number[]]>([
		['newest', [4, 3, 2, 1]],
		['title', [2, 4, 1, 3]],
		['priceAsc', [2, 4, 1, 3]],
		['priceDesc', [2, 4, 1, 3]]
	])('sorts by %s without changing the input', (sort, expected) => {
		expect(sortTitles(titles, sort).map((t) => t.id)).toEqual(expected);
		expect(titles.map((t) => t.id)).toEqual([3, 1, 4, 2]);
	});

	const dated = [
		{ id: 1, name: 'Żaba', year: 1990 },
		{ id: 2, name: 'Lalka', year: null },
		{ id: 3, name: 'Łąka', year: 2005 },
		{ id: 4, name: 'Arka', year: 1990 },
		{ id: 5, name: 'Bez roku', year: null }
	];

	it.each<[CollectionSort, number[]]>([
		['yearDesc', [3, 4, 1, 5, 2]],
		['yearAsc', [4, 1, 3, 5, 2]],
		['purchased', [4, 5, 2, 3, 1]]
	])('sorts by %s with titles without a year last', (sort, expected) => {
		expect(sortTitles(dated, sort).map((t) => t.id)).toEqual(expected);
		expect(dated.map((t) => t.id)).toEqual([1, 2, 3, 4, 5]);
	});
});
