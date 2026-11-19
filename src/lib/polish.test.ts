import { describe, expect, it } from 'vitest';
import { copyCount, editionCount, purchaseDate, queuedCodes, resultCount } from './polish';

describe('Polish result counts', () => {
	it.each([
		[0, '0 wyników'],
		[1, '1 wynik'],
		[2, '2 wyniki'],
		[5, '5 wyników'],
		[12, '12 wyników'],
		[22, '22 wyniki']
	])('formats %d as %s', (count, expected) => {
		expect(resultCount(count)).toBe(expected);
	});
});

describe('Polish edition and copy counts', () => {
	it.each([
		[1, '1 wydanie', '1 egzemplarz'],
		[3, '3 wydania', '3 egzemplarze'],
		[5, '5 wydań', '5 egzemplarzy'],
		[12, '12 wydań', '12 egzemplarzy'],
		[24, '24 wydania', '24 egzemplarze']
	])('formats %d', (count, editions, copies) => {
		expect(editionCount(count)).toBe(editions);
		expect(copyCount(count)).toBe(copies);
	});
});

describe('Polish scan queue counts', () => {
	it.each([
		[1, '1 kod czeka na uzupełnienie'],
		[2, '2 kody czekają na uzupełnienie'],
		[5, '5 kodów czeka na uzupełnienie'],
		[12, '12 kodów czeka na uzupełnienie'],
		[22, '22 kody czekają na uzupełnienie'],
		[1000, '1000 kodów czeka na uzupełnienie']
	])('formats %d as %s', (count, expected) => {
		expect(queuedCodes(count)).toBe(expected);
	});
});

describe('Polish purchase dates', () => {
	it('keeps the calendar date of a purchase', () => {
		expect(purchaseDate('2026-11-01')).toBe('1 lis 2026');
	});

	it('leaves dates blank when no purchase date was recorded', () => {
		expect(purchaseDate(null)).toBe('');
	});
});
