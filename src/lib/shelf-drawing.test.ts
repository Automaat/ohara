import { describe, expect, it } from 'vitest';
import { drawnCount, firstTiles, tilesPerStep } from './shelf-drawing';

const keys = (from: number, to: number) => Array.from({ length: to - from }, (_, i) => from + i);

describe('drawnCount', () => {
	it.each([
		{ name: 'a first draw stops at the budget', rows: keys(0, 2000), drawn: [], want: 60 },
		{ name: 'a short list is drawn whole', rows: keys(0, 10), drawn: [], want: 10 },
		{ name: 'no rows', rows: [], drawn: [1, 2], want: 0 },
		{
			name: 'a refresh keeps every tile drawn',
			rows: keys(0, 2000),
			drawn: keys(0, 2000),
			want: 2000
		},
		{
			name: 'a refresh while drawing adds a budget past the drawn part',
			rows: keys(0, 2000),
			drawn: keys(0, 300),
			want: 360
		},
		{
			name: 'a narrower filter draws its rows that were drawn',
			rows: keys(0, 2000).filter((k) => k % 3 === 0),
			drawn: keys(0, 2000),
			want: 667
		},
		{
			name: 'a wider filter keeps its drawn rows and adds at most the budget',
			rows: keys(0, 2000),
			drawn: keys(0, 2000).filter((k) => k % 3 === 0),
			want: 91
		},
		{
			name: 'a new order counts only the tiles it creates',
			rows: keys(0, 2000).reverse(),
			drawn: keys(1000, 2000),
			want: 1060
		}
	])('$name', ({ rows, drawn, want }) => {
		expect(drawnCount(rows, new Set<number>(drawn), firstTiles)).toBe(want);
	});

	it('takes the budget it is given', () => {
		expect(drawnCount(keys(0, 2000), new Set<number>(), tilesPerStep)).toBe(tilesPerStep);
		expect(drawnCount(keys(0, 5), new Set<number>(), 0)).toBe(0);
	});
});
