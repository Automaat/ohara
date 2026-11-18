import type { ChartSeries } from '@smykla-skalski/sui';
import { formatPLN, type Format, type Kind } from '#lib/collection.js';

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

/** A collection bound with its coverage, including observed zero offers. */
export function boundTotal(b: Bound, copies: number): string {
	const amount = b.copies === 0 ? 'Brak wyceny' : formatPLN(b.cents);
	return `${amount} (${b.copies} z ${copies} egz.)`;
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

/** The valuation series in złoty, with x measured in days from the first valuation. */
export function historyChart(history: HistoryPoint[]) {
	const start = history[0] ? dayTimestamp(history[0].day) : 0;
	const series = (id: string, label: string, cents: (p: HistoryPoint) => number): ChartSeries => ({
		id,
		label,
		points: history.map((p) => ({
			x: (dayTimestamp(p.day) - start) / dayMilliseconds,
			y: cents(p) / 100
		}))
	});
	return {
		series: [
			...boundKeys.map((k) => series(k, boundLabels[k], (p) => p[k].cents)),
			series('spent', 'Wydano', (p) => p.spent_cents)
		],
		formatDay: (offset: number) =>
			history.length === 1 && offset !== 0
				? ''
				: dayFormat.format(new Date(start + offset * dayMilliseconds))
	};
}

const zl = new Intl.NumberFormat('pl-PL', { maximumFractionDigits: 0 });

/** A chart value in złoty as "1 234 zł". */
export function formatZl(value: number): string {
	return `${zl.format(value)} zł`;
}
