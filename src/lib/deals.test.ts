import { describe, expect, it } from 'vitest';
import {
	intervalLabel,
	isUnderLimit,
	keywordPayload,
	keywordsOf,
	lastRunSummary,
	limitComparison,
	underLimitCount,
	underLimitLabel,
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
		[8500, 10000, '15\u00a0zł poniżej limitu'],
		[10000, 10000, 'W limicie'],
		[8450, 10000, '15,50\u00a0zł poniżej limitu'],
		[10001, 10000, '0,01\u00a0zł powyżej limitu'],
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
