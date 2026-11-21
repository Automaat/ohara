import {
	formatPLN,
	type AcquiredAs,
	type Format,
	type FormIssue,
	type Parsed
} from '#lib/collection.js';

export type DealSource = 'olx' | 'pepper';
export type AlertState = 'none' | 'pending' | 'sent' | 'failed';
export type KeywordKind = 'reject' | 'accept';

export type Observation = {
	id: number;
	source: DealSource;
	external_id: string;
	observed_at: string;
	price_cents: number;
	acquired_as: AcquiredAs | null;
	url: string;
	listing_title: string;
	title_id: number | null;
	title_name: string | null;
	edition_id: number | null;
	format: Format | null;
	listing_photo_url: string | null;
	cover_url: string | null;
	max_price_cents: number | null;
	alert: AlertState;
	alert_error: string;
	alert_attempts: number;
};

const wholePLN = new Intl.NumberFormat('pl-PL', {
	style: 'currency',
	currency: 'PLN',
	minimumFractionDigits: 0
});

/** A listing price as "79 zł", with grosze only when it has them: "79,99 zł". */
export function dealPrice(cents: number): string {
	return cents % 100 === 0 ? wholePLN.format(cents / 100) : formatPLN(cents);
}

/** How a price stands against the wish's limit, as "5 zł pod limitem" or "Brak limitu". */
export function limitComparison(price: number, limit: number | null): string {
	if (limit == null) return 'Brak limitu';
	const difference = limit - price;
	if (difference === 0) return 'W limicie';
	const amount = dealPrice(Math.abs(difference));
	return difference > 0 ? `${amount} pod limitem` : `${amount} ponad limit`;
}

/** The wish's limit as "limit 40 zł", or "bez limitu". */
export function wishLimit(limit: number | null): string {
	return limit == null ? 'bez limitu' : `limit ${dealPrice(limit)}`;
}

/** A listing at or under the price limit of the wish it matched. */
export function isUnderLimit(o: Pick<Observation, 'price_cents' | 'max_price_cents'>): boolean {
	return o.max_price_cents != null && o.price_cents <= o.max_price_cents;
}

/** How many listings are at or under their wish's price limit. */
export function underLimitCount(
	observations: Pick<Observation, 'price_cents' | 'max_price_cents'>[]
): number {
	return observations.filter(isUnderLimit).length;
}

const pluralRules = new Intl.PluralRules('pl-PL');

/** The count of listings under the limit in words, as "3 oferty pod limitem". */
export function underLimitLabel(count: number): string {
	const plural = pluralRules.select(count);
	const noun = plural === 'one' ? 'oferta' : plural === 'few' ? 'oferty' : 'ofert';
	return `${count} ${noun} pod limitem`;
}

export type RunResult = {
	wants: number;
	queries: number;
	listings: number;
	matched: number;
	new: number;
	alerts: number;
	sent: number;
	errors: string[];
	failure?: string;
};

export type HunterStatus = {
	running: boolean;
	scheduled: boolean;
	interval_seconds: number;
	telegram: boolean;
	last_started_at: string | null;
	last_finished_at: string | null;
	last_result: RunResult | null;
};

/** GET /api/deals: the hunter, how many copies are on the wishlist and the listings asked for. */
export type DealsView = { status: HunterStatus; wishes: number; observations: Observation[] };

/**
 * Every listing matched to a wish, with or without a limit, filtered by the server before its
 * row limit, so listings for owned editions never push them out.
 */
export const matchedPath = '/api/deals?matched=true&limit=500';

/** The listings whose Telegram alert went out for a wish that still has a limit. */
export const sentPath = '/api/deals?sent=true&limit=500';

export type DealSegment = 'under' | 'all' | 'sent';

export const segmentLabels: Record<DealSegment, string> = {
	under: 'Pod limitem',
	all: 'Wszystkie',
	sent: 'Wysłane'
};

/** A segment's label with its count, as "Pod limitem · 3". */
export function segmentLabel(segment: DealSegment, count: number): string {
	return `${segmentLabels[segment]} · ${count}`;
}

const relative = new Intl.RelativeTimeFormat('pl', { numeric: 'auto', style: 'short' });

/** How long ago instant was: "przed chwilą", "5 min temu", "2 godz. temu", "wczoraj". */
export function timeAgo(instant: string, now: Date): string {
	const seconds = (now.getTime() - Date.parse(instant)) / 1000;
	if (seconds < 60) return 'przed chwilą';
	if (seconds < 3600) return relative.format(-Math.floor(seconds / 60), 'minute');
	if (seconds < 86_400) return relative.format(-Math.floor(seconds / 3600), 'hour');
	return relative.format(-Math.floor(seconds / 86_400), 'day');
}

/** The page's line under its title: "4 na liście życzeń · sprawdzono 2 godz. temu". */
export function dealsSummary(wishes: number, status: HunterStatus, now: Date): string {
	const parts = [`${wishes} na liście życzeń`];
	if (status.last_finished_at) parts.push(`sprawdzono ${timeAgo(status.last_finished_at, now)}`);
	return parts.join(' · ');
}

/** Why a segment is empty: how many wishes the hunter searches for and when it last ran. */
export function searchedFor(wishes: number, status: HunterStatus, now: Date): string {
	const last = status.last_finished_at
		? `Ostatnie wyszukiwanie: ${timeAgo(status.last_finished_at, now)}.`
		: 'Od uruchomienia serwera nie było wyszukiwania.';
	return `Szukamy ofert dla ${wishes} pozycji z listy życzeń. ${last}`;
}

export type Keyword = { id: number; kind: KeywordKind; phrase: string; created_at: string };
export type KeywordPayload = { kind: KeywordKind; phrase: string };

export const sourceLabels: Record<DealSource, string> = {
	olx: 'OLX',
	pepper: 'Pepper'
};

export const alertLabels: Record<AlertState, string> = {
	none: 'Bez powiadomienia',
	pending: 'Czeka na wysłanie',
	sent: 'Wysłano',
	failed: 'Nie wysłano'
};

export const keywordKindLabels: Record<KeywordKind, string> = {
	reject: 'Odrzucaj',
	accept: 'Zawsze dopuszczaj'
};

/** The phrases of one kind, in the server's order. */
export function keywordsOf(keywords: Keyword[], kind: KeywordKind): Keyword[] {
	return keywords.filter((k) => k.kind === kind);
}

/** The request body for a new phrase; blank or overlong phrases never reach the server. */
export function keywordPayload(kind: KeywordKind, phrase: string): Parsed<KeywordPayload> {
	const cleaned = phrase.trim().replace(/\s+/g, ' ');
	if (!/[\p{L}\p{N}]/u.test(cleaned)) {
		return issue('phrase', 'Wpisz słowo lub frazę.');
	}
	if (cleaned.length > 100) {
		return issue('phrase', 'Fraza może mieć najwyżej 100 znaków.');
	}
	return { ok: true, value: { kind, phrase: cleaned } };
}

function issue(field: string, message: string): { ok: false; issue: FormIssue } {
	return { ok: false, issue: { field, message } };
}

/** How often the hunter polls, as "co 2 godz." or "co 45 min"; empty when it does not poll. */
export function intervalLabel(status: HunterStatus): string {
	if (!status.scheduled || status.interval_seconds <= 0) return '';
	const minutes = Math.round(status.interval_seconds / 60);
	if (minutes % 60 === 0) return `co ${minutes / 60} godz.`;
	return `co ${minutes} min`;
}

/** One line about the last finished run, or null before the first one. */
export function lastRunSummary(result: RunResult | null): string | null {
	if (!result) return null;
	if (result.failure) return `Ostatnie wyszukiwanie przerwane: ${result.failure}`;
	const parts = [
		`Pozycji z listy życzeń: ${result.wants}`,
		`nowych ofert: ${result.new}`,
		`powiadomień: ${result.sent}`
	];
	if (result.errors.length > 0) parts.push(`błędów źródeł: ${result.errors.length}`);
	return parts.join(', ');
}
