import type { AcquiredAs, CollectionItem } from './api.gen';

/** The fields of a `GET /api/collection` item the statistics read. */
export type StatsItem = Pick<
	CollectionItem,
	'status' | 'price_cents' | 'purchased_on' | 'acquired_as' | 'created_at_us'
>;

/** How many months the spending and growth charts cover, the current one last. */
export const monthSpan = 12;

const warsawParts = new Intl.DateTimeFormat('en-CA', {
	timeZone: 'Europe/Warsaw',
	year: 'numeric',
	month: '2-digit',
	day: '2-digit'
});

/** The calendar day in Poland at an instant, as YYYY-MM-DD. */
export function warsawDay(at: Date): string {
	const part = (type: Intl.DateTimeFormatPartTypes) =>
		warsawParts.formatToParts(at).find((p) => p.type === type)?.value ?? '';
	return `${part('year')}-${part('month')}-${part('day')}`;
}

/** The YYYY-MM month of a YYYY-MM-DD day. */
function monthOf(day: string): string {
	return day.slice(0, 7);
}

/** The `count` calendar months in Poland up to now's, oldest first, as YYYY-MM. */
export function lastMonths(now: Date, count = monthSpan): string[] {
	const [year, month] = monthOf(warsawDay(now)).split('-').map(Number);
	const months: string[] = [];
	for (let back = count - 1; back >= 0; back--) {
		const d = new Date(Date.UTC(year, month - 1 - back, 1));
		months.push(`${d.getUTCFullYear()}-${String(d.getUTCMonth() + 1).padStart(2, '0')}`);
	}
	return months;
}

/** The copies on the shelf: wishes, orders and sold copies count in no statistic. */
export function ownedCopies<T extends Pick<StatsItem, 'status'>>(items: readonly T[]): T[] {
	return items.filter((item) => item.status === 'owned');
}

/** Copies bought in one month, or without a date: their price sum and how many had none. */
export type Spend = { cents: number; copies: number; unpriced: number };
export type MonthSpend = Spend & { month: string };
export type Spending = { months: MonthSpend[]; undated: Spend };

function add(spend: Spend, priceCents: number | null) {
	spend.copies += 1;
	if (priceCents == null) spend.unpriced += 1;
	else spend.cents += priceCents;
}

/**
 * The price paid for owned copies in each of the last 12 months in Poland by purchase date, and
 * one figure for the copies without a date. An unpriced copy counts but adds nothing.
 */
export function monthlySpending(items: readonly StatsItem[], now: Date): Spending {
	const months = lastMonths(now).map((month) => ({ month, cents: 0, copies: 0, unpriced: 0 }));
	const byMonth = new Map(months.map((m) => [m.month, m]));
	const undated: Spend = { cents: 0, copies: 0, unpriced: 0 };
	for (const item of ownedCopies(items)) {
		if (!item.purchased_on) {
			add(undated, item.price_cents);
			continue;
		}
		const spend = byMonth.get(monthOf(item.purchased_on));
		if (spend) add(spend, item.price_cents);
	}
	return { months, undated };
}

/** The day a copy joined the collection: its purchase date, else the day it was added to Ohara. */
export function joinedOn(item: Pick<StatsItem, 'purchased_on' | 'created_at_us'>): string {
	return item.purchased_on || warsawDay(new Date(item.created_at_us / 1000));
}

export type MonthCount = { month: string; copies: number };

/**
 * How many owned copies the collection held at the end of each of the last 12 months. A copy
 * dated after this month counts in it, so the last month holds every copy on the shelf.
 */
export function collectionGrowth(items: readonly StatsItem[], now: Date): MonthCount[] {
	const months = lastMonths(now);
	const current = months[months.length - 1];
	const joined = ownedCopies(items).map((item) => {
		const month = monthOf(joinedOn(item));
		return month > current ? current : month;
	});
	return months.map((month) => ({
		month,
		copies: joined.filter((m) => m <= month).length
	}));
}

export type AcquiredKey = AcquiredAs | 'unknown';

export const acquiredKeys: AcquiredKey[] = ['new', 'used', 'unknown'];

export const acquiredShareLabels: Record<AcquiredKey, string> = {
	new: 'Nowe',
	used: 'Używane',
	unknown: 'Nie zapisano'
};

/** One way copies were bought: how many and how much they cost, with their whole percent shares. */
export type Share = {
	key: AcquiredKey;
	copies: number;
	cents: number;
	copyPercent: number;
	/** Null when no owned copy has a price, so money has no shares. */
	centsPercent: number | null;
};

/**
 * Whole percents of `values` that add up to 100, the largest remainders rounded up; all 0 when
 * the values sum to 0.
 */
export function percents(values: readonly number[]): number[] {
	const total = values.reduce((sum, v) => sum + v, 0);
	if (total <= 0) return values.map(() => 0);
	const exact = values.map((v) => (v * 100) / total);
	const out = exact.map(Math.floor);
	let left = 100 - out.reduce((sum, v) => sum + v, 0);
	const order = exact
		.map((v, i) => ({ i, rest: v - Math.floor(v) }))
		.sort((a, b) => b.rest - a.rest || a.i - b.i);
	for (const { i } of order) {
		if (left <= 0) break;
		out[i] += 1;
		left -= 1;
	}
	return out;
}

/** The owned copies bought new, used and with nothing recorded, by count and by money paid. */
export function acquiredShare(items: readonly StatsItem[]): Share[] {
	const rows = acquiredKeys.map((key) => ({ key, copies: 0, cents: 0 }));
	const byKey = new Map(rows.map((r) => [r.key, r]));
	for (const item of ownedCopies(items)) {
		const row = byKey.get(item.acquired_as ?? 'unknown');
		if (!row) continue;
		row.copies += 1;
		row.cents += item.price_cents ?? 0;
	}
	const copyPercents = percents(rows.map((r) => r.copies));
	const paid = rows.some((r) => r.cents > 0);
	const centsPercents = percents(rows.map((r) => r.cents));
	return rows.map((r, i) => ({
		...r,
		copyPercent: copyPercents[i],
		centsPercent: paid ? centsPercents[i] : null
	}));
}

const monthName = new Intl.DateTimeFormat('pl-PL', {
	month: 'long',
	year: 'numeric',
	timeZone: 'UTC'
});
const monthShort = new Intl.DateTimeFormat('pl-PL', { month: 'short', timeZone: 'UTC' });

function monthDate(month: string): Date {
	const [year, m] = month.split('-').map(Number);
	return new Date(Date.UTC(year, m - 1, 1));
}

/** A YYYY-MM month as "styczeń 2026". */
export function monthLabel(month: string): string {
	return monthName.format(monthDate(month));
}

/** A YYYY-MM month as the chart axis's "sty". */
export function monthTick(month: string): string {
	return monthShort.format(monthDate(month));
}

const zl = new Intl.NumberFormat('pl-PL', { maximumFractionDigits: 0 });

/** Grosze as whole złoty for a chart's figures, "1 235 zł". */
export function roundZl(cents: number): string {
	return `${zl.format(Math.round(cents / 100))} zł`;
}

/** A bar of a chart in its viewBox: x and width, then y from the top and height. */
export type Bar = { x: number; y: number; width: number; height: number };

/**
 * One bar per value, side by side across `width`, rising from the bottom of `height` in
 * proportion to the largest value; a 0 gets no height.
 */
export function bars(values: readonly number[], width: number, height: number, gap = 4): Bar[] {
	const max = Math.max(0, ...values);
	const slot = values.length > 0 ? width / values.length : 0;
	return values.map((v, i) => {
		const h = max > 0 ? (Math.max(0, v) / max) * height : 0;
		return { x: i * slot + gap / 2, y: height - h, width: Math.max(0, slot - gap), height: h };
	});
}

/**
 * The line through the values at the middle of each month's slot across `width`, from 0 at the
 * bottom of `height` to the largest value at its top, and the area under it, as SVG paths; null
 * without values.
 */
export function growthPaths(
	values: readonly number[],
	width: number,
	height: number,
	inset = 4
): { line: string; area: string } | null {
	if (values.length === 0) return null;
	const max = Math.max(0, ...values);
	const slot = width / values.length;
	const usable = height - inset * 2;
	const points = values.map((v, i) => {
		const x = round(i * slot + slot / 2);
		const y = round(max > 0 ? inset + usable - (v / max) * usable : height - inset);
		return `${x},${y}`;
	});
	const line = `M${points.join('L')}`;
	const first = round(slot / 2);
	const last = round((values.length - 1) * slot + slot / 2);
	const area = `${line}L${last},${height}L${first},${height}Z`;
	return { line, area };
}

function round(n: number): number {
	return Math.round(n * 100) / 100;
}
