import { describe, expect, it } from 'vitest';
import {
	boundLine,
	byValue,
	copiesLabel,
	copyMeta,
	copyNote,
	copyRange,
	missingCeiling,
	daysAgo,
	discogsRelease,
	estimateSource,
	estimateValue,
	formatZl,
	heroFigures,
	historyPaths,
	historyRows,
	historySpan,
	intervalLabel,
	lastRunSummary,
	offers,
	pageSummary,
	rangeMarks,
	recentHistory,
	sparkline,
	type CopyValue,
	type HistoryPoint,
	type RunResult,
	type RunnerStatus,
	type Summary,
	type ValuationView
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

	it('names the source and count of a used price', () => {
		expect(estimateSource('mid', { cents: 2550, source: 'olx', count: 5, trimmed: 2 })).toBe(
			'OLX · 5 ofert (2 odrzucone)'
		);
		expect(estimateSource('mid', { cents: 4999, source: 'olx', count: 1, trimmed: 0 })).toBe(
			'OLX · 1 oferta'
		);
		expect(estimateSource('mid', { cents: 2600, source: 'olx', count: 3, trimmed: 0 })).toBe(
			'OLX · 3 oferty'
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
	releases: 0,
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

/**
 * A summary whose gain, unless patched, covers every valued copy as if each had a price, or no
 * copy when nothing was spent.
 */
const summary = (patch: Partial<Summary> = {}): Summary => {
	const s = {
		copies: 20,
		spent_cents: 117300,
		floor: { cents: 3900, copies: 6 },
		mid: { cents: 84800, copies: 20 },
		ceiling: { cents: 137500, copies: 18 },
		low_confidence: 3,
		...patch
	};
	return {
		gain:
			s.spent_cents > 0
				? { copies: s.mid.copies, mid_cents: s.mid.cents, spent_cents: s.spent_cents }
				: { copies: 0, mid_cents: 0, spent_cents: 0 },
		...s
	};
};

describe('value hero', () => {
	it('states the used total against what was spent', () => {
		const f = heroFigures(summary());

		expect(f.value).toBe('848');
		expect(nbsp(f.spent)).toBe('1173 zł');
		expect(nbsp(f.delta ?? '')).toBe('−325 zł · −28%');
	});

	it('signs a gain and rounds it from grosze', () => {
		const f = heroFigures(summary({ mid: { cents: 1234560, copies: 3 }, spent_cents: 1000040 }));

		expect(nbsp(f.value ?? '')).toBe('12 346');
		expect(nbsp(f.delta ?? '')).toBe('+2345 zł · +23%');
	});

	it('does not round a grosz apart into whole złoty and percent', () => {
		const f = heroFigures(summary({ mid: { cents: 1050, copies: 1 }, spent_cents: 1049 }));

		expect(f.value).toBe('11');
		expect(nbsp(f.spent)).toBe('10 zł');
		expect(nbsp(f.delta ?? '')).toBe('0 zł · 0%');
		expect(
			nbsp(heroFigures(summary({ mid: { cents: 1000, copies: 1 }, spent_cents: 1049 })).delta ?? '')
		).toBe('0 zł · −5%');
	});

	it('leaves the difference out when nothing was spent or valued', () => {
		expect(heroFigures(summary({ spent_cents: 0 })).delta).toBeNull();
		const unvalued = heroFigures(summary({ mid: { cents: 0, copies: 0 } }));
		expect(unvalued.value).toBeNull();
		expect(unvalued.delta).toBeNull();
	});

	it('shows no sign for an even difference', () => {
		expect(nbsp(heroFigures(summary({ spent_cents: 84800 })).delta ?? '')).toBe('0 zł · 0%');
	});

	it('compares only the copies with both a price and a used value', () => {
		// One copy bought for 50 zł worth 60 zł, one bought for 200 zł without a used value
		// and one without a price worth 40 zł.
		const f = heroFigures(
			summary({
				copies: 3,
				spent_cents: 25000,
				mid: { cents: 10000, copies: 2 },
				gain: { copies: 1, mid_cents: 6000, spent_cents: 5000 }
			})
		);

		expect(f.value).toBe('100');
		expect(nbsp(f.spent)).toBe('250 zł');
		expect(nbsp(f.delta ?? '')).toBe('+10 zł · +20%');
		expect(f.coverage).toBe('Różnica dla 1 z 3 egzemplarzy');
	});

	it('says nothing of coverage when the difference counts every copy', () => {
		const f = heroFigures(summary());

		expect(f.delta).not.toBeNull();
		expect(f.coverage).toBeNull();
	});

	it('shows no difference without a copy that has both figures', () => {
		const f = heroFigures(
			summary({
				copies: 2,
				spent_cents: 5000,
				mid: { cents: 4000, copies: 1 },
				gain: { copies: 0, mid_cents: 0, spent_cents: 0 }
			})
		);

		expect(f.value).toBe('40');
		expect(nbsp(f.spent)).toBe('50 zł');
		expect(f.delta).toBeNull();
		expect(f.coverage).toBeNull();
	});

	it('shows the difference without a percent when the compared copies cost nothing', () => {
		// One copy bought for 0 zł worth 30 zł and one bought for 200 zł without a used value.
		const f = heroFigures(
			summary({
				copies: 2,
				spent_cents: 20000,
				mid: { cents: 3000, copies: 1 },
				gain: { copies: 1, mid_cents: 3000, spent_cents: 0 }
			})
		);

		expect(nbsp(f.delta ?? '')).toBe('+30 zł');
		expect(f.coverage).toBe('Różnica dla 1 z 2 egzemplarzy');
	});

	it('keeps the totals and shows no difference for a response without a gain', () => {
		// An open app reading a server rolled back to before the gain existed.
		const { gain: _, ...older } = summary();
		const f = heroFigures(older as Summary);

		expect(f.value).toBe('848');
		expect(nbsp(f.spent)).toBe('1173 zł');
		expect(f.delta).toBeNull();
		expect(f.coverage).toBeNull();
	});

	it('places the used total and the amount spent between buy-back and new', () => {
		const m = rangeMarks(summary());

		expect(m?.mid).toBeCloseTo(60.55, 1);
		expect(m?.spent).toBeCloseTo(84.88, 1);
	});

	it('keeps marks past either end on that end', () => {
		expect(rangeMarks(summary({ spent_cents: 500000 }))?.spent).toBe(100);
		expect(rangeMarks(summary({ mid: { cents: 1000, copies: 1 } }))?.mid).toBe(0);
	});

	it('marks no used total without one', () => {
		expect(rangeMarks(summary({ mid: { cents: 0, copies: 0 } }))).toEqual({
			mid: null,
			spent: expect.closeTo(84.88, 1)
		});
	});

	it('starts the bar at 0 zł when no buy-back shop quotes the collection', () => {
		const m = rangeMarks(
			summary({
				floor: { cents: 0, copies: 0 },
				mid: { cents: 8000, copies: 1 },
				ceiling: { cents: 16000, copies: 1 },
				spent_cents: 10000
			})
		);

		expect(m).toEqual({ mid: 50, spent: 62.5 });
	});

	it('draws no bar without a new total above its start', () => {
		const unpriced = { cents: 0, copies: 0 };
		const movie = { mid: { cents: 8000, copies: 1 }, spent_cents: 10000 };

		expect(rangeMarks(summary({ ...movie, floor: unpriced, ceiling: unpriced }))).toBeNull();
		expect(rangeMarks(summary({ ...movie, ceiling: unpriced }))).toBeNull();
		expect(
			rangeMarks(
				summary({ floor: { cents: 5000, copies: 1 }, ceiling: { cents: 5000, copies: 1 } })
			)
		).toBeNull();
	});

	it('labels each bound total, or a dash when no copy has it', () => {
		expect(nbsp(boundLine('floor', { cents: 3900, copies: 6 }))).toBe('Skup 39 zł');
		expect(nbsp(boundLine('ceiling', { cents: 137500, copies: 18 }))).toBe('Nowe 1375 zł');
		expect(boundLine('floor', { cents: 0, copies: 0 })).toBe('Skup —');
		expect(boundLine('floor', { cents: 0, copies: 2 })).toBe('Skup 0 zł');
	});
});

describe('page summary', () => {
	it.each([
		[1, '1 egzemplarz'],
		[2, '2 egzemplarze'],
		[5, '5 egzemplarzy'],
		[22, '22 egzemplarze'],
		[1000, '1000 egzemplarzy']
	])('%i → %s', (count, want) => {
		expect(nbsp(copiesLabel(count))).toBe(want);
	});

	it.each([
		['2026-11-19', 'dzisiaj'],
		['2026-11-18', 'wczoraj'],
		['2026-11-16', '3 dni temu']
	])('%s on 19 Nov is %s', (day, want) => {
		expect(daysAgo(day, new Date(2026, 10, 19, 23, 30))).toBe(want);
	});

	it('counts copies, the unvalued ones and when the last valuation ran', () => {
		const view = (s: Summary): ValuationView => ({
			status: status({}),
			window_days: 90,
			min_observations: 3,
			summary: s,
			copies: [],
			history: [],
			last_valuation: '2026-11-18'
		});
		const now = new Date(2026, 10, 19, 9);

		expect(pageSummary(view(summary()), now)).toBe('20 egzemplarzy · wycena wczoraj');
		expect(pageSummary(view(summary({ mid: { cents: 100, copies: 18 } })), now)).toBe(
			'20 egzemplarzy · 2 bez wyceny · wycena wczoraj'
		);
	});
});

describe('history', () => {
	it('keeps the last 90 days up to today', () => {
		const history = [
			point('2026-08-21', 1),
			point('2026-08-22', 2),
			point('2026-10-01', 3),
			point('2026-11-19', 4)
		];
		const now = new Date(2026, 10, 19, 23, 30);

		expect(recentHistory(history, now).map((p) => p.day)).toEqual([
			'2026-08-22',
			'2026-10-01',
			'2026-11-19'
		]);
		expect(recentHistory([], now)).toEqual([]);
	});

	it('leaves out valuations older than 90 days even when they are the latest', () => {
		const history = [point('2026-07-01', 1), point('2026-08-01', 2)];

		expect(recentHistory(history, new Date(2026, 10, 19, 9))).toEqual([]);
	});

	it('names the days it spans', () => {
		expect(historySpan([])).toBe('');
		expect(historySpan([point('2026-11-17', 1)])).toBe('17 lis');
		expect(historySpan([point('2026-08-24', 1), point('2026-11-19', 1)])).toBe('24 sie – 19 lis');
	});

	it('turns each valuation into złoty, x in days from the first', () => {
		const rows = historyRows([point('2026-10-24', 1000), point('2026-10-27', 1100)]);

		expect(rows).toEqual([
			{ x: 0, day: '24 paź', floor: 10, mid: 40, ceiling: 80, spent: 100 },
			{ x: 3, day: '27 paź', floor: 11, mid: 44, ceiling: 88, spent: 100 }
		]);
	});

	it('leaves bounds no copy had as gaps, keeping quotes of 0 zł', () => {
		const unpriced = point('2026-10-25', 1000);
		unpriced.mid = { cents: 0, copies: 0 };
		unpriced.floor = { cents: 0, copies: 2 };
		unpriced.ceiling = { cents: 0, copies: 0 };

		expect(historyRows([unpriced])[0]).toEqual({
			x: 0,
			day: '25 paź',
			floor: 0,
			mid: null,
			ceiling: null,
			spent: 100
		});
	});

	it('keeps the band over the estimates a day has', () => {
		const movies = (day: string, base: number) => {
			const p = point(day, base);
			p.floor = { cents: 0, copies: 0 };
			return p;
		};

		const paths = historyPaths(
			[movies('2026-10-24', 1000), movies('2026-10-27', 1000)],
			100,
			100,
			0
		);

		// Used 40 zł, new 80 zł and spent 100 zł with an 8% margin: y = (1 - (zł - 35.2) / 69.6) * 100.
		expect(paths?.band).toBe('M0 35.6 L100 35.6 L100 93.1 L0 93.1 Z');
		expect(paths?.mid).toBe('M0 93.1 L100 93.1');
	});

	it('breaks the lines where a bound is missing', () => {
		const gap = point('2026-10-25', 1100);
		gap.mid = { cents: 0, copies: 0 };
		gap.ceiling = { cents: 0, copies: 0 };
		const history = [
			point('2026-10-24', 1000),
			gap,
			point('2026-10-26', 1000),
			point('2026-10-27', 1000)
		];

		const paths = historyPaths(history, 300, 100, 0);

		const xs = (d = '') => d.split(' ').filter((t) => /^[MLZ]/.test(t));
		expect(xs(paths?.mid)).toEqual(['M0', 'L0', 'M200', 'L300']);
		expect(xs(paths?.band)).toEqual([
			'M0',
			'L100',
			'L200',
			'L300',
			'L300',
			'L200',
			'L100',
			'L0',
			'Z'
		]);
		expect(xs(paths?.spent)).toEqual(['M0', 'L100', 'L200', 'L300']);
	});

	it('draws the band from buy-back to new, the used line and the spent line', () => {
		const paths = historyPaths([point('2026-10-24', 1000), point('2026-10-27', 1250)], 100, 100, 0);
		// Values run from 10 to 100 zł with an 8% margin: y = (1 - (zł - 2.8) / 104.4) * 100.
		expect(paths).toEqual({
			band: 'M0 26.1 L100 6.9 L100 90.7 L0 93.1 Z',
			mid: 'M0 64.4 L100 54.8',
			spent: 'M0 6.9 L100 6.9'
		});
	});

	it('stretches a single valuation across the plot', () => {
		const paths = historyPaths([point('2026-11-17', 1000)], 100, 50, 0);

		expect(paths?.mid).toMatch(/^M0 (\S+) L100 \1$/);
		expect(paths?.spent).toMatch(/^M0 (\S+) L100 \1$/);
		expect(historyPaths([], 100, 50)).toBeNull();
	});

	it('leaves days without a used total out of the sparkline', () => {
		const gap = point('2026-11-11', 1000);
		gap.mid = { cents: 0, copies: 0 };
		const last = point('2026-11-18', 1000);
		last.mid = { cents: 0, copies: 0 };

		expect(sparkline([point('2026-11-01', 1000), gap, last], 92, 36)).toBeNull();

		const spark = sparkline(
			[point('2026-11-01', 1000), gap, point('2026-11-17', 2000), last],
			92,
			36
		);
		expect(spark).toEqual({ path: 'M4 32 L4 32 M88 4 L88 4', end: { x: 88, y: 4 } });
	});

	it('draws the used total as a sparkline ending on the latest value', () => {
		expect(sparkline([point('2026-11-17', 1000)], 92, 36)).toBeNull();

		const spark = sparkline(
			[point('2026-11-01', 1000), point('2026-11-11', 1000), point('2026-11-17', 2000)],
			92,
			36
		);

		expect(spark).toEqual({ path: 'M4 32 L56.5 32 L88 4', end: { x: 88, y: 4 } });
	});

	it('formats values in whole złoty', () => {
		expect(nbsp(formatZl(12345.4))).toBe('12 345 zł');
	});
});

const copy = (patch: Partial<CopyValue>): CopyValue => ({
	copy_id: 1,
	edition_id: 1,
	title_id: 1,
	title_name: 'Spokojnie',
	kind: 'music',
	format: 'vinyl',
	barcode: null,
	price_cents: null,
	floor: null,
	mid: null,
	ceiling: null,
	low_confidence: false,
	...patch
});

const est = (cents: number, count = 3) => ({ cents, source: 'olx' as const, count, trimmed: 0 });

describe('copy rows', () => {
	it('sorts by used value from the highest, unvalued copies last in their order', () => {
		const sorted = byValue([
			copy({ copy_id: 1 }),
			copy({ copy_id: 2, mid: est(7900) }),
			copy({ copy_id: 3, mid: est(15200) }),
			copy({ copy_id: 4 }),
			copy({ copy_id: 5, mid: est(0) })
		]);

		expect(sorted.map((c) => c.copy_id)).toEqual([3, 2, 5, 1, 4]);
	});

	it('shows the range from buy-back to new, or the end it has', () => {
		expect(nbsp(copyRange(copy({ floor: est(11800), ceiling: est(20100) })) ?? '')).toBe(
			'118–201 zł'
		);
		expect(nbsp(copyRange(copy({ floor: est(0), ceiling: est(14900) })) ?? '')).toBe('0–149 zł');
		expect(nbsp(copyRange(copy({ ceiling: est(14900) })) ?? '')).toBe('do 149 zł');
		expect(nbsp(copyRange(copy({ floor: est(500) })) ?? '')).toBe('od 5 zł');
		expect(copyRange(copy({ mid: est(7900) }))).toBeNull();
	});

	it('falls back from the range to the source of the used value', () => {
		expect(nbsp(copyNote(copy({ floor: est(500), ceiling: est(900), mid: est(700) })))).toBe(
			'5–9 zł'
		);
		expect(copyNote(copy({ mid: est(7900, 2) }))).toBe('OLX · 2 oferty');
		expect(copyNote(copy({}))).toBe('brak danych');
	});

	it('marks a missing ceiling under every valued copy without one', () => {
		expect(missingCeiling(copy({ floor: est(500), mid: est(2500) }))).toBe('Nowe: brak danych');
		expect(missingCeiling(copy({ mid: est(2500) }))).toBe('Nowe: brak danych');
		expect(missingCeiling(copy({ floor: est(500), ceiling: est(900) }))).toBeNull();
		expect(missingCeiling(copy({}))).toBeNull();
	});

	it('credits Discogs only for a copy priced from its release', () => {
		const discogs = { cents: 7098, source: 'discogs' as const, count: 0, trimmed: 0 };
		expect(discogsRelease(copy({ discogs_release_id: 1873013, mid: discogs }))).toBe(1873013);
		expect(discogsRelease(copy({ discogs_release_id: 1873013, floor: discogs }))).toBe(1873013);
		expect(discogsRelease(copy({ discogs_release_id: 1873013, mid: est(2500) }))).toBeNull();
		expect(discogsRelease(copy({ discogs_release_id: 1873013 }))).toBeNull();
		expect(discogsRelease(copy({ mid: discogs }))).toBeNull();
	});

	it('names the format, or the kind, and what was paid', () => {
		expect(nbsp(copyMeta(copy({ price_cents: 18999 })))).toBe('Winyl · kupiono za 189,99 zł');
		expect(copyMeta(copy({ format: 'uhd' }))).toBe('4K UHD');
		expect(copyMeta(copy({ kind: 'book', format: null }))).toBe('Książka');
	});
});
