import { describe, expect, it } from 'vitest';
import {
	dealPrice,
	dealsSummary,
	dismissalPath,
	intervalLabel,
	isUnderLimit,
	keywordPayload,
	keywordsOf,
	lastRunSummary,
	limitComparison,
	marketFor,
	marketLabel,
	marketPath,
	searchedFor,
	segmentLabel,
	timeAgo,
	underLimitCount,
	underLimitLabel,
	wishLimit,
	withoutDismissed,
	type HunterStatus,
	type Keyword,
	type MarketPrice,
	type RunResult
} from './deals';
import type { Format } from './collection';

const status = (patch: Partial<HunterStatus>): HunterStatus => ({
	running: false,
	scheduled: true,
	interval_seconds: 7200,
	telegram: true,
	last_started_at: null,
	last_finished_at: null,
	last_result: null,
	...patch
});

const result = (patch: Partial<RunResult>): RunResult => ({
	wants: 3,
	queries: 6,
	listings: 120,
	matched: 9,
	new: 2,
	alerts: 1,
	sent: 1,
	errors: [],
	...patch
});

describe('keywordPayload', () => {
	it('collapses spaces', () => {
		expect(keywordPayload('reject', '  sama   płyta ')).toEqual({
			ok: true,
			value: { kind: 'reject', phrase: 'sama płyta' }
		});
	});

	it.each([[''], ['  '], ['--']])('rejects %j', (phrase) => {
		const parsed = keywordPayload('accept', phrase);
		expect(!parsed.ok && parsed.issue.field).toBe('phrase');
	});

	it('rejects a phrase over 100 characters', () => {
		const parsed = keywordPayload('reject', 'a'.repeat(101));
		expect(!parsed.ok && parsed.issue.message).toContain('100');
	});
});

describe('keywordsOf', () => {
	it('keeps one kind', () => {
		const keywords: Keyword[] = [
			{ id: 1, kind: 'reject', phrase: 'pudełko', created_at: '' },
			{ id: 2, kind: 'accept', phrase: 'twarda okładka', created_at: '' }
		];
		expect(keywordsOf(keywords, 'accept').map((k) => k.id)).toEqual([2]);
	});
});

describe('intervalLabel', () => {
	it.each([
		[status({}), 'co 2 godz.'],
		[status({ interval_seconds: 2700 }), 'co 45 min'],
		[status({ scheduled: false, interval_seconds: 0 }), '']
	])('%j → %s', (s, label) => {
		expect(intervalLabel(s)).toBe(label);
	});
});

describe('lastRunSummary', () => {
	it('is null before the first run', () => {
		expect(lastRunSummary(null)).toBeNull();
	});

	it('counts what the run did', () => {
		expect(lastRunSummary(result({ errors: ['olx: status 403'] }))).toBe(
			'Pozycji z listy życzeń: 3, nowych ofert: 2, powiadomień: 1, błędów źródeł: 1'
		);
		expect(lastRunSummary(result({}))).not.toContain('błędów');
	});

	it('reports a failed run', () => {
		expect(lastRunSummary(result({ failure: 'context canceled' }))).toBe(
			'Ostatnie wyszukiwanie przerwane: context canceled'
		);
	});
});

describe('limitComparison', () => {
	it.each([
		[8500, 10000, '15\u00a0zł pod limitem'],
		[10000, 10000, 'W limicie'],
		[8450, 10000, '15,50\u00a0zł pod limitem'],
		[10001, 10000, '0,01\u00a0zł ponad limit'],
		[8500, null, 'Brak limitu'],
		[0, 0, 'W limicie']
	] as const)('compares %s grosze against %s', (price, limit, expected) => {
		expect(limitComparison(price, limit)).toBe(expected);
	});
});

describe('underLimitCount', () => {
	it('counts listings at or under the wish limit', () => {
		// One wish limited to 40 zł, listings at 35 zł and 55 zł, one owned-edition listing.
		expect(
			underLimitCount([
				{ price_cents: 3500, max_price_cents: 4000 },
				{ price_cents: 5500, max_price_cents: 4000 },
				{ price_cents: 1000, max_price_cents: null }
			])
		).toBe(1);
	});

	it.each([
		[4000, 4000, true],
		[4001, 4000, false],
		[0, 0, true],
		[100, null, false]
	] as const)('%s grosze against limit %s → %s', (price, limit, expected) => {
		expect(isUnderLimit({ price_cents: price, max_price_cents: limit })).toBe(expected);
	});

	it('is zero without listings', () => {
		expect(underLimitCount([])).toBe(0);
	});
});

describe('underLimitLabel', () => {
	it.each([
		[1, '1 oferta pod limitem'],
		[3, '3 oferty pod limitem'],
		[5, '5 ofert pod limitem'],
		[12, '12 ofert pod limitem'],
		[22, '22 oferty pod limitem']
	] as const)('%s → %s', (count, expected) => {
		expect(underLimitLabel(count)).toBe(expected);
	});
});

describe('marketLabel', () => {
	it.each([
		[4250, 4, 'Rynek ok. 42,50 zł (4 oferty)'],
		[4000, 3, 'Rynek ok. 40 zł (3 oferty)'],
		[4000, 2, 'Rynek ok. 40 zł (2 oferty, mało danych)'],
		[3500, 1, 'Rynek ok. 35 zł (1 oferta, mało danych)'],
		[2500, 12, 'Rynek ok. 25 zł (12 ofert)']
	] as const)('%s grosze from %s listings → %s', (cents, count, expected) => {
		expect(marketLabel({ cents, count })).toBe(expected);
	});

	it('shows nothing without listings', () => {
		expect(marketLabel(null)).toBeNull();
		expect(marketLabel({ cents: 0, count: 0 })).toBeNull();
	});
});

describe('marketFor', () => {
	const market = (titleId: number, format: Format | null): MarketPrice => ({
		title_id: titleId,
		format,
		cents: 4250,
		source: 'olx',
		count: 4,
		trimmed: 1
	});
	const markets = [market(2, 'bluray'), market(2, 'dvd'), market(7, null)];

	it('finds the market of a wish by title and format', () => {
		expect(marketFor(markets, 2, 'dvd')).toBe(markets[1]);
		expect(marketFor(markets, 7, null)).toBe(markets[2]);
	});

	it('is null for another format, a deleted title or before the prices load', () => {
		expect(marketFor(markets, 2, 'uhd')).toBeNull();
		expect(marketFor(markets, 7, 'dvd')).toBeNull();
		expect(marketFor(markets, null, 'dvd')).toBeNull();
		expect(marketFor(null, 2, 'dvd')).toBeNull();
	});

	it('labels each deal card with its wish market', () => {
		const deal = { title_id: 2, format: 'dvd' as const };
		expect(marketLabel(marketFor(markets, deal.title_id, deal.format))).toBe(
			'Rynek ok. 42,50 zł (4 oferty)'
		);
	});
});

describe('marketPath', () => {
	it('asks for every wish, or for every format of one title', () => {
		expect(marketPath()).toBe('/api/valuation/market');
		expect(marketPath(12)).toBe('/api/valuation/market?title_id=12');
	});
});

describe('dealPrice', () => {
	it.each([
		[7900, '79 zł'],
		[7999, '79,99 zł'],
		[7950, '79,50 zł'],
		[0, '0 zł']
	] as const)('%s grosze → %s', (cents, expected) => {
		expect(dealPrice(cents)).toBe(expected);
	});
});

describe('wishLimit', () => {
	it.each([
		[4000, 'limit 40 zł'],
		[3999, 'limit 39,99 zł'],
		[null, 'bez limitu']
	] as const)('%s → %s', (limit, expected) => {
		expect(wishLimit(limit)).toBe(expected);
	});
});

describe('segments', () => {
	it('labels a segment with its count', () => {
		expect(segmentLabel('under', 3)).toBe('Pod limitem · 3');
		expect(segmentLabel('all', 0)).toBe('Wszystkie · 0');
		expect(segmentLabel('sent', 12)).toBe('Wysłane · 12');
	});
});

describe('dismissal', () => {
	it("addresses the listing's dismissal", () => {
		expect(dismissalPath(42)).toBe('/api/deals/42/dismissal');
	});

	it('leaves out the dismissed listings and keeps the order of the rest', () => {
		const listings = [{ id: 3 }, { id: 1 }, { id: 2 }];
		expect(withoutDismissed(listings, new Set([1]))).toEqual([{ id: 3 }, { id: 2 }]);
		expect(withoutDismissed(listings, new Set([9]))).toEqual(listings);
	});

	it('hands back the same list when nothing is dismissed', () => {
		const listings = [{ id: 1 }];
		expect(withoutDismissed(listings, new Set())).toBe(listings);
	});
});

describe('timeAgo', () => {
	const now = new Date('2026-11-19T12:00:00Z');
	it.each([
		['2026-11-19T11:59:30Z', 'przed chwilą'],
		['2026-11-19T12:00:05Z', 'przed chwilą'],
		['2026-11-19T11:55:00Z', '5 min temu'],
		['2026-11-19T10:00:00Z', '2 godz. temu'],
		['2026-11-18T11:00:00Z', 'wczoraj'],
		['2026-11-16T12:00:00Z', '3 dni temu']
	])('%s → %s', (instant, expected) => {
		expect(timeAgo(instant, now)).toBe(expected);
	});
});

describe('dealsSummary', () => {
	const now = new Date('2026-11-19T12:00:00Z');

	it('names the wishes and the last run', () => {
		expect(dealsSummary(4, status({ last_finished_at: '2026-11-19T10:00:00Z' }), now)).toBe(
			'4 na liście życzeń · sprawdzono 2 godz. temu'
		);
	});

	it('leaves out a run that has not finished', () => {
		expect(dealsSummary(1, status({}), now)).toBe('1 na liście życzeń');
	});
});

describe('searchedFor', () => {
	const now = new Date('2026-11-19T12:00:00Z');

	it('names the wishes and the last run', () => {
		expect(searchedFor(4, status({ last_finished_at: '2026-11-19T11:55:00Z' }), now)).toBe(
			'Szukamy ofert dla 4 pozycji z listy życzeń. Ostatnie wyszukiwanie: 5 min temu.'
		);
	});

	it('says when no run finished since the server started', () => {
		expect(searchedFor(1, status({}), now)).toBe(
			'Szukamy ofert dla 1 pozycji z listy życzeń. Od uruchomienia serwera nie było wyszukiwania.'
		);
	});
});
