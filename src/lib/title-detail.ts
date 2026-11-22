import type { TitlePreview } from '#lib/collection-cache.js';
import {
	acquiredLabels,
	conditionLabels,
	displayName,
	editionSummary,
	formatPLN,
	kindLabels,
	statusLabels,
	type Copy,
	type FinishPayload,
	type FormIssue,
	type Kind,
	type Parsed,
	type Title,
	type TitleDetail
} from '#lib/collection.js';
import { copyCount, editionCount, purchaseDate } from '#lib/polish.js';
import {
	copySparkline,
	estimateValue,
	formatZl,
	type CopyValue,
	type CopyValueDay
} from '#lib/valuation.js';

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

/**
 * Whether a navigation opened a title page from the collection, so the page's way back can step
 * back in history and find the collection as the reader left it.
 */
export function openedFromCollection(navigation: {
	type: string;
	from: { url: URL } | null;
}): boolean {
	return navigation.type === 'link' && navigation.from?.url.pathname === '/';
}

/** Whether a click is a plain primary click, not one that opens a link in a new tab or window. */
export function plainClick(
	event: Pick<
		MouseEvent,
		'button' | 'metaKey' | 'ctrlKey' | 'shiftKey' | 'altKey' | 'defaultPrevented'
	>
): boolean {
	return (
		!event.defaultPrevented &&
		event.button === 0 &&
		!event.metaKey &&
		!event.ctrlKey &&
		!event.shiftKey &&
		!event.altKey
	);
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

/** What a finished title is for its kind: watched, read or heard. */
export const finishedLabels: Record<Kind, string> = {
	movie: 'Obejrzane',
	book: 'Przeczytane',
	music: 'Przesłuchane'
};

/** The action that marks a title finished today, such as "Oznacz jako obejrzane". */
export function finishAction(kind: Kind): string {
	return `Oznacz jako ${finishedLabels[kind].toLocaleLowerCase('pl-PL')}`;
}

/**
 * A finished title's line, such as "Obejrzane 12 maj 2026", or its label alone when the day is
 * unknown; null while it is not finished.
 */
export function finishedLine(t: Pick<Title, 'kind' | 'finished' | 'finished_on'>): string | null {
	if (!t.finished) return null;
	return [finishedLabels[t.kind], purchaseDate(t.finished_on ?? null)].filter(Boolean).join(' ');
}

const warsawDay = new Intl.DateTimeFormat('en-CA', {
	timeZone: 'Europe/Warsaw',
	year: 'numeric',
	month: '2-digit',
	day: '2-digit'
});

/**
 * The day it is in Poland at an instant, YYYY-MM-DD: the server's today, which no finish day may
 * pass, whatever the reader's time zone.
 */
export function todayInPoland(now: Date): string {
	return warsawDay.format(now);
}

/**
 * The finish day a form's text names: null for an empty field, an unknown day. A day after
 * today in Poland is refused as the server refuses it.
 */
export function finishDay(text: string, today: string): Parsed<string | null> {
	const day = text.trim();
	if (!day) return { ok: true, value: null };
	if (!/^\d{4}-\d{2}-\d{2}$/.test(day)) return dayIssue('Data w formacie RRRR-MM-DD.');
	if (day > today) return dayIssue('Data nie może być późniejsza niż dziś.');
	return { ok: true, value: day };
}

function dayIssue(message: string): { ok: false; issue: FormIssue } {
	return { ok: false, issue: { field: 'finished_on', message } };
}

/**
 * The `PUT /api/titles/{id}/finished` body that records a title as finished on a day (null when
 * unknown) or not finished. It carries nothing else, so a page holding an older title never
 * writes its name or notes back.
 */
export function finishPayload(finished: boolean, day: string | null): FinishPayload {
	return { finished, finished_on: finished ? day : null };
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

/** What a title page's hero shows, as `TitleHero` takes it. */
export type Hero = {
	title: string;
	overline: string;
	meta: string;
	byline?: string;
	kind: Kind;
	artwork?: string;
};

function byline(authors: readonly string[]): string | undefined {
	return authors.length > 0 ? authors.join(', ') : undefined;
}

/** The hero of a loaded title. */
export function titleHero(t: TitleDetail): Hero {
	return {
		title: displayName(t),
		overline: kindAndYear(t),
		meta: editionsLine(t),
		byline: byline(t.authors),
		kind: t.kind,
		artwork: titleArtwork(t)
	};
}

/**
 * The hero of a title still loading, as the collection showed it. A no-break space holds the
 * editions line's height until the title arrives, so the lines beside the cover stay put.
 */
export function previewHero(p: TitlePreview): Hero {
	return {
		title: displayName(p),
		overline: kindAndYear(p),
		meta: '\u00a0',
		byline: byline(p.authors),
		kind: p.kind,
		artwork: p.cover_url || undefined
	};
}

export type CopyFact = { label: string; value: string };

/** A copy's facts for its card: what was paid, when and where, the sale, its state and where it is kept. */
export function copyFacts(c: Copy): CopyFact[] {
	const state =
		[
			c.acquired_as ? acquiredLabels[c.acquired_as] : null,
			c.condition ? conditionLabels[c.condition] : null
		]
			.filter(Boolean)
			.join(' · ') || '—';
	if (c.status === 'wishlist') {
		const percent =
			c.max_market_percent == null
				? []
				: [{ label: 'Procent ceny rynkowej', value: `${c.max_market_percent}%` }];
		return [
			{ label: 'Cena maksymalna', value: formatPLN(c.max_price_cents) || '—' },
			...percent,
			{ label: 'Gdzie szukać', value: c.shop || '—' },
			{ label: 'Stan', value: state }
		];
	}
	const sale =
		c.status === 'sold'
			? [
					{ label: 'Sprzedano', value: purchaseDate(c.sold_on ?? null) || '—' },
					{ label: 'Cena sprzedaży', value: formatPLN(c.sale_price_cents) || '—' }
				]
			: [];
	return [
		{ label: 'Cena', value: formatPLN(c.price_cents) || '—' },
		{ label: 'Kupiono', value: purchaseDate(c.purchased_on) || '—' },
		{ label: 'Sklep', value: c.shop || '—' },
		...sale,
		{ label: 'Stan', value: state },
		...(c.location ? [{ label: 'Miejsce', value: c.location }] : [])
	];
}

/**
 * A copy's row in an edition card, such as "Na półce · 89,99 zł · Empik", "Lista życzeń · do
 * 60,00 zł" or, with what it sold for, "Sprzedany · 35,00 zł · Empik".
 */
export function copyLine(c: Copy): string {
	const price =
		c.status === 'owned'
			? formatPLN(c.price_cents)
			: c.status === 'sold'
				? formatPLN(c.sale_price_cents)
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
	/**
	 * The used value's trend over the stored days in a trendWidth × trendHeight box: the line
	 * through runs of days and a dot for each day alone between gaps; null under two days with one.
	 */
	trend: { path: string; dots: { x: number; y: number }[] } | null;
};

/** The size of a value card's trend. */
export const trendWidth = 92;
export const trendHeight = 36;

/**
 * The "Wartość dziś" card of an owned copy, or null when the valuation has no price for it and
 * no trend. A copy whose prices ran out of the window keeps a card with dashes and its trend. The
 * bar runs from the buy-back offer (0 without one) to the new price, or to the higher of the used
 * value and the price paid when no shop sells it new; marks outside it sit at its ends. Without a
 * used value there is no used mark and no difference from the price paid. The trend follows the
 * copy's used value over the days in history, leaving a gap on a day without one.
 */
export function valueCard(
	c: Copy,
	values: readonly CopyValue[],
	history: readonly CopyValueDay[] = []
): ValueCard | null {
	if (c.status !== 'owned') return null;
	const trend = copySparkline(history, c.id, trendWidth, trendHeight);
	const v = values.find((value) => value.copy_id === c.id);
	if (!v || (!v.floor && !v.mid && !v.ceiling)) {
		if (!trend) return null;
		return { used: '—', floor: '—', ceiling: '—', usedAt: null, paidAt: null, delta: null, trend };
	}
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
		delta: paid == null || mid == null ? null : `${signedZl(mid - paid)} względem zakupu`,
		trend
	};
}

/** Grosze in whole złoty with a sign: "+12 zł", "−16 zł" or "0 zł". */
function signedZl(cents: number): string {
	const zl = Math.round(Math.abs(cents) / 100);
	const sign = zl === 0 ? '' : cents > 0 ? '+' : '−';
	return `${sign}${formatZl(zl)}`;
}
