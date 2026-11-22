import { request } from '#lib/api.js';
import {
	clockTime,
	expiringSources,
	stillConfirmed,
	underLimitCount,
	withoutUnconfirmed,
	type ClockTime,
	type DealsView,
	type Observation
} from '#lib/deals.js';

/**
 * The listings at or under their wish's price limit from the newest answer, null before the
 * first one, how many there are, for the Okazje tab's badge, and when that answer arrived, null
 * before the first. The deals page lists these same listings under "Pod limitem", so a refresh
 * from any page moves the badge and the segment together.
 */
export const dealBadge = $state<{
	count: number;
	listings: Observation[] | null;
	answeredAt: ClockTime | null;
}>({
	count: 0,
	listings: null,
	answeredAt: null
});

/** How often the badge checks whether its listings of expiring sources still may count. */
export const expiryCheckMs = 30_000;

/**
 * The listings under their wish's limit, filtered by the server before its row limit, so newer
 * listings above the limit or for owned editions never push them out of the count.
 */
export const badgePath = '/api/deals?under_limit=true&limit=500';

let issued = 0;
let applied = 0;
let watch: ReturnType<typeof setInterval> | undefined;

/**
 * Asks the server for the listings under the limit and counts them in the badge, so a page that
 * lists the same answer, such as the deals page's "Pod limitem", never disagrees with the badge.
 * Each request is numbered as it starts, and an answer that arrives after a later request's
 * answer still reaches its caller but leaves the badge alone. The listings of expiring sources
 * leave the badge once their answer no longer confirms them (expireUnconfirmed), unless a newer
 * answer arrives first.
 */
export async function loadUnderLimit(fetchFn: typeof fetch): Promise<DealsView> {
	const ticket = ++issued;
	const view = await request<DealsView>(fetchFn, 'GET', badgePath);
	if (ticket >= applied) {
		applied = ticket;
		dealBadge.listings = view.observations;
		dealBadge.count = underLimitCount(view.observations);
		dealBadge.answeredAt = clockTime();
		clearInterval(watch);
		watch = view.observations.some((o) => expiringSources.has(o.source))
			? setInterval(expireUnconfirmed, expiryCheckMs)
			: undefined;
	}
	return view;
}

/**
 * Takes the listings of expiring sources out of the badge and "Pod limitem" once their answer no
 * longer confirms them (stillConfirmed). It runs every expiryCheckMs while the badge holds one and
 * whenever the tab shows again, since timers stop while a computer sleeps.
 */
export function expireUnconfirmed(): void {
	if (!dealBadge.listings || !dealBadge.answeredAt) return;
	if (stillConfirmed(dealBadge.answeredAt, clockTime())) return;
	clearInterval(watch);
	watch = undefined;
	dealBadge.listings = withoutUnconfirmed(dealBadge.listings);
	dealBadge.count = underLimitCount(dealBadge.listings);
}

if (typeof document !== 'undefined') {
	document.addEventListener('visibilitychange', expireUnconfirmed);
}

/**
 * Asks the server for the listings. Every page navigation asks again, so a wish limit changed on
 * another page shows at once; a shallow one, such as the scanner's sheet, stays on its page and
 * does not. The badge is only a hint, so a failed request keeps the last count.
 */
export function refreshDealBadge(fetchFn: typeof fetch): Promise<void> {
	return loadUnderLimit(fetchFn).then(
		() => undefined,
		() => undefined
	);
}

/**
 * Takes a dismissed listing out of the badge and "Pod limitem" at once. An answer to a request
 * sent before the dismissal may still hold it, so only a later request moves the badge again.
 */
export function forgetDealListing(id: number): void {
	applied = issued + 1;
	if (!dealBadge.listings) return;
	dealBadge.listings = dealBadge.listings.filter((o) => o.id !== id);
	dealBadge.count = underLimitCount(dealBadge.listings);
}
