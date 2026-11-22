import { describe, expect, it } from 'vitest';
import {
	boundCell,
	coverage,
	creatorsCell,
	creatorsLimit,
	imagesSettled,
	midGrade,
	paidCell,
	reportDating,
	reportDay,
	reportRows,
	reportTotals,
	totalCell,
	unvalued,
	type Settling
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
		condition: null,
		authors: [],
		binding: null,
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

	it('names the Discogs grade of a used value, and none for an OLX one', () => {
		expect(midGrade({ mid: discogs(12000), mid_grade: 'VG+' })).toBe('VG+');
		expect(midGrade({ mid: olx(12000), mid_grade: undefined })).toBeNull();
		expect(midGrade({ mid: null, mid_grade: undefined })).toBeNull();
	});
});

describe('creatorsCell', () => {
	it('keeps a list up to the limit whole', () => {
		expect(creatorsCell(['Frank Herbert', 'Brian Herbert'])).toEqual({
			text: 'Frank Herbert, Brian Herbert',
			full: null
		});
		expect(creatorsCell([])).toEqual({ text: '', full: null });
		const exact = 'ż'.repeat(creatorsLimit);
		expect(creatorsCell([exact])).toEqual({ text: exact, full: null });
	});

	it('cuts a longer list at the limit and keeps the whole one for the appendix', () => {
		const names = Array.from({ length: 20 }, (_, i) => `${'Ł'.repeat(490)} ${i}`);
		const cell = creatorsCell(names);
		expect([...cell.text]).toHaveLength(creatorsLimit);
		expect(cell.text.endsWith('…')).toBe(true);
		expect(cell.full).toBe(names.join(', '));
	});
});

describe('reportRows', () => {
	it('labels each copy with its creators, condition and edition, in the valuation order', () => {
		const rows = reportRows([
			copy({
				copy_id: 2,
				kind: 'book',
				format: null,
				binding: 'hardcover',
				authors: ['Frank Herbert', 'Brian Herbert']
			}),
			copy({ copy_id: 1, condition: 'very_good' }),
			copy({ copy_id: 3, kind: 'music', format: 'vinyl', condition: 'mint', authors: ['Björk'] })
		]);
		expect(rows.map((r) => [r.copy.copy_id, r.creators, r.edition, r.condition])).toEqual([
			[2, 'Frank Herbert, Brian Herbert', 'Książka · Twarda oprawa', ''],
			[1, '', 'Film · Blu-ray', 'Bardzo dobry'],
			[3, 'Björk', 'Muzyka · Winyl', 'Idealny']
		]);
		expect(rows.every((r) => r.fullCreators === null)).toBe(true);
	});

	it('names only the kind of a book without a binding', () => {
		expect(reportRows([copy({ kind: 'book', format: null })])[0].edition).toBe('Książka');
	});
});

describe('dates', () => {
	it('writes a valuation day in full', () => {
		expect(reportDay('2026-11-17')).toBe('17 listopada 2026');
	});

	it('tells the last run apart from the day the amounts stand for', () => {
		expect(reportDating('2026-11-17', '2026-11-22', 90)).toBe(
			'Ostatnia wycena: 17 listopada 2026. Kwoty według cen z ostatnich 90 dni, stan na 22 listopada 2026.'
		);
	});
});

/** An image stand-in that settles when told to. */
function image(complete: boolean): Settling & { fire: (type: 'load' | 'error') => void } {
	const target = new EventTarget();
	return {
		complete,
		addEventListener: target.addEventListener.bind(target) as Settling['addEventListener'],
		fire: (type) => target.dispatchEvent(new Event(type))
	};
}

describe('imagesSettled', () => {
	it('resolves at once when every image is complete', async () => {
		await expect(imagesSettled([image(true), image(true)])).resolves.toBeUndefined();
	});

	it('waits until each pending image loads or fails', async () => {
		const loading = image(false);
		const failing = image(false);
		let done = false;
		const settled = imagesSettled([image(true), loading, failing]).then(() => {
			done = true;
		});
		loading.fire('load');
		await Promise.resolve();
		expect(done).toBe(false);
		failing.fire('error');
		await settled;
		expect(done).toBe(true);
	});
});
