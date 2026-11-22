import { describe, expect, it } from 'vitest';
import {
	acquiredShare,
	bars,
	collectionGrowth,
	datedLater,
	growthPaths,
	joinedOn,
	lastMonths,
	monthLabel,
	monthTick,
	monthlySpending,
	percents,
	roundZl,
	warsawDay,
	type StatsItem
} from './stats';

/** Microseconds since the epoch of an instant, as `created_at_us` carries it. */
const us = (iso: string) => Date.parse(iso) * 1000;

function copy(over: Partial<StatsItem> = {}): StatsItem {
	return {
		status: 'owned',
		price_cents: null,
		purchased_on: null,
		acquired_as: null,
		created_at_us: us('2025-06-10T12:00:00Z'),
		...over
	};
}

const now = new Date('2026-02-15T12:00:00Z');

describe('warsawDay', () => {
	it.each([
		['2026-01-31T22:59:59Z', '2026-01-31'],
		['2026-01-31T23:00:00Z', '2026-02-01'],
		['2026-07-31T21:59:59Z', '2026-07-31'],
		['2026-07-31T22:00:00Z', '2026-08-01']
	])('%s is %s in Poland', (at, day) => {
		expect(warsawDay(new Date(at))).toBe(day);
	});
});

describe('lastMonths', () => {
	it('ends with the current month in Poland and crosses the year', () => {
		expect(lastMonths(now)).toEqual([
			'2025-03',
			'2025-04',
			'2025-05',
			'2025-06',
			'2025-07',
			'2025-08',
			'2025-09',
			'2025-10',
			'2025-11',
			'2025-12',
			'2026-01',
			'2026-02'
		]);
	});

	it('takes the month of the Polish day on New Year’s Eve night', () => {
		expect(lastMonths(new Date('2025-12-31T23:30:00Z'), 2)).toEqual(['2025-12', '2026-01']);
	});
});

describe('monthlySpending', () => {
	it('sums prices per purchase month, counts unpriced and undated copies apart', () => {
		const items = [
			copy({ purchased_on: '2026-01-31', price_cents: 4999 }),
			copy({ purchased_on: '2026-02-01', price_cents: 1500 }),
			copy({ purchased_on: '2026-02-01', price_cents: null }),
			copy({ purchased_on: null, price_cents: 2000 }),
			copy({ purchased_on: null, price_cents: null })
		];
		const { months, undated } = monthlySpending(items, now);
		expect(months).toHaveLength(12);
		expect(months.at(-2)).toEqual({ month: '2026-01', cents: 4999, copies: 1, unpriced: 0 });
		expect(months.at(-1)).toEqual({ month: '2026-02', cents: 1500, copies: 2, unpriced: 1 });
		expect(months.slice(0, 10).every((m) => m.copies === 0 && m.cents === 0)).toBe(true);
		expect(undated).toEqual({ cents: 2000, copies: 2, unpriced: 1 });
	});

	it('leaves out copies bought before the window and every copy not owned', () => {
		const items = [
			copy({ purchased_on: '2025-02-28', price_cents: 100 }),
			copy({ purchased_on: '2025-03-01', price_cents: 200 }),
			copy({ status: 'wishlist', purchased_on: null, price_cents: null }),
			copy({ status: 'ordered', purchased_on: '2026-02-10', price_cents: 300 }),
			copy({ status: 'sold', purchased_on: '2026-02-10', price_cents: 400 })
		];
		const { months, undated } = monthlySpending(items, now);
		expect(months[0]).toEqual({ month: '2025-03', cents: 200, copies: 1, unpriced: 0 });
		expect(months.reduce((sum, m) => sum + m.copies, 0)).toBe(1);
		expect(undated).toEqual({ cents: 0, copies: 0, unpriced: 0 });
	});
});

describe('joinedOn', () => {
	it('takes the purchase date, else the Polish day the copy was added', () => {
		expect(
			joinedOn({ purchased_on: '2024-05-05', created_at_us: us('2026-01-01T00:00:00Z') })
		).toBe('2024-05-05');
		expect(joinedOn({ purchased_on: null, created_at_us: us('2026-01-31T23:30:00Z') })).toBe(
			'2026-02-01'
		);
	});
});

describe('collectionGrowth', () => {
	it('counts owned copies at the end of each month, undated ones from when they were added', () => {
		const items = [
			copy({ purchased_on: '2020-01-01' }),
			copy({ purchased_on: '2025-12-31' }),
			copy({ purchased_on: null, created_at_us: us('2026-01-31T23:30:00Z') }),
			copy({ purchased_on: null, created_at_us: us('2025-03-15T10:00:00Z') }),
			copy({ status: 'wishlist', created_at_us: us('2025-03-15T10:00:00Z') }),
			copy({ status: 'sold', purchased_on: '2025-04-01' })
		];
		const growth = collectionGrowth(items, now);
		expect(growth.map((g) => g.copies)).toEqual([2, 2, 2, 2, 2, 2, 2, 2, 2, 3, 3, 4]);
		expect(growth.at(-1)).toEqual({ month: '2026-02', copies: 4 });
	});

	it('counts a copy dated after this month in no month', () => {
		const items = [copy({ purchased_on: '2026-05-01' }), copy({ purchased_on: '2026-02-28' })];
		expect(collectionGrowth(items, now).at(-1)).toEqual({ month: '2026-02', copies: 1 });
		expect(collectionGrowth(items, new Date('2026-02-28T23:01:00Z')).at(-1)).toEqual({
			month: '2026-03',
			copies: 1
		});
		expect(datedLater(items, now)).toBe(1);
		expect(datedLater(items, new Date('2026-04-30T22:00:00Z'))).toBe(0);
	});

	it('is flat at 0 without owned copies', () => {
		expect(collectionGrowth([copy({ status: 'wishlist' })], now).every((g) => g.copies === 0)).toBe(
			true
		);
	});
});

describe('percents', () => {
	it.each([
		[
			[1, 1, 1],
			[34, 33, 33]
		],
		[
			[2, 1, 0],
			[67, 33, 0]
		],
		[
			[0, 0, 0],
			[0, 0, 0]
		],
		[
			[5, 0, 0],
			[100, 0, 0]
		],
		[
			[1, 2, 2],
			[20, 40, 40]
		]
	])('%j → %j', (values, out) => {
		expect(percents(values)).toEqual(out);
		if (values.some((v) => v > 0)) expect(out.reduce((s, v) => s + v, 0)).toBe(100);
	});
});

describe('acquiredShare', () => {
	it('splits owned copies by count and by money paid', () => {
		const items = [
			copy({ acquired_as: 'new', price_cents: 6000 }),
			copy({ acquired_as: 'used', price_cents: 2000 }),
			copy({ acquired_as: 'used', price_cents: null }),
			copy({ acquired_as: null, price_cents: 2000 }),
			copy({ status: 'wishlist', acquired_as: 'new', price_cents: 9999 })
		];
		expect(acquiredShare(items)).toEqual([
			{ key: 'new', copies: 1, priced: 1, cents: 6000, copyPercent: 25, centsPercent: 60 },
			{ key: 'used', copies: 2, priced: 1, cents: 2000, copyPercent: 50, centsPercent: 20 },
			{ key: 'unknown', copies: 1, priced: 1, cents: 2000, copyPercent: 25, centsPercent: 20 }
		]);
	});

	it('puts every copy under Nie zapisano when nothing is recorded', () => {
		const share = acquiredShare([copy(), copy()]);
		expect(share.map((s) => [s.key, s.copies, s.copyPercent, s.centsPercent])).toEqual([
			['new', 0, 0, null],
			['used', 0, 0, null],
			['unknown', 2, 100, null]
		]);
	});

	it('tells copies priced at 0 zł from unpriced ones', () => {
		const share = acquiredShare([copy({ acquired_as: 'used', price_cents: 0 }), copy()]);
		expect(share.map((s) => [s.key, s.priced, s.cents, s.centsPercent])).toEqual([
			['new', 0, 0, null],
			['used', 1, 0, null],
			['unknown', 0, 0, null]
		]);
	});

	it('has no shares without owned copies', () => {
		expect(acquiredShare([]).every((s) => s.copies === 0 && s.copyPercent === 0)).toBe(true);
	});
});

describe('labels', () => {
	it('names months in Polish', () => {
		expect(monthLabel('2026-01')).toBe('styczeń 2026');
		expect(monthTick('2026-02')).toBe('lut');
		expect(roundZl(123456)).toBe('1235 zł');
	});
});

describe('bars', () => {
	it('scales bars to the largest value and gives 0 no height', () => {
		expect(bars([0, 50, 100], 300, 100, 0)).toEqual([
			{ x: 0, y: 100, width: 100, height: 0 },
			{ x: 100, y: 50, width: 100, height: 50 },
			{ x: 200, y: 0, width: 100, height: 100 }
		]);
	});

	it('draws nothing tall when every value is 0', () => {
		expect(bars([0, 0], 100, 50).every((b) => b.height === 0)).toBe(true);
	});
});

describe('growthPaths', () => {
	it('draws a line through each month and the area under it', () => {
		expect(growthPaths([0, 2], 200, 50, 5)).toEqual({
			line: 'M50,45L150,5',
			area: 'M50,45L150,5L150,50L50,50Z'
		});
	});

	it('lays a flat line on the floor without copies', () => {
		expect(growthPaths([0, 0], 200, 50, 5)?.line).toBe('M50,45L150,45');
	});

	it('draws nothing without months', () => {
		expect(growthPaths([], 200, 50)).toBeNull();
	});
});
