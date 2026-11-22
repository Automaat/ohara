import {
	bindingLabels,
	conditionLabels,
	formatLabels,
	formatPLN,
	kindLabels,
	type CollectionItem
} from '#lib/collection.js';
import type { CopyValue, Estimate } from '#lib/valuation.js';

/**
 * The insurance report: every owned copy as `GET /api/valuation` values it, with the condition,
 * creators and binding `GET /api/collection` knows for it, and totals that say how many copies
 * each one covers. The values are the ones Wartość shows; the report only prints them.
 */

export const reportTitle = 'Raport dla ubezpieczyciela';

/** What a cell shows for a bound without prices. */
export const unvalued = 'brak wyceny';

/** One printed row: the valued copy and what the collection adds to it. */
export type ReportRow = {
	copy: CopyValue;
	/** The title's authors or artists, comma separated; empty when it names none. */
	creators: string;
	/** The kind with the format, or a book's binding: "Film · Blu-ray", "Książka · Twarda oprawa". */
	edition: string;
	/** The copy's condition label; empty when it has none. */
	condition: string;
};

/** The collection item fields a row reads. */
export type ReportItem = Pick<CollectionItem, 'copy_id' | 'authors' | 'binding' | 'condition'>;

/** One total: the sum and how many copies have it. */
export type ReportTotal = { cents: number; copies: number };

export type ReportTotals = {
	copies: number;
	paid: ReportTotal;
	mid: ReportTotal;
	ceiling: ReportTotal;
};

function edition(c: CopyValue, item: ReportItem | undefined): string {
	const kind = kindLabels[c.kind];
	if (c.format) return `${kind} · ${formatLabels[c.format]}`;
	if (item?.binding) return `${kind} · ${bindingLabels[item.binding]}`;
	return kind;
}

/**
 * The report's rows in the valuation's order. A copy the collection read does not hold (sold or
 * deleted between the two reads) keeps its row without condition or creators.
 */
export function reportRows(
	copies: readonly CopyValue[],
	items: readonly ReportItem[]
): ReportRow[] {
	const byCopy = new Map(items.map((i) => [i.copy_id, i]));
	return copies.map((copy) => {
		const item = byCopy.get(copy.copy_id);
		return {
			copy,
			creators: item?.authors?.join(', ') ?? '',
			edition: edition(copy, item),
			condition: item?.condition ? conditionLabels[item.condition] : ''
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
