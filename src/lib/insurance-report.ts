import {
	bindingLabels,
	conditionLabels,
	formatLabels,
	formatPLN,
	kindLabels
} from '#lib/collection.js';
import type { CopyValue, Estimate } from '#lib/valuation.js';

/**
 * The insurance report: every owned copy as `GET /api/valuation` values it, with the condition,
 * creators and binding read in the same snapshot, and totals that say how many copies each one
 * covers. The values are the ones Wartość shows; the report only prints them.
 */

export const reportTitle = 'Raport dla ubezpieczyciela';

/** What a cell shows for a bound without prices. */
export const unvalued = 'brak wyceny';

/**
 * The most characters of creators a printed row holds. A longer list is cut there and printed
 * in full in the report's appendix, so no row grows past an A4 page.
 */
export const creatorsLimit = 160;

/** One printed row: the valued copy and its labels. */
export type ReportRow = {
	copy: CopyValue;
	/** The title's authors or artists, comma separated and cut at creatorsLimit; empty for none. */
	creators: string;
	/** The whole list when creators was cut, for the appendix; null otherwise. */
	fullCreators: string | null;
	/** The kind with the format, or a book's binding: "Film · Blu-ray", "Książka · Twarda oprawa". */
	edition: string;
	/** The copy's condition label; empty when it has none. */
	condition: string;
};

/** One total: the sum and how many copies have it. */
export type ReportTotal = { cents: number; copies: number };

export type ReportTotals = {
	copies: number;
	paid: ReportTotal;
	mid: ReportTotal;
	ceiling: ReportTotal;
};

function edition(c: CopyValue): string {
	const kind = kindLabels[c.kind];
	if (c.format) return `${kind} · ${formatLabels[c.format]}`;
	if (c.binding) return `${kind} · ${bindingLabels[c.binding]}`;
	return kind;
}

/** The creators as printed in a row: whole up to creatorsLimit, else cut with "…". */
export function creatorsCell(authors: readonly string[]): {
	text: string;
	full: string | null;
} {
	const full = authors.join(', ');
	const chars = [...full];
	if (chars.length <= creatorsLimit) return { text: full, full: null };
	return { text: `${chars.slice(0, creatorsLimit - 1).join('')}…`, full };
}

/** The report's rows in the valuation's order. */
export function reportRows(copies: readonly CopyValue[]): ReportRow[] {
	return copies.map((copy) => {
		const creators = creatorsCell(copy.authors);
		return {
			copy,
			creators: creators.text,
			fullCreators: creators.full,
			edition: edition(copy),
			condition: copy.condition ? conditionLabels[copy.condition] : ''
		};
	});
}

/**
 * Sums what was paid and the used and new values over the copies, each with the number of copies
 * that have it. A price of 0 zł counts as paid; a missing bound counts in no total.
 */
export function reportTotals(
	copies: readonly Pick<CopyValue, 'price_cents' | 'mid' | 'ceiling'>[]
): ReportTotals {
	const totals: ReportTotals = {
		copies: copies.length,
		paid: { cents: 0, copies: 0 },
		mid: { cents: 0, copies: 0 },
		ceiling: { cents: 0, copies: 0 }
	};
	const add = (t: ReportTotal, cents: number | null | undefined) => {
		if (cents == null) return;
		t.cents += cents;
		t.copies++;
	};
	for (const c of copies) {
		add(totals.paid, c.price_cents);
		add(totals.mid, c.mid?.cents);
		add(totals.ceiling, c.ceiling?.cents);
	}
	return totals;
}

/** A bound's cell: the amount with grosze, or "brak wyceny". */
export function boundCell(e: Estimate | null): string {
	return e ? formatPLN(e.cents) : unvalued;
}

/**
 * The Discogs grade a used value was priced at, "VG+", when it is a Discogs suggestion; null
 * otherwise. Printed beside the amount, since a copy without a suggestion for its own condition
 * is priced at Very Good Plus.
 */
export function midGrade(c: Pick<CopyValue, 'mid' | 'mid_grade'>): string | null {
	return c.mid?.source === 'discogs' && c.mid_grade ? c.mid_grade : null;
}

/** A price paid: the amount with grosze, or a dash when none was recorded. */
export function paidCell(cents: number | null): string {
	return cents == null ? '—' : formatPLN(cents);
}

/** How many of all copies a total covers: "3 z 5 egzemplarzy", "0 z 1 egzemplarza". */
export function coverage(t: ReportTotal, all: number): string {
	return `${t.copies} z ${all} ${all === 1 ? 'egzemplarza' : 'egzemplarzy'}`;
}

/** A total's figure: the sum, or empty (by default "brak wyceny") when no copy has it. */
export function totalCell(t: ReportTotal, empty = unvalued): string {
	return t.copies === 0 ? empty : formatPLN(t.cents);
}

const longDay = new Intl.DateTimeFormat('pl-PL', {
	day: 'numeric',
	month: 'long',
	year: 'numeric',
	timeZone: 'UTC'
});

/** A day (YYYY-MM-DD) as "17 listopada 2026". */
export function reportDay(day: string): string {
	return longDay.format(new Date(`${day}T00:00:00Z`));
}

/**
 * The report's dating: the last valuation run's day, and that the amounts are the prices of the
 * last windowDays days up to today, since the page values copies from the live window.
 */
export function reportDating(lastValuation: string, today: string, windowDays: number): string {
	return `Ostatnia wycena: ${reportDay(lastValuation)}. Kwoty według cen z ostatnich ${windowDays} dni, stan na ${reportDay(today)}.`;
}

/** The part of an image element imagesSettled reads. */
export type Settling = Pick<HTMLImageElement, 'complete' | 'addEventListener'>;

/** Resolves once every image has loaded or failed; at once for those already complete. */
export function imagesSettled(images: Iterable<Settling>): Promise<void> {
	const waits = [...images]
		.filter((img) => !img.complete)
		.map(
			(img) =>
				new Promise<void>((resolve) => {
					img.addEventListener('load', () => resolve(), { once: true });
					img.addEventListener('error', () => resolve(), { once: true });
				})
		);
	return Promise.all(waits).then(() => undefined);
}
