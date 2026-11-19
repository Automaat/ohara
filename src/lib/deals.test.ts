import { describe, expect, it } from 'vitest';
import {
	dealPrice,
	dealsSummary,
	intervalLabel,
	isSent,
	isUnderLimit,
	keywordPayload,
	keywordsOf,
	lastRunSummary,
	limitComparison,
	searchedFor,
	segmentLabel,
	timeAgo,
	underLimitCount,
	underLimitLabel,
	wishLimit,
	type HunterStatus,
	type Keyword,
	type RunResult
} from './deals';

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
	const matched = [
		{ price_cents: 3500, max_price_cents: 4000, alert: 'sent' },
		{ price_cents: 5500, max_price_cents: 4000, alert: 'none' },
		{ price_cents: 2000, max_price_cents: null, alert: 'sent' }
	] as const;

	it('keeps a listing alerted before its wish lost the limit out of Wysłane', () => {
		expect(matched.filter(isSent)).toEqual([matched[0]]);
		expect(matched.filter(isUnderLimit)).toEqual([matched[0]]);
	});

	it('labels a segment with its count', () => {
		expect(segmentLabel('under', 3)).toBe('Pod limitem · 3');
		expect(segmentLabel('all', 0)).toBe('Wszystkie · 0');
		expect(segmentLabel('sent', 12)).toBe('Wysłane · 12');
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
