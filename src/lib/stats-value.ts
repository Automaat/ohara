import type { CopyValue, Format, Kind } from './api.gen';
import { formatPLN } from '#lib/collection.js';
import { discogsReleaseUrl } from '#lib/discogs.js';
import { copyCount } from '#lib/polish.js';

/** The fields of a `GET /api/valuation` copy the value statistics read. */
export type ValueItem = Pick<
	CopyValue,
	'kind' | 'format' | 'price_cents' | 'mid' | 'low_confidence' | 'discogs_release_id'
>;

/** A group of owned copies: a kind, or a format with books as one group. */
export type ValueKey = Kind | Format | 'none';

/** The owned copies of one group: the used value of the last valuation and the price paid. */
export type ValueGroup = {
	key: ValueKey;
	label: string;
	copies: number;
	/** The summed used (mid) value of the copies that have one. */
	midCents: number;
	/** How many copies have no used value. */
	unvalued: number;
	/** How many copies have a used value resting on too few prices. */
	lowConfidence: number;
	/** The summed price paid, 0 zł included. */
	paidCents: number;
	/** How many copies have no purchase price. */
	unpriced: number;
	/** The Discogs releases whose suggestions priced copies of the group, each once. */
	discogsReleases: number[];
};

export const kindGroupLabels: Record<Kind, string> = {
	movie: 'Filmy',
	tv: 'Seriale',
	book: 'Książki',
	music: 'Muzyka'
};

export const formatGroupLabels: Record<Format | 'book' | 'none', string> = {
	dvd: 'DVD',
	bluray: 'Blu-ray',
	uhd: '4K UHD',
	book: 'Książki',
	audiobook: 'Audiobooki',
	cd: 'CD',
	vinyl: 'Winyl',
	cassette: 'Kaseta',
	none: 'Bez nośnika'
};

const kindOrder: Kind[] = ['movie', 'tv', 'book', 'music'];
const formatOrder: (Format | 'book' | 'none')[] = [
	'dvd',
	'bluray',
	'uhd',
	'book',
	'audiobook',
	'cd',
	'vinyl',
	'cassette',
	'none'
];

function groupBy<K extends ValueKey>(
	copies: readonly ValueItem[],
	order: readonly K[],
	labels: Record<K, string>,
	keyOf: (c: ValueItem) => K
): ValueGroup[] {
	const groups = order.map((key) => ({
		key,
		label: labels[key],
		copies: 0,
		midCents: 0,
		unvalued: 0,
		lowConfidence: 0,
		paidCents: 0,
		unpriced: 0,
		discogsReleases: [] as number[]
	}));
	const byKey = new Map<ValueKey, ValueGroup>(groups.map((g) => [g.key, g]));
	for (const c of copies) {
		const g = byKey.get(keyOf(c));
		if (!g) continue;
		g.copies += 1;
		if (c.mid) {
			g.midCents += c.mid.cents;
			if (c.low_confidence) g.lowConfidence += 1;
			const release = c.mid.source === 'discogs' ? c.discogs_release_id : null;
			if (release != null && discogsReleaseUrl(release) && !g.discogsReleases.includes(release)) {
				g.discogsReleases.push(release);
			}
		} else {
			g.unvalued += 1;
		}
		if (c.price_cents == null) g.unpriced += 1;
		else g.paidCents += c.price_cents;
	}
	const rank = new Map<ValueKey, number>(order.map((key, i) => [key, i]));
	return groups
		.filter((g) => g.copies > 0)
		.sort((a, b) => b.midCents - a.midCents || (rank.get(a.key) ?? 0) - (rank.get(b.key) ?? 0));
}

/** The valued copies per kind, the most valuable first; kinds without copies are left out. */
export function valueByKind(copies: readonly ValueItem[]): ValueGroup[] {
	return groupBy(copies, kindOrder, kindGroupLabels, (c) => c.kind);
}

/**
 * The valued copies per format, the most valuable first, print books (which have no format) as
 * one group and audiobooks as another; formats without copies are left out.
 */
export function valueByFormat(copies: readonly ValueItem[]): ValueGroup[] {
	return groupBy(copies, formatOrder, formatGroupLabels, (c) =>
		c.format ? c.format : c.kind === 'book' ? 'book' : 'none'
	);
}

/** A group's bar length in percent of the most valuable group's, 0 when none has a value. */
export function valueShare(group: ValueGroup, groups: readonly ValueGroup[]): number {
	const max = Math.max(0, ...groups.map((g) => g.midCents));
	return max > 0 ? Math.round((group.midCents / max) * 100) : 0;
}

/** A group's used value, or "brak wyceny" when none of its copies has one. */
export function groupValue(group: ValueGroup): string {
	return group.unvalued < group.copies ? formatPLN(group.midCents) : 'brak wyceny';
}

/**
 * What a group's value rests on: its copies, the price paid with the unpriced ones, and how many
 * have no used value or one from too few prices.
 */
export function groupNotes(group: ValueGroup): string[] {
	const paid = `zapłacono ${formatPLN(group.paidCents)}`;
	const notes = [
		copyCount(group.copies),
		group.unpriced > 0 ? `${paid} (bez ceny: ${group.unpriced})` : paid
	];
	if (group.unvalued > 0) notes.push(`bez wyceny: ${group.unvalued}`);
	if (group.lowConfidence > 0) notes.push(`mało danych: ${group.lowConfidence}`);
	return notes;
}
