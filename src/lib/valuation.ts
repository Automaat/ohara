import { formatLabels, formatPLN, kindLabels, type Format, type Kind } from '#lib/collection.js';

export type ValueSource = 'olx' | 'vinted' | 'olx+vinted' | 'ceneo' | 'skupszop' | 'discogs';
export type BoundKey = 'floor' | 'mid' | 'ceiling';

export type Estimate = { cents: number; source: ValueSource; count: number; trimmed: number };

export type CopyValue = {
	copy_id: number;
	edition_id: number;
	title_id: number;
	title_name: string;
	kind: Kind;
	format: Format | null;
	barcode: string | null;
	price_cents: number | null;
	cover_url?: string;
	floor: Estimate | null;
	mid: Estimate | null;
	ceiling: Estimate | null;
	low_confidence: boolean;
};

export type Bound = { cents: number; copies: number };

export type Summary = {
	copies: number;
	spent_cents: number;
	floor: Bound;
	mid: Bound;
	ceiling: Bound;
	low_confidence: number;
};

export type HistoryPoint = Summary & { day: string; computed_at: string };

export type RunResult = {
	editions: number;
	queries: number;
	listings: number;
	matched: number;
	new: number;
	quotes: number;
	errors: string[];
	failure?: string;
};

export type RunnerStatus = {
	running: boolean;
	scheduled: boolean;
	interval_seconds: number;
	last_started_at: string | null;
	last_finished_at: string | null;
	last_result: RunResult | null;
};

export type ValuationView = {
	status: RunnerStatus;
	window_days: number;
	min_observations: number;
	summary: Summary;
	copies: CopyValue[];
	history: HistoryPoint[];
};

export const boundKeys: BoundKey[] = ['floor', 'mid', 'ceiling'];

export const boundLabels: Record<BoundKey, string> = {
	floor: 'Skup',
	mid: 'Używane',
	ceiling: 'Nowe'
};

export const boundHints: Record<BoundKey, string> = {
	floor: 'Ile od ręki zapłaci skup (SkupSzop, tylko książki).',
	mid: 'Mediana cen ofertowych używanych egzemplarzy na OLX i Vinted, bez wartości odstających.',
	ceiling: 'Najniższa cena nowego egzemplarza w sklepach z Ceneo (mediana produktów).'
};

export const sourceLabels: Record<ValueSource, string> = {
	olx: 'OLX',
	vinted: 'Vinted',
	'olx+vinted': 'OLX i Vinted',
	ceneo: 'Ceneo',
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

/** Where one bound of a copy comes from, as "OLX · 5 ofert (2 odrzucone)". */
export function estimateSource(bound: BoundKey, e: Estimate | null): string {
	if (!e) return 'brak danych';
	if (e.source === 'discogs') {
		if (bound === 'floor') return 'Discogs · najtańsza oferta';
		return bound === 'mid' ? 'Discogs · sugestia VG+' : 'Discogs · sugestia Mint';
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
export function lastRunSummary(result: RunResult | null): string | null {
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

/** How long ago day was, in the reader's calendar: "dzisiaj", "wczoraj", "3 dni temu". */
export function daysAgo(day: string, now: Date): string {
	const today = Date.UTC(now.getFullYear(), now.getMonth(), now.getDate());
	return relativeDays.format(Math.round((dayTimestamp(day) - today) / dayMilliseconds), 'day');
}

/** The page's one line under its title: copies, how many have no used value, and when. */
export function pageSummary(view: ValuationView, now: Date): string {
	const { summary } = view;
	const parts = [copiesLabel(summary.copies)];
	const unvalued = summary.copies - summary.mid.copies;
	if (unvalued > 0) parts.push(`${unvalued} bez wyceny`);
	const last = view.history.at(-1);
	if (last) parts.push(`wycena ${daysAgo(last.day, now)}`);
	return parts.join(' · ');
}

function signed(value: number, text: string): string {
	if (value > 0) return `+${text}`;
	if (value < 0) return `−${text}`;
	return text;
}

/**
 * The value hero's figures: the used total in whole złoty, what was spent, and how far apart
 * they are in złoty and percent. The difference is taken between the rounded amounts shown.
 */
export function heroFigures(summary: Summary) {
	const value = summary.mid.copies > 0 ? Math.round(summary.mid.cents / 100) : null;
	const spent = Math.round(summary.spent_cents / 100);
	let delta: string | null = null;
	if (value !== null && spent > 0) {
		const diff = value - spent;
		const percent = Math.round((diff / spent) * 100);
		delta = `${signed(diff, formatZl(Math.abs(diff)))} · ${signed(percent, `${Math.abs(percent)}%`)}`;
	}
	return {
		value: value === null ? null : zl.format(value),
		spent: formatZl(spent),
		delta
	};
}

function percentOf(value: number, low: number, high: number): number {
	if (high <= low) return 50;
	return Math.min(100, Math.max(0, ((value - low) / (high - low)) * 100));
}

/**
 * Where the used total and the amount spent stand on the bar from the buy-back total to the new
 * total, in percent of its width. A value past an end stays on that end.
 */
export function rangeMarks(summary: Summary): { mid: number; spent: number } {
	const low = summary.floor.cents;
	const high = summary.ceiling.cents;
	return {
		mid: percentOf(summary.mid.cents, low, high),
		spent: percentOf(summary.spent_cents, low, high)
	};
}

/** A bound's label with its total, "Skup 39 zł", or a dash when no copy has it. */
export function boundLine(key: BoundKey, b: Bound): string {
	return `${boundLabels[key]} ${b.copies === 0 ? '—' : wholeZl(b.cents)}`;
}

/** The valuations from the last days, counted back from the latest one. */
export function recentHistory(history: HistoryPoint[], days = 90): HistoryPoint[] {
	const last = history.at(-1);
	if (!last) return [];
	const from = dayTimestamp(last.day) - (days - 1) * dayMilliseconds;
	return history.filter((p) => dayTimestamp(p.day) >= from);
}

/** The days a history spans, as "24 sie – 19 lis", or one day as "17 lis". */
export function historySpan(history: HistoryPoint[]): string {
	const first = history[0];
	const last = history.at(-1);
	if (!first || !last) return '';
	const start = dayFormat.format(new Date(dayTimestamp(first.day)));
	const end = dayFormat.format(new Date(dayTimestamp(last.day)));
	return start === end ? end : `${start} – ${end}`;
}

/** One valuation day in złoty, x counted in days from the first valuation shown. */
export type HistoryRow = {
	x: number;
	day: string;
	floor: number;
	mid: number;
	ceiling: number;
	spent: number;
};

/** The history as rows of złoty, x in days from its first day. */
export function historyRows(history: HistoryPoint[]): HistoryRow[] {
	const start = history[0] ? dayTimestamp(history[0].day) : 0;
	return history.map((p) => ({
		x: (dayTimestamp(p.day) - start) / dayMilliseconds,
		day: dayFormat.format(new Date(dayTimestamp(p.day))),
		floor: p.floor.cents / 100,
		mid: p.mid.cents / 100,
		ceiling: p.ceiling.cents / 100,
		spent: p.spent_cents / 100
	}));
}

type Plot = { x: (x: number) => number; y: (y: number) => number };

function plot(rows: HistoryRow[], width: number, height: number, inset: number): Plot {
	const xs = rows.map((r) => r.x);
	const ys = rows.flatMap((r) => [r.floor, r.mid, r.ceiling, r.spent]);
	const xMax = Math.max(...xs);
	let yMin = Math.min(...ys);
	let yMax = Math.max(...ys);
	if (yMax === yMin) {
		yMin -= 1;
		yMax += 1;
	}
	const pad = (yMax - yMin) * 0.08;
	yMin -= pad;
	yMax += pad;
	const round = (v: number) => Math.round(v * 10) / 10;
	return {
		x: (x) => round(xMax === 0 ? (x === 0 ? 0 : width) : (x / xMax) * width),
		y: (y) => round(inset + (1 - (y - yMin) / (yMax - yMin)) * (height - 2 * inset))
	};
}

function line(points: [number, number][]): string {
	return points.map(([x, y], i) => `${i === 0 ? 'M' : 'L'}${x} ${y}`).join(' ');
}

/** A single day stretches across the plot, so it draws as a level band and lines. */
function spread(rows: HistoryRow[]): HistoryRow[] {
	const only = rows[0];
	return rows.length === 1 && only ? [only, { ...only, x: 1 }] : rows;
}

/**
 * SVG paths of the history in a width × height box: the band from the buy-back to the new
 * total, the used total and the amount spent. The values fill the height less an inset and an
 * 8% margin, so the lines never touch the edges.
 */
export function historyPaths(history: HistoryPoint[], width: number, height: number, inset = 4) {
	const rows = spread(historyRows(history));
	if (rows.length === 0) return null;
	const p = plot(rows, width, height, inset);
	const at = (pick: (r: HistoryRow) => number) =>
		rows.map((r): [number, number] => [p.x(r.x), p.y(pick(r))]);
	const top = at((r) => r.ceiling);
	const bottom = at((r) => r.floor).reverse();
	return {
		band: `${line(top)} ${line(bottom).replace(/^M/, 'L')} Z`,
		mid: line(at((r) => r.mid)),
		spent: line(at((r) => r.spent))
	};
}

/** A sparkline of the used total in a width × height box, with its last point; null under two days. */
export function sparkline(history: HistoryPoint[], width: number, height: number, inset = 4) {
	if (history.length < 2) return null;
	const rows = historyRows(history);
	const ys = rows.map((r) => r.mid);
	const xMax = Math.max(...rows.map((r) => r.x));
	let yMin = Math.min(...ys);
	let yMax = Math.max(...ys);
	if (yMax === yMin) {
		yMin -= 1;
		yMax += 1;
	}
	const round = (v: number) => Math.round(v * 10) / 10;
	const points = rows.map((r): [number, number] => [
		round(inset + (xMax === 0 ? 0 : r.x / xMax) * (width - 2 * inset)),
		round(inset + (1 - (r.mid - yMin) / (yMax - yMin)) * (height - 2 * inset))
	]);
	const [x, y] = points[points.length - 1] ?? [0, 0];
	return { path: line(points), end: { x, y } };
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

/** The line under a copy's value: its range, else where its used value comes from. */
export function copyNote(c: CopyValue): string {
	return copyRange(c) ?? estimateSource('mid', c.mid);
}

/** A copy's format, or its kind, and what was paid for it: "Winyl · kupiono za 189,99 zł". */
export function copyMeta(c: CopyValue): string {
	const medium = c.format ? formatLabels[c.format] : kindLabels[c.kind];
	return c.price_cents == null ? medium : `${medium} · kupiono za ${formatPLN(c.price_cents)}`;
}
