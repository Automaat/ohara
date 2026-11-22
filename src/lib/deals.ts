import {
	formatLabels,
	formatPLN,
	kindLabels,
	type CollectionItem,
	type FormIssue,
	type Parsed
} from '#lib/collection.js';
import type {
	AlertState,
	DealSource,
	DealsRunResult,
	HunterStatus,
	Keyword,
	KeywordKind,
	KeywordPayload,
	Format,
	MarketPrice,
	Observation,
	WantlistSyncResult
} from '#lib/api.gen.js';

export type {
	AlertState,
	DealSource,
	DealsRunResult as RunResult,
	DealsView,
	HunterStatus,
	Keyword,
	KeywordKind,
	KeywordPayload,
	MarketPrice,
	MarketView,
	Observation,
	ShareLink,
	WantlistStatus,
	WantlistSyncResult
} from '#lib/api.gen.js';

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

/**
 * The wish's rules as "limit 40 zł", "70% rynku (50 zł)" or both joined by "lub", naming the
 * market price the percent applies to when there is a trusted one; "bez limitu" without either.
 */
export function wishLimit(
	limit: number | null,
	percent: number | null = null,
	market: number | null = null
): string {
	const rules: string[] = [];
	if (limit != null) rules.push(`limit ${dealPrice(limit)}`);
	if (percent != null) {
		rules.push(market == null ? `${percent}% rynku` : `${percent}% rynku (${dealPrice(market)})`);
	}
	return rules.length === 0 ? 'bez limitu' : rules.join(' lub ');
}

/** What decides whether a listing alerts: its price, its wish's limit and share of the market. */
export type WishPrice = Pick<Observation, 'price_cents' | 'max_price_cents'> &
	Partial<Pick<Observation, 'max_market_percent' | 'market_cents'>>;

/**
 * A listing at or under its wish's percent of the market price, the server's trusted used median
 * for the wished title and format.
 */
export function isUnderMarket(o: WishPrice): boolean {
	return (
		o.max_market_percent != null &&
		o.market_cents != null &&
		o.price_cents * 100 <= o.market_cents * o.max_market_percent
	);
}

/** A listing at or under the price limit of the wish it matched, or under its market percent. */
export function isUnderLimit(o: WishPrice): boolean {
	return (o.max_price_cents != null && o.price_cents <= o.max_price_cents) || isUnderMarket(o);
}

/**
 * How a price stands against the wish: against its share of the market when only that holds, as
 * "5 zł pod 70% rynku", else against its limit, as limitComparison writes it.
 */
export function wishComparison(o: WishPrice): string {
	const percent = o.max_market_percent;
	const underLimit = o.max_price_cents != null && o.price_cents <= o.max_price_cents;
	if (percent == null || underLimit || (o.max_price_cents != null && !isUnderMarket(o))) {
		return limitComparison(o.price_cents, o.max_price_cents);
	}
	if (o.market_cents == null) return 'Brak ceny rynkowej';
	const difference = Math.floor((o.market_cents * percent) / 100) - o.price_cents;
	if (difference === 0) return `W ${percent}% rynku`;
	const amount = dealPrice(Math.abs(difference));
	return difference > 0 ? `${amount} pod ${percent}% rynku` : `${amount} ponad ${percent}% rynku`;
}

/** How many listings are at or under their wish's price limit or market percent. */
export function underLimitCount(observations: WishPrice[]): number {
	return observations.filter(isUnderLimit).length;
}

const pluralRules = new Intl.PluralRules('pl-PL');

/** A count of listings in words, as "1 oferta", "3 oferty" or "5 ofert". */
function offers(count: number): string {
	const plural = pluralRules.select(count);
	const noun = plural === 'one' ? 'oferta' : plural === 'few' ? 'oferty' : 'ofert';
	return `${count} ${noun}`;
}

/** The count of listings under the limit in words, as "3 oferty pod limitem". */
export function underLimitLabel(count: number): string {
	return `${offers(count)} pod limitem`;
}

/** Every wished title and format, or with a title every format of it. */
export function marketPath(titleId?: number): string {
	return titleId == null ? '/api/valuation/market' : `/api/valuation/market?title_id=${titleId}`;
}

/** The fewest listings a market price needs before it is trusted, the server's MinObservations. */
export const marketMinListings = 3;

/** The market price of one title in one format, null when it has none. */
export function marketFor(
	markets: readonly MarketPrice[] | null | undefined,
	titleId: number | null,
	format: Format | null
): MarketPrice | null {
	if (titleId == null) return null;
	return markets?.find((m) => m.title_id === titleId && m.format === format) ?? null;
}

/**
 * The market price in words, as "Rynek ok. 42,50 zł (4 oferty)", with "mało danych" when it rests
 * on fewer than 3 listings; null without a price.
 */
export function marketLabel(market: Pick<MarketPrice, 'cents' | 'count'> | null): string | null {
	if (market == null || market.count <= 0) return null;
	const basis = offers(market.count);
	const note = market.count < marketMinListings ? `${basis}, mało danych` : basis;
	return `Rynek ok. ${dealPrice(market.cents)} (${note})`;
}

/**
 * Every listing matched to a wish, with or without a limit, filtered by the server before its
 * row limit, so listings for owned editions never push them out.
 */
export const matchedPath = '/api/deals?matched=true&limit=500';

/** The listings whose Telegram alert went out for a wish that still has a limit. */
export const sentPath = '/api/deals?sent=true&limit=500';

/** POST hides the listing from Okazje ("Ignoruj"), DELETE brings it back. */
export function dismissalPath(id: number): string {
	return `/api/deals/${id}/dismissal`;
}

/** The listings without those just dismissed, before the server's next answer leaves them out. */
export function withoutDismissed<T extends Pick<Observation, 'id'>>(
	listings: T[],
	dismissed: ReadonlySet<number>
): T[] {
	return dismissed.size === 0 ? listings : listings.filter((o) => !dismissed.has(o.id));
}

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
export function lastRunSummary(result: DealsRunResult | null): string | null {
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

/**
 * Every wish in the collection, which Okazje lists whether or not an offer was found: Kolekcja
 * lists none, as a wish is not had yet.
 */
export const wishesPath = '/api/collection?status=wishlist';

/**
 * A wish's line on Okazje, such as "Film · Blu-ray · limit 80 zł" or "Książka · Stanisław Lem": its
 * kind, authors, format and price limit. The collection does not carry a wish's market percent,
 * so a wish without a price limit names none rather than claiming it has no limit.
 */
export function wishFacts(
	wish: Pick<CollectionItem, 'kind' | 'authors' | 'format' | 'max_price_cents'>
): string {
	return [
		kindLabels[wish.kind],
		wish.authors?.join(', '),
		wish.format ? formatLabels[wish.format] : null,
		wish.max_price_cents == null ? null : wishLimit(wish.max_price_cents)
	]
		.filter(Boolean)
		.join(' · ');
}

/** GET reads the Discogs wantlist sync's state; POST to its /sync starts a sync. */
export const wantlistPath = '/api/discogs/wantlist';

/** One line about the last Discogs wantlist sync, or null before the first one. */
export function wantlistSummary(result: WantlistSyncResult | null): string | null {
	if (!result) return null;
	const parts = [`dodano: ${result.added}`, `już na liście: ${result.already}`];
	if (result.removed > 0) parts.push(`usunięto: ${result.removed}`);
	if (result.skipped > 0) parts.push(`bez wydania Discogs: ${result.skipped}`);
	const counts = parts.join(', ');
	return result.failure
		? `Synchronizacja przerwana: ${result.failure}. Przed przerwaniem ${counts}.`
		: `Ostatnia synchronizacja: ${counts}.`;
}

/** The owner's share link of the wishlist: GET shows it, POST makes a new one, DELETE turns it off. */
export const shareLinkPath = '/api/wishlist/share';

/** The full address of a share link, whose `url` is its path on this site. */
export function shareURL(link: { url: string }, origin: string): string {
	return new URL(link.url, origin).href;
}
