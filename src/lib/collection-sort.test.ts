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
});
