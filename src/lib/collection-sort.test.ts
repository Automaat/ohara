import { describe, expect, it } from 'vitest';
import { sortCopies, type CollectionSort } from './collection-sort';

const copies = [
	{ copy_id: 4, created_at_us: 2001, name: 'Żaba', price_cents: 500, purchased_on: '1990-01-01' },
	{ copy_id: 2, created_at_us: 1000, name: 'Zebra', price_cents: null, purchased_on: null },
	{ copy_id: 3, created_at_us: 2000, name: 'Łąka', price_cents: 0, purchased_on: null },
	{ copy_id: 1, created_at_us: 1000, name: 'Lalka', price_cents: 500, purchased_on: '2020-01-01' }
];

describe('sortCopies', () => {
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
