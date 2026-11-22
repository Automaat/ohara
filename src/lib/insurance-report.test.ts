import { describe, expect, it } from 'vitest';
import {
	boundCell,
	coverage,
	paidCell,
	reportDay,
	reportRows,
	reportTotals,
	totalCell,
	unvalued,
	type ReportItem
} from './insurance-report.js';
import type { CopyValue, Estimate } from './valuation.js';

const olx = (cents: number): Estimate => ({ cents, source: 'olx', count: 4, trimmed: 0 });
const discogs = (cents: number): Estimate => ({ cents, source: 'discogs', count: 1, trimmed: 0 });

function copy(over: Partial<CopyValue> = {}): CopyValue {
	return {
		copy_id: 1,
		edition_id: 1,
		title_id: 1,
		title_name: 'Diuna',
		kind: 'movie',
		format: 'bluray',
		barcode: '5901234123457',
		price_cents: null,
		floor: null,
		mid: null,
		ceiling: null,
		low_confidence: true,
		likely_sold: null,
		...over
	};
}

/** Strips the narrow and plain no-break spaces Intl puts in Polish amounts. */
const plain = (s: string) => s.replace(/[  ]/g, ' ');

describe('reportTotals', () => {
	it('sums every copy when all are valued', () => {
		const t = reportTotals([
			copy({ price_cents: 4000, mid: olx(5000), ceiling: discogs(9000) }),
			copy({ price_cents: 2500, mid: discogs(3000), ceiling: discogs(4500) })
		]);
		expect(t).toEqual({
			copies: 2,
			paid: { cents: 6500, copies: 2 },
			mid: { cents: 8000, copies: 2 },
			ceiling: { cents: 13500, copies: 2 }
		});
	});

	it('counts only the copies that have each figure when some are partly valued', () => {
		const t = reportTotals([
			copy({ price_cents: 4000, mid: olx(5000) }),
			copy({ price_cents: 0, mid: discogs(3000), ceiling: discogs(4500) }),
			copy()
		]);
		expect(t).toEqual({
			copies: 3,
			paid: { cents: 4000, copies: 2 },
			mid: { cents: 8000, copies: 2 },
			ceiling: { cents: 4500, copies: 1 }
		});
	});

	it('covers no copy when none is valued or priced', () => {
		const t = reportTotals([copy(), copy({ copy_id: 2 })]);
		expect(t).toEqual({
			copies: 2,
			paid: { cents: 0, copies: 0 },
			mid: { cents: 0, copies: 0 },
			ceiling: { cents: 0, copies: 0 }
		});
		expect(totalCell(t.mid)).toBe(unvalued);
		expect(totalCell(t.ceiling)).toBe(unvalued);
		expect(totalCell(t.paid, '—')).toBe('—');
		expect(coverage(t.mid, t.copies)).toBe('0 z 2 egzemplarzy');
	});

	it('is empty for no copies', () => {
		expect(reportTotals([])).toEqual({
			copies: 0,
			paid: { cents: 0, copies: 0 },
			mid: { cents: 0, copies: 0 },
			ceiling: { cents: 0, copies: 0 }
		});
	});
});

describe('coverage', () => {
	it.each([
		[{ cents: 0, copies: 5 }, 5, '5 z 5 egzemplarzy'],
		[{ cents: 0, copies: 1 }, 1, '1 z 1 egzemplarza'],
		[{ cents: 0, copies: 0 }, 1, '0 z 1 egzemplarza'],
		[{ cents: 0, copies: 3 }, 5, '3 z 5 egzemplarzy'],
		[{ cents: 0, copies: 1 }, 2, '1 z 2 egzemplarzy'],
		[{ cents: 0, copies: 2 }, 22, '2 z 22 egzemplarzy']
	])('names how many of all copies %o covers of %i', (t, all, want) => {
		expect(coverage(t, all)).toBe(want);
	});
});

describe('cells', () => {
	it('shows "brak wyceny" for a missing bound and the amount with grosze otherwise', () => {
		expect(boundCell(null)).toBe('brak wyceny');
		expect(plain(boundCell(olx(1234567)))).toBe('12 345,67 zł');
	});

	it('shows a dash for a price never recorded and 0 zł for a gift', () => {
		expect(paidCell(null)).toBe('—');
		expect(plain(paidCell(0))).toBe('0,00 zł');
	});

	it('shows a total with grosze', () => {
		expect(plain(totalCell({ cents: 1999, copies: 1 }))).toBe('19,99 zł');
	});
});

describe('reportRows', () => {
	const items: ReportItem[] = [
		{ copy_id: 1, authors: ['Denis Villeneuve'], binding: null, condition: 'very_good' },
		{
			copy_id: 2,
			authors: ['Frank Herbert', 'Brian Herbert'],
			binding: 'hardcover',
			condition: null
		},
		{ copy_id: 3, binding: null, condition: 'mint' }
	];

	it('joins each copy with its creators, condition and edition, in the valuation order', () => {
		const rows = reportRows(
			[
				copy({ copy_id: 2, kind: 'book', format: null }),
				copy({ copy_id: 1 }),
				copy({ copy_id: 3, kind: 'music', format: 'vinyl' })
			],
			items
		);
		expect(rows.map((r) => [r.copy.copy_id, r.creators, r.edition, r.condition])).toEqual([
			[2, 'Frank Herbert, Brian Herbert', 'Książka · Twarda oprawa', ''],
			[1, 'Denis Villeneuve', 'Film · Blu-ray', 'Bardzo dobry'],
			[3, '', 'Muzyka · Winyl', 'Idealny']
		]);
	});

	it('keeps a copy the collection no longer lists, without its details', () => {
		const rows = reportRows([copy({ copy_id: 9, kind: 'book', format: null })], items);
		expect(rows).toHaveLength(1);
		expect(rows[0]).toMatchObject({ creators: '', edition: 'Książka', condition: '' });
	});
});

describe('reportDay', () => {
	it('writes a valuation day in full', () => {
		expect(reportDay('2026-11-17')).toBe('17 listopada 2026');
	});
});
