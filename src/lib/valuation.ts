import { formatLabels, formatPLN, kindLabels } from '#lib/collection.js';
import type {
	Bound,
	CopyValue,
	DiscogsGrade,
	Estimate,
	Gain,
	HistoryPoint,
	RunnerStatus,
	Summary,
	ValuationRunResult,
	ValuationView,
	ValueSource
} from '#lib/api.gen.js';

export type {
	Bound,
	CopyValue,
	DiscogsGrade,
	Estimate,
	Gain,
	HistoryPoint,
	RunnerStatus,
	Summary,
	TitleValues,
	ValuationRunResult as RunResult,
	ValuationView,
	ValueSource
} from '#lib/api.gen.js';

export type BoundKey = 'floor' | 'mid' | 'ceiling';

export const boundKeys: BoundKey[] = ['floor', 'mid', 'ceiling'];

export const boundLabels: Record<BoundKey, string> = {
	floor: 'Skup',
	mid: 'Używane',
	ceiling: 'Nowe'
};

export const boundHints: Record<BoundKey, string> = {
	floor: 'Ile od ręki zapłaci skup (SkupSzop, tylko książki).',
	mid: 'Mediana cen ofertowych używanych egzemplarzy na OLX, bez wartości odstających.',
	ceiling:
		'Sugerowana cena egzemplarza w stanie Mint z Discogs, tylko dla płyt powiązanych z Discogs.'
};

export const sourceLabels: Record<ValueSource, string> = {
	olx: 'OLX',
	skupszop: 'SkupSzop',
	discogs: 'Discogs'
};

/** "oferta" with its Polish plural for count. */
export function offers(count: number): string {
	const last = count % 10;
	const tens = count % 100;
	if (count === 1) return '1 oferta';
	if (last >= 2 && last <= 4 && (tens < 12 || tens > 14)) return `${count} oferty`;
	return `${count} ofert`;
}

/** The amount of one bound of a copy, or a dash when it has no prices. */
export function estimateValue(e: Estimate | null): string {
	return e ? formatPLN(e.cents) : '—';
}

/** The grade of a Discogs used value when the server names none. */
const defaultGrade: DiscogsGrade = 'VG+';

/**
 * Where one bound of a copy comes from, as "OLX · 5 ofert (2 odrzucone)". A Discogs middle names
 * the grade of its suggestion, VG+ when the server names none.
 */
export function estimateSource(
	bound: BoundKey,
	e: Estimate | null,
	grade: DiscogsGrade = defaultGrade
): string {
	if (!e) return 'brak danych';
	if (e.source === 'discogs') {
		if (bound === 'floor') return 'Discogs · najtańsza oferta';
		return bound === 'mid' ? `Discogs · sugestia ${grade}` : 'Discogs · sugestia Mint';
	}
	if (bound === 'floor') {
		return e.cents === 0 ? `${sourceLabels[e.source]} nie skupuje` : sourceLabels[e.source];
	}
	const base = `${sourceLabels[e.source]} · ${offers(e.count)}`;
	return e.trimmed > 0 ? `${base} (${e.trimmed} odrzucone)` : base;
}

/** How often the valuation runs, as "codziennie" or "co 12 godz."; empty when it does not run. */
export function intervalLabel(status: RunnerStatus): string {
	if (!status.scheduled || status.interval_seconds <= 0) return '';
	const hours = Math.round(status.interval_seconds / 3600);
	if (hours === 24) return 'codziennie';
	if (hours % 24 === 0) return `co ${hours / 24} dni`;
	return `co ${hours} godz.`;
}

/** One line about the last finished run, or null before the first one. */
export function lastRunSummary(result: ValuationRunResult | null): string | null {
	if (!result) return null;
	if (result.failure) return `Ostatnia wycena przerwana: ${result.failure}`;
	const parts = [
		`Wydań: ${result.editions}`,
		`dopasowanych ofert: ${result.matched}`,
		`nowych: ${result.new}`,
		`wycen skupu: ${result.quotes}`
	];
	if (result.errors.length > 0) parts.push(`błędów źródeł: ${result.errors.length}`);
	return parts.join(', ');
}

const dayMilliseconds = 86_400_000;

function dayTimestamp(day: string): number {
	return Date.parse(`${day}T00:00:00Z`);
}

const dayFormat = new Intl.DateTimeFormat('pl-PL', {
	day: 'numeric',
	month: 'short',
	timeZone: 'UTC'
});

const zl = new Intl.NumberFormat('pl-PL', { maximumFractionDigits: 0 });

/** A value in złoty as "1 234 zł". */
export function formatZl(value: number): string {
	return `${zl.format(value)} zł`;
}

/** An amount in grosze as whole złoty, "1 234 zł". */
export function wholeZl(cents: number): string {
	return formatZl(cents / 100);
}

const pluralRules = new Intl.PluralRules('pl-PL');

/** "egzemplarz" with its Polish plural for count. */
export function copiesLabel(count: number): string {
	const plural = pluralRules.select(count);
	const noun = plural === 'one' ? 'egzemplarz' : plural === 'few' ? 'egzemplarze' : 'egzemplarzy';
	return `${zl.format(count)} ${noun}`;
}

const relativeDays = new Intl.RelativeTimeFormat('pl', { numeric: 'auto' });

function today(now: Date): number {
	return Date.UTC(now.getFullYear(), now.getMonth(), now.getDate());
}

/** How long ago day was, in the reader's calendar: "dzisiaj", "wczoraj", "3 dni temu". */
export function daysAgo(day: string, now: Date): string {
	return relativeDays.format(Math.round((dayTimestamp(day) - today(now)) / dayMilliseconds), 'day');
}

/** A valuation day as "17 lis". */
export function dayLabel(day: string): string {
	return dayFormat.format(new Date(dayTimestamp(day)));
}

/** The page's one line under its title: copies, how many have no used value, and when. */
export function pageSummary(view: ValuationView, now: Date): string {
	const { summary } = view;
	const parts = [copiesLabel(summary.copies)];
	const unvalued = summary.copies - summary.mid.copies;
	if (unvalued > 0) parts.push(`${unvalued} bez wyceny`);
	if (view.last_valuation) parts.push(`wycena ${daysAgo(view.last_valuation, now)}`);
	return parts.join(' · ');
}

function signed(value: number, text: string): string {
	if (value > 0) return `+${text}`;
	if (value < 0) return `−${text}`;
	return text;
}

/**
 * How far the used value of the copies with a price lies from what they cost, "+10 zł · +20%";
 * copies that cost nothing have no percent, "+30 zł". Null without such copies, also when an
 * older server sent no gain.
 */
function gainDelta(gain: Gain | undefined): string | null {
	if (!gain || gain.copies === 0) return null;
	const diffCents = gain.mid_cents - gain.spent_cents;
	const diff = Math.round(diffCents / 100) || 0;
	const amount = signed(diff, formatZl(Math.abs(diff)));
	if (gain.spent_cents <= 0) return amount;
	const percent = Math.round((diffCents / gain.spent_cents) * 100) || 0;
	return `${amount} · ${signed(percent, `${Math.abs(percent)}%`)}`;
}

/**
 * The value hero's figures: the used total in whole złoty and what was spent, and how far the
 * used value lies from the price paid over the copies that have both, in złoty and percent from
 * the amounts in grosze. When those copies are fewer than the collection, coverage says how many
 * the difference counts.
 */
export function heroFigures(summary: Summary) {
	const value = summary.mid.copies > 0 ? summary.mid.cents : null;
	// A response from a server older than the gain has none.
	const gain: Gain | undefined = summary.gain;
	const delta = gainDelta(gain);
	const coverage =
		gain && delta !== null && gain.copies < summary.copies
			? `Różnica dla ${zl.format(gain.copies)} z ${zl.format(summary.copies)} egzemplarzy`
			: null;
	return {
		value: value === null ? null : zl.format(value / 100),
		spent: wholeZl(summary.spent_cents),
		delta,
		coverage
	};
}

function percentOf(value: number, low: number, high: number): number {
	return Math.min(100, Math.max(0, ((value - low) / (high - low)) * 100));
}

/**
 * Where the used total and the amount spent stand on the bar from the buy-back total to the new
 * total, in percent of its width; a value past an end stays on that end. A collection that no
 * buy-back shop quotes starts the bar at 0 zł, and one without a used total has no used mark.
 * Null without a new total above the start, as the bar then has no length.
 */
export function rangeMarks(summary: Summary): { mid: number | null; spent: number } | null {
	const low = summary.floor.copies > 0 ? summary.floor.cents : 0;
	const high = summary.ceiling.cents;
	if (summary.ceiling.copies === 0 || high <= low) return null;
	return {
		mid: summary.mid.copies > 0 ? percentOf(summary.mid.cents, low, high) : null,
		spent: percentOf(summary.spent_cents, low, high)
	};
}

/** A bound's label with its total, "Skup 39 zł", or a dash when no copy has it. */
export function boundLine(key: BoundKey, b: Bound): string {
	return `${boundLabels[key]} ${b.copies === 0 ? '—' : wholeZl(b.cents)}`;
}

/** The valuations from the last days up to today in the reader's calendar. */
export function recentHistory(history: HistoryPoint[], now: Date, days = 90): HistoryPoint[] {
	const from = today(now) - (days - 1) * dayMilliseconds;
	return history.filter((p) => dayTimestamp(p.day) >= from);
}

/** The days a history spans, as "24 sie – 19 lis", or one day as "17 lis". */
export function historySpan(history: HistoryPoint[]): string {
	const first = history[0];
	const last = history.at(-1);
	if (!first || !last) return '';
	const start = dayLabel(first.day);
	const end = dayLabel(last.day);
	return start === end ? end : `${start} – ${end}`;
}

/**
 * One valuation day in złoty, x counted in days from the first valuation shown. A bound no copy
 * had that day is null, while a bound of 0 zł was quoted at 0 zł.
 */
export type HistoryRow = {
	x: number;
	day: string;
	floor: number | null;
	mid: number | null;
	ceiling: number | null;
	spent: number;
};

const boundZl = (b: Bound): number | null => (b.copies > 0 ? b.cents / 100 : null);

/** The history as rows of złoty, x in days from its first day. */
export function historyRows(history: HistoryPoint[]): HistoryRow[] {
	const start = history[0] ? dayTimestamp(history[0].day) : 0;
	return history.map((p) => ({
		x: (dayTimestamp(p.day) - start) / dayMilliseconds,
		day: dayLabel(p.day),
		floor: boundZl(p.floor),
		mid: boundZl(p.mid),
		ceiling: boundZl(p.ceiling),
		spent: p.spent_cents / 100
	}));
}

type Point = [number, number];

const round = (v: number) => Math.round(v * 10) / 10;

/**
 * A scale from the values to a width × height box, xInset and yInset from its edges, with a
 * margin of pad times the value range above and below.
 */
function scale(
	xs: number[],
	ys: number[],
	box: { width: number; height: number; xInset: number; yInset: number; pad: number }
) {
	const xMax = Math.max(...xs);
	let yMin = Math.min(...ys);
	let yMax = Math.max(...ys);
	if (yMax === yMin) {
		yMin -= 1;
		yMax += 1;
	}
	const margin = (yMax - yMin) * box.pad;
	yMin -= margin;
	yMax += margin;
	const w = box.width - 2 * box.xInset;
	const h = box.height - 2 * box.yInset;
	return {
		x: (x: number) => round(box.xInset + (xMax === 0 ? 0 : x / xMax) * w),
		y: (y: number) => round(box.yInset + (1 - (y - yMin) / (yMax - yMin)) * h)
	};
}

/** Runs of consecutive rows where pick has a value. */
function runs<T>(rows: HistoryRow[], pick: (r: HistoryRow) => T | null): [HistoryRow, T][][] {
	const out: [HistoryRow, T][][] = [];
	let run: [HistoryRow, T][] = [];
	for (const r of rows) {
		const v = pick(r);
		if (v === null) {
			if (run.length > 0) out.push(run);
			run = [];
		} else run.push([r, v]);
	}
	if (run.length > 0) out.push(run);
	return out;
}

/** A line through points; a lone point draws as a dot under a round cap. */
function line(points: Point[]): string {
	const [first] = points;
	if (points.length === 1 && first) return `M${first[0]} ${first[1]} L${first[0]} ${first[1]}`;
	return points.map(([x, y], i) => `${i === 0 ? 'M' : 'L'}${x} ${y}`).join(' ');
}

/** A single day stretches across the plot, so it draws as a level band and lines. */
function spread(rows: HistoryRow[]): HistoryRow[] {
	const only = rows[0];
	return rows.length === 1 && only ? [only, { ...only, x: 1 }] : rows;
}

/** The band of one day: from its lowest to its highest estimate, null without any. */
function bandOf(r: HistoryRow): { low: number; high: number } | null {
	const known = [r.floor, r.mid, r.ceiling].filter((v): v is number => v !== null);
	return known.length === 0 ? null : { low: Math.min(...known), high: Math.max(...known) };
}

/**
 * SVG paths of the history in a width × height box: the band from the buy-back to the new
 * total, the used total and the amount spent. A day that lacks a bound keeps the band over the
 * estimates it has, so a collection no buy-back shop quotes still shows its new price, and its
 * missing lines leave gaps. The values fill the height less an inset and an 8% margin, so the
 * lines never touch the edges.
 */
export function historyPaths(history: HistoryPoint[], width: number, height: number, inset = 4) {
	const rows = spread(historyRows(history));
	if (rows.length === 0) return null;
	const ys = rows.flatMap((r) =>
		[r.floor, r.mid, r.ceiling, r.spent].filter((v): v is number => v !== null)
	);
	const s = scale(
		rows.map((r) => r.x),
		ys,
		{ width, height, xInset: 0, yInset: inset, pad: 0.08 }
	);
	const lines = (pick: (r: HistoryRow) => number | null) =>
		runs(rows, pick)
			.map((run) => line(run.map(([r, v]): Point => [s.x(r.x), s.y(v)])))
			.join(' ');
	const band = runs(rows, bandOf)
		.map((run) => {
			const top = run.map(([r, v]): Point => [s.x(r.x), s.y(v.high)]);
			const bottom = run.map(([r, v]): Point => [s.x(r.x), s.y(v.low)]).reverse();
			return `${line(top)} ${line(bottom).replace(/^M/, 'L')} Z`;
		})
		.join(' ');
	return { band, mid: lines((r) => r.mid), spent: lines((r) => r.spent) };
}

/**
 * A sparkline of the used total in a width × height box, with its last point; days without a
 * used total leave a gap. Null under two days with one.
 */
export function sparkline(history: HistoryPoint[], width: number, height: number, inset = 4) {
	const rows = historyRows(history);
	const valued = rows.filter((r) => r.mid !== null);
	if (valued.length < 2) return null;
	const s = scale(
		valued.map((r) => r.x),
		valued.map((r) => r.mid ?? 0),
		{ width, height, xInset: inset, yInset: inset, pad: 0 }
	);
	const path = runs(rows, (r) => r.mid)
		.map((run) => line(run.map(([r, v]): Point => [s.x(r.x), s.y(v)])))
		.join(' ');
	const last = valued.at(-1);
	return { path, end: { x: s.x(last?.x ?? 0), y: s.y(last?.mid ?? 0) } };
}

/** Copies by used value, highest first; copies without one keep their order at the end. */
export function byValue(copies: readonly CopyValue[]): CopyValue[] {
	return [...copies].sort((a, b) => (b.mid?.cents ?? -1) - (a.mid?.cents ?? -1));
}

/** A copy's range from buy-back to new, "118–201 zł", or the one end it has. */
export function copyRange(c: CopyValue): string | null {
	const zlOnly = (cents: number) => zl.format(Math.round(cents / 100));
	if (c.floor && c.ceiling) return `${zlOnly(c.floor.cents)}–${wholeZl(c.ceiling.cents)}`;
	if (c.ceiling) return `do ${wholeZl(c.ceiling.cents)}`;
	if (c.floor) return `od ${wholeZl(c.floor.cents)}`;
	return null;
}

/** "Nowe: brak danych" under a copy that has a value but no ceiling. */
export function missingCeiling(c: CopyValue): string | null {
	return (c.floor || c.mid) && !c.ceiling
		? `${boundLabels.ceiling}: ${estimateSource('ceiling', null)}`
		: null;
}

/**
 * The line under a copy's value: its range, with the grade of a Discogs used value, as
 * "40–160 zł · sugestia G", else where its used value comes from.
 */
export function copyNote(c: CopyValue): string {
	const range = copyRange(c);
	if (range === null) return estimateSource('mid', c.mid, c.mid_grade);
	return c.mid?.source === 'discogs' ? `${range} · sugestia ${c.mid_grade ?? defaultGrade}` : range;
}

export const likelySoldLabel = 'Prawdopodobnie sprzedane';

/**
 * A copy's likely-sold price: the amount in whole złoty and how many vanished listings it rests
 * on, as "3 oferty", "1 oferta · mało danych" under minObservations, or with the outliers left
 * out. Null when no listing of its title and format vanished soon enough.
 */
export function likelySold(
	c: CopyValue,
	minObservations: number
): { value: string; note: string; low: boolean } | null {
	const e = c.likely_sold;
	if (!e) return null;
	const low = e.count < minObservations;
	const counted = e.trimmed > 0 ? `${offers(e.count)} (${e.trimmed} odrzucone)` : offers(e.count);
	return { value: wholeZl(e.cents), note: low ? `${counted} · mało danych` : counted, low };
}

/** The line under a copy's value on a phone, "Prawdopodobnie sprzedane: 50 zł · 3 oferty". */
export function likelySoldLine(c: CopyValue, minObservations: number): string | null {
	const sold = likelySold(c, minObservations);
	return sold ? `${likelySoldLabel}: ${sold.value} · ${sold.note}` : null;
}

/** The Discogs release a copy's shown prices come from, or null when none is from Discogs. */
export function discogsRelease(c: CopyValue): number | null {
	const priced = [c.floor, c.mid, c.ceiling].some((e) => e?.source === 'discogs');
	return priced ? (c.discogs_release_id ?? null) : null;
}

/** A number rounded to a whole magnitude with its sign; one that rounds to 0 has none. */
function signedWhole(value: number, text: (magnitude: number) => string): string {
	const magnitude = Math.round(Math.abs(value));
	return signed(magnitude === 0 ? 0 : value, text(magnitude));
}

/**
 * A copy's used value against what was paid for it, as on its title page: "+10 zł · +20%", in
 * whole złoty and percent rounded from grosze. A copy bought for 0 zł has no percent, and one
 * without a used value or a price shows a dash.
 */
export function copyGain(c: CopyValue): string {
	if (!c.mid || c.price_cents == null) return '—';
	const cents = c.mid.cents - c.price_cents;
	const amount = signedWhole(cents / 100, formatZl);
	if (c.price_cents <= 0) return amount;
	return `${amount} · ${signedWhole((cents * 100) / c.price_cents, (n) => `${n}%`)}`;
}

/** A copy's format, or its kind, and what was paid for it: "Winyl · kupiono za 189,99 zł". */
export function copyMeta(c: CopyValue): string {
	const medium = c.format ? formatLabels[c.format] : kindLabels[c.kind];
	return c.price_cents == null ? medium : `${medium} · kupiono za ${formatPLN(c.price_cents)}`;
}
