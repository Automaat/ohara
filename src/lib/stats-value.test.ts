import { describe, expect, it } from 'vitest';
import type { Estimate } from './api.gen';
import {
	groupNotes,
	groupValue,
	valueByFormat,
	valueByKind,
	valueShare,
	type ValueGroup,
	type ValueItem
} from './stats-value';

const pln = new Intl.NumberFormat('pl-PL', { style: 'currency', currency: 'PLN' });

function olx(cents: number, count = 4): Estimate {
	return { cents, source: 'olx', count, trimmed: 0 };
}

function copy(over: Partial<ValueItem> = {}): ValueItem {
	return {
		kind: 'movie',
		format: 'dvd',
		price_cents: null,
		mid: null,
		low_confidence: true,
		...over
	};
}

/** One copy of every kind and format, an unvalued one, an unpriced one and two of low confidence. */
const shelf: ValueItem[] = [
	copy({ format: 'dvd', price_cents: 2000, mid: olx(1500), low_confidence: false }),
	copy({ format: 'dvd', price_cents: 1000, mid: olx(500, 2), low_confidence: true }),
	copy({ format: 'bluray', price_cents: 4000, mid: null, low_confidence: true }),
	copy({ format: 'uhd', price_cents: null, mid: olx(9000), low_confidence: false }),
	copy({ kind: 'book', format: null, price_cents: 3000, mid: olx(2500, 1), low_confidence: true }),
	copy({ kind: 'book', format: null, price_cents: 0, mid: olx(700), low_confidence: false }),
	copy({
		kind: 'music',
		format: 'vinyl',
		price_cents: 8000,
		mid: { cents: 12000, source: 'discogs', count: 1, trimmed: 0 },
		low_confidence: false,
		discogs_release_id: 456
	}),
	copy({
		kind: 'music',
		format: 'cd',
		price_cents: 3500,
		mid: olx(1000),
		low_confidence: false,
		discogs_release_id: 789
	}),
	copy({ kind: 'music', format: 'cassette', price_cents: null, mid: null, low_confidence: true })
];

describe('valueByKind', () => {
	it('sums the used value and the price paid per kind, the most valuable first', () => {
		expect(valueByKind(shelf)).toEqual([
			{
				key: 'music',
				label: 'Muzyka',
				copies: 3,
				midCents: 13000,
				unvalued: 1,
				lowConfidence: 0,
				paidCents: 11500,
				unpriced: 1,
				discogsReleases: [456]
			},
			{
				key: 'movie',
				label: 'Filmy',
				copies: 4,
				midCents: 11000,
				unvalued: 1,
				lowConfidence: 1,
				paidCents: 7000,
				unpriced: 1,
				discogsReleases: []
			},
			{
				key: 'book',
				label: 'Książki',
				copies: 2,
				midCents: 3200,
				unvalued: 0,
				lowConfidence: 1,
				paidCents: 3000,
				unpriced: 0,
				discogsReleases: []
			}
		]);
	});

	it('leaves out kinds without copies and counts an unvalued copy of low confidence once', () => {
		const groups = valueByKind([copy({ mid: null, low_confidence: true })]);
		expect(groups).toHaveLength(1);
		expect(groups[0]).toMatchObject({ key: 'movie', copies: 1, unvalued: 1, lowConfidence: 0 });
	});

	it('keeps the kinds in their order when no copy has a value', () => {
		const groups = valueByKind([
			copy({ kind: 'music', format: 'cd' }),
			copy({ kind: 'book', format: null }),
			copy()
		]);
		expect(groups.map((g) => g.key)).toEqual(['movie', 'book', 'music']);
	});

	it('keeps TV series apart from films by kind and with them by format', () => {
		const items = [
			copy({ format: 'dvd', mid: olx(1000) }),
			copy({ kind: 'tv', format: 'dvd', mid: olx(3000) }),
			copy({ kind: 'tv', format: 'bluray', mid: null })
		];
		expect(valueByKind(items).map((g) => [g.label, g.midCents, g.copies, g.unvalued])).toEqual([
			['Seriale', 3000, 2, 1],
			['Filmy', 1000, 1, 0]
		]);
		expect(valueByFormat(items).map((g) => [g.label, g.midCents, g.copies])).toEqual([
			['DVD', 4000, 2],
			['Blu-ray', 0, 1]
		]);
	});

	it('is empty without copies', () => {
		expect(valueByKind([])).toEqual([]);
	});
});

describe('valueByFormat', () => {
	it('groups movies and albums by format and books as one group', () => {
		const groups = valueByFormat(shelf);
		expect(groups.map((g) => [g.label, g.midCents, g.copies])).toEqual([
			['Winyl', 12000, 1],
			['4K UHD', 9000, 1],
			['Książki', 3200, 2],
			['DVD', 2000, 2],
			['CD', 1000, 1],
			['Blu-ray', 0, 1],
			['Kaseta', 0, 1]
		]);
	});

	it('counts the copies of low confidence, unvalued and unpriced ones per format', () => {
		const byKey = new Map(valueByFormat(shelf).map((g) => [g.key, g]));
		expect(byKey.get('dvd')).toMatchObject({
			lowConfidence: 1,
			unvalued: 0,
			paidCents: 3000,
			unpriced: 0
		});
		expect(byKey.get('book')).toMatchObject({ lowConfidence: 1, paidCents: 3000, unpriced: 0 });
		expect(byKey.get('uhd')).toMatchObject({ unpriced: 1, paidCents: 0 });
		expect(byKey.get('bluray')).toMatchObject({ unvalued: 1, lowConfidence: 0 });
		expect(byKey.get('vinyl')).toMatchObject({ lowConfidence: 0 });
	});

	it('puts a movie without a format apart from books', () => {
		const groups = valueByFormat([copy({ format: null, mid: olx(100) })]);
		expect(groups.map((g) => [g.key, g.label])).toEqual([['none', 'Bez nośnika']]);
	});
});

describe('Discogs releases', () => {
	const discogs = (cents: number): Estimate => ({ cents, source: 'discogs', count: 1, trimmed: 0 });

	it('names each release whose suggestion priced a copy once, for the credit', () => {
		const groups = valueByFormat([
			copy({ kind: 'music', format: 'vinyl', mid: discogs(5000), discogs_release_id: 1 }),
			copy({ kind: 'music', format: 'vinyl', mid: discogs(5000), discogs_release_id: 1 }),
			copy({ kind: 'music', format: 'vinyl', mid: discogs(7000), discogs_release_id: 2 }),
			copy({ kind: 'music', format: 'vinyl', mid: olx(900), discogs_release_id: 3 }),
			copy({ kind: 'music', format: 'vinyl', mid: null, discogs_release_id: 4 }),
			copy({ kind: 'music', format: 'cd', mid: discogs(100), discogs_release_id: 0 }),
			copy({ kind: 'music', format: 'cd', mid: discogs(100), discogs_release_id: null })
		]);
		expect(groups.map((g) => [g.key, g.discogsReleases])).toEqual([
			['vinyl', [1, 2]],
			['cd', []]
		]);
	});
});

describe('valueShare', () => {
	it('measures each group against the most valuable one', () => {
		const groups = valueByFormat(shelf);
		expect(groups.map((g) => valueShare(g, groups))).toEqual([100, 75, 27, 17, 8, 0, 0]);
	});

	it('is 0 when no group has a value', () => {
		const groups = valueByKind([copy()]);
		expect(valueShare(groups[0], groups)).toBe(0);
	});
});

function group(over: Partial<ValueGroup> = {}): ValueGroup {
	return {
		key: 'dvd',
		label: 'DVD',
		copies: 1,
		midCents: 0,
		unvalued: 0,
		lowConfidence: 0,
		paidCents: 0,
		unpriced: 0,
		discogsReleases: [],
		...over
	};
}

describe('groupValue', () => {
	it('names the summed used value, 0 zł included', () => {
		expect(groupValue(group({ midCents: 123456 }))).toBe(pln.format(1234.56));
		expect(groupValue(group({ copies: 2, unvalued: 1 }))).toBe(pln.format(0));
	});

	it('says no copy has a used value', () => {
		expect(groupValue(group({ copies: 2, unvalued: 2 }))).toBe('brak wyceny');
	});
});

describe('groupNotes', () => {
	it('names the copies and the price paid', () => {
		expect(groupNotes(group({ copies: 2, paidCents: 4999 }))).toEqual([
			'2 egzemplarze',
			`zapłacono ${pln.format(49.99)}`
		]);
	});

	it('names the unpriced, unvalued and low-confidence copies', () => {
		expect(
			groupNotes(group({ copies: 5, paidCents: 1000, unpriced: 2, unvalued: 1, lowConfidence: 3 }))
		).toEqual([
			'5 egzemplarzy',
			`zapłacono ${pln.format(10)} (bez ceny: 2)`,
			'bez wyceny: 1',
			'mało danych: 3'
		]);
	});
});
