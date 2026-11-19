import {
	acquiredLabels,
	conditionLabels,
	editionSummary,
	formatPLN,
	kindLabels,
	statusLabels,
	type Copy,
	type TitleDetail
} from '#lib/collection.js';
import { copyCount, editionCount, purchaseDate } from '#lib/polish.js';
import { estimateValue, formatZl, type CopyValue } from '#lib/valuation.js';

export type TitleEdition = TitleDetail['editions'][number];

/** The copy a title page leads with: the only copy of its only edition, else null. */
export function soleCopy(t: TitleDetail): { edition: TitleEdition; copy: Copy } | null {
	const [edition] = t.editions;
	if (t.editions.length !== 1 || !edition || edition.copies.length !== 1) return null;
	const [copy] = edition.copies;
	return copy ? { edition, copy } : null;
}

/** A link to a title or edition page that opens the form for adding a copy once it loads. */
export function addCopyHref(path: string): string {
	return `${path}?add=copy`;
}

/** Whether a page was opened to add a copy, as `addCopyHref` asks. */
export function wantsCopy(params: Pick<URLSearchParams, 'get'>): boolean {
	return params.get('add') === 'copy';
}

/**
 * A page's request to add a copy, read once when the page opens an id and used by at most one
 * answer for it. Refreshing the same id or an answer that arrives after the page closed starts
 * nothing, since the address can still show the request after it was used.
 */
export function copyRequest() {
	let pending: number | undefined;
	return {
		/** Notes whether the page opened for `id` was asked to add a copy. */
		open(id: number, params: Pick<URLSearchParams, 'get'>) {
			pending = wantsCopy(params) ? id : undefined;
		},
		/** Whether the answer for `id` starts adding a copy: true once per request. */
		take(id: number): boolean {
			if (pending !== id) return false;
			pending = undefined;
			return true;
		},
		/** Drops the request when the page closes. */
		close() {
			pending = undefined;
		}
	};
}

/**
 * Where adding a copy to a title starts: a new edition when it has none, the copy form of its only
 * edition, or null when the reader picks one of several editions.
 */
export function addCopyStart(
	t: Pick<TitleDetail, 'editions'>
): { form: 'edition' } | { form: 'copy'; editionID: number } | null {
	const [only] = t.editions;
	if (!only) return { form: 'edition' };
	return t.editions.length === 1 ? { form: 'copy', editionID: only.id } : null;
}

/** An edition's artwork: its own photo, then its catalog cover, then the title's. */
export function editionArtwork(
	e: { cover_override_url?: string; cover_url?: string },
	t: { cover_url?: string }
): string | undefined {
	return e.cover_override_url || e.cover_url || t.cover_url || undefined;
}

/** The hero's artwork: the only edition's, else the title's, else the first edition's. */
export function titleArtwork(t: TitleDetail): string | undefined {
	const [only] = t.editions;
	if (t.editions.length === 1 && only) return editionArtwork(only, t);
	return t.cover_url || t.editions.map((e) => editionArtwork(e, {})).find(Boolean);
}

/** The line above the title, such as "Film · 2014". */
export function kindAndYear(t: Pick<TitleDetail, 'kind' | 'year'>): string {
	return [kindLabels[t.kind], t.year?.toString()].filter(Boolean).join(' · ');
}

/** What an edition is, such as "4K UHD · Slipcover", never with its barcode. */
export function editionName(e: TitleEdition): string {
	return editionSummary({ ...e, barcode: null });
}

/** The line under the title: the only edition's name, or how many editions and copies. */
export function editionsLine(t: TitleDetail): string {
	const [only] = t.editions;
	if (t.editions.length === 1 && only) return editionName(only);
	if (t.editions.length === 0) return 'Brak wydań';
	const copies = t.editions.reduce((n, e) => n + e.copies.length, 0);
	return `${editionCount(t.editions.length)} · ${copyCount(copies)}`;
}

export type CopyFact = { label: string; value: string };

/** A copy's facts for its card: what was paid, when and where, and its state. */
export function copyFacts(c: Copy): CopyFact[] {
	const state =
		[
			c.acquired_as ? acquiredLabels[c.acquired_as] : null,
			c.condition ? conditionLabels[c.condition] : null
		]
			.filter(Boolean)
			.join(' · ') || '—';
	if (c.status === 'wishlist') {
		return [
			{ label: 'Cena maksymalna', value: formatPLN(c.max_price_cents) || '—' },
			{ label: 'Gdzie szukać', value: c.shop || '—' },
			{ label: 'Stan', value: state }
		];
	}
	return [
		{ label: 'Cena', value: formatPLN(c.price_cents) || '—' },
		{ label: 'Kupiono', value: purchaseDate(c.purchased_on) || '—' },
		{ label: 'Sklep', value: c.shop || '—' },
		{ label: 'Stan', value: state }
	];
}

/** A copy's row in an edition card, such as "Na półce · 89,99 zł · Empik" or "Lista życzeń · do 60,00 zł". */
export function copyLine(c: Copy): string {
	const price =
		c.status === 'owned'
			? formatPLN(c.price_cents)
			: c.max_price_cents == null
				? ''
				: `do ${formatPLN(c.max_price_cents)}`;
	return [statusLabels[c.status], price, c.shop].filter(Boolean).join(' · ');
}

export type ValueCard = {
	/** Each bound as `/valuation` writes it, a dash without prices. */
	used: string;
	floor: string;
	ceiling: string;
	/** Where the used value and the price paid sit on the bar from buy-back to new, 0–100. */
	usedAt: number | null;
	paidAt: number | null;
	/** The used value against the price paid in whole złoty, such as "−16 zł względem zakupu". */
	delta: string | null;
};

/**
 * The "Wartość dziś" card of an owned copy, or null when the valuation has no price for it. The
 * bar runs from the buy-back offer (0 without one) to the new price, or to the higher of the used
 * value and the price paid when no shop sells it new; marks outside it sit at its ends. Without a
 * used value there is no used mark and no difference from the price paid.
 */
export function valueCard(c: Copy, values: readonly CopyValue[]): ValueCard | null {
	if (c.status !== 'owned') return null;
	const v = values.find((value) => value.copy_id === c.id);
	if (!v || (!v.floor && !v.mid && !v.ceiling)) return null;
	const mid = v.mid?.cents ?? null;
	const paid = c.price_cents;
	const start = v.floor?.cents ?? 0;
	const end = v.ceiling?.cents ?? Math.max(mid ?? 0, paid ?? 0);
	const at = (cents: number) =>
		end > start ? Math.min(100, Math.max(0, ((cents - start) / (end - start)) * 100)) : 50;
	return {
		used: estimateValue(v.mid),
		floor: estimateValue(v.floor),
		ceiling: estimateValue(v.ceiling),
		usedAt: mid == null ? null : at(mid),
		paidAt: paid == null ? null : at(paid),
		delta: paid == null || mid == null ? null : `${signedZl(mid - paid)} względem zakupu`
	};
}

/** Grosze in whole złoty with a sign: "+12 zł", "−16 zł" or "0 zł". */
function signedZl(cents: number): string {
	const zl = Math.round(Math.abs(cents) / 100);
	const sign = zl === 0 ? '' : cents > 0 ? '+' : '−';
	return `${sign}${formatZl(zl)}`;
}
