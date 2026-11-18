import { describe, expect, it } from 'vitest';
import {
	boundTotal,
	dayTimestamp,
	estimateSource,
	estimateValue,
	formatDay,
	formatZl,
	historySeries,
	intervalLabel,
	lastRunSummary,
	offers,
	type HistoryPoint,
	type RunResult,
	type RunnerStatus
} from './valuation';

const nbsp = (s: string) => s.replace(/[  ]/g, ' ');

describe('offers', () => {
	it.each([
		[0, '0 ofert'],
		[1, '1 oferta'],
		[2, '2 oferty'],
		[4, '4 oferty'],
		[5, '5 ofert'],
		[12, '12 ofert'],
		[14, '14 ofert'],
		[22, '22 oferty'],
		[25, '25 ofert'],
		[104, '104 oferty']
	])('%i → %s', (count, want) => {
		expect(offers(count)).toBe(want);
	});
});

describe('estimates', () => {
	it('shows a dash and no source without prices', () => {
		expect(estimateValue(null)).toBe('—');
		expect(estimateSource('mid', null)).toBe('brak danych');
	});

	it('formats the amount', () => {
		expect(nbsp(estimateValue({ cents: 2550, source: 'olx', count: 3, trimmed: 0 }))).toBe(
			'25,50 zł'
		);
	});

	it('names the source and count of a used or new price', () => {
		expect(estimateSource('mid', { cents: 2550, source: 'olx', count: 5, trimmed: 2 })).toBe(
			'OLX · 5 ofert (2 odrzucone)'
		);
		expect(estimateSource('ceiling', { cents: 4999, source: 'ceneo', count: 1, trimmed: 0 })).toBe(
			'Ceneo · 1 oferta'
		);
		expect(estimateSource('mid', { cents: 2600, source: 'olx+vinted', count: 3, trimmed: 0 })).toBe(
			'OLX i Vinted · 3 oferty'
		);
	});

	it('names the Discogs price behind each bound of an album', () => {
		const e = { cents: 5313, source: 'discogs' as const, count: 1, trimmed: 0 };
		expect(estimateSource('floor', e)).toBe('Discogs · najtańsza oferta');
		expect(estimateSource('mid', e)).toBe('Discogs · sugestia VG+');
		expect(estimateSource('ceiling', e)).toBe('Discogs · sugestia Mint');
	});

	it('says when the buy-back shop does not buy the book', () => {
		expect(estimateSource('floor', { cents: 0, source: 'skupszop', count: 1, trimmed: 0 })).toBe(
			'SkupSzop nie skupuje'
		);
		expect(estimateSource('floor', { cents: 537, source: 'skupszop', count: 1, trimmed: 0 })).toBe(
			'SkupSzop'
		);
	});
});

describe('boundTotal', () => {
	it.each([
		[{ cents: 0, copies: 0 }, 0, 'Brak wyceny (0 z 0 egz.)'],
		[{ cents: 0, copies: 0 }, 4, 'Brak wyceny (0 z 4 egz.)'],
		[{ cents: 0, copies: 1 }, 4, '0,00 zł (1 z 4 egz.)'],
		[{ cents: 537, copies: 1 }, 3, '5,37 zł (1 z 3 egz.)'],
		[{ cents: 1234500, copies: 3 }, 3, '12 345,00 zł (3 z 3 egz.)']
	])('formats %o over %i copies as %s', (bound, copies, expected) => {
		expect(nbsp(boundTotal(bound, copies))).toBe(expected);
	});
});

const status = (patch: Partial<RunnerStatus>): RunnerStatus => ({
	running: false,
	scheduled: true,
	interval_seconds: 86400,
	last_started_at: null,
	last_finished_at: null,
	last_result: null,
	...patch
});

describe('intervalLabel', () => {
	it.each([
		[status({}), 'codziennie'],
		[status({ interval_seconds: 7 * 86400 }), 'co 7 dni'],
		[status({ interval_seconds: 12 * 3600 }), 'co 12 godz.'],
		[status({ scheduled: false }), ''],
		[status({ interval_seconds: 0 }), '']
	])('%o → %s', (s, want) => {
		expect(intervalLabel(s)).toBe(want);
	});
});

const result = (patch: Partial<RunResult>): RunResult => ({
	editions: 4,
	queries: 7,
	listings: 120,
	matched: 9,
	new: 2,
	quotes: 1,
	errors: [],
	...patch
});

describe('lastRunSummary', () => {
	it('is null before the first run', () => {
		expect(lastRunSummary(null)).toBeNull();
	});

	it('reports a failed run', () => {
		expect(lastRunSummary(result({ failure: 'db down' }))).toBe(
			'Ostatnia wycena przerwana: db down'
		);
	});

	it('counts what a run did', () => {
		expect(lastRunSummary(result({}))).toBe(
			'Wydań: 4, dopasowanych ofert: 9, nowych: 2, wycen skupu: 1'
		);
		expect(lastRunSummary(result({ errors: ['a', 'b'] }))).toBe(
			'Wydań: 4, dopasowanych ofert: 9, nowych: 2, wycen skupu: 1, błędów źródeł: 2'
		);
	});
});

const point = (day: string, base: number): HistoryPoint => ({
	day,
	computed_at: `${day}T04:00:00Z`,
	copies: 3,
	spent_cents: 10000,
	floor: { cents: base, copies: 1 },
	mid: { cents: base * 4, copies: 3 },
	ceiling: { cents: base * 8, copies: 2 },
	low_confidence: 1
});

describe('history chart', () => {
	it('maps days to UTC midnight and back to a short label', () => {
		const ts = dayTimestamp('2026-11-17');
		expect(ts).toBe(Date.UTC(2026, 10, 17));
		expect(formatDay(ts)).toBe('17 lis');
	});

	it('draws each bound and the spending in złoty', () => {
		const series = historySeries([point('2026-11-16', 1000), point('2026-11-17', 1100)]);
		expect(series.map((s) => s.label)).toEqual(['Skup', 'Używane', 'Nowe', 'Wydano']);
		expect(series[1].points).toEqual([
			{ x: Date.UTC(2026, 10, 16), y: 40 },
			{ x: Date.UTC(2026, 10, 17), y: 44 }
		]);
		expect(series[3].points.map((p) => p.y)).toEqual([100, 100]);
	});

	it('formats axis values in whole złoty', () => {
		expect(nbsp(formatZl(12345.4))).toBe('12 345 zł');
	});
});
