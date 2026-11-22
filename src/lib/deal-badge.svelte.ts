import { request } from '#lib/api.js';
import {
	underLimitCount,
	withoutUnconfirmed,
	type DealsView,
	type Observation
} from '#lib/deals.js';

/**
 * The listings at or under their wish's price limit from the newest answer, null before the
 * first one, and how many there are, for the Okazje tab's badge. The deals page lists these same
 * listings under "Pod limitem", so a refresh from any page moves the badge and the segment
 * together.
 */
export const dealBadge = $state<{ count: number; listings: Observation[] | null }>({
	count: 0,
	listings: null
});

/**
 * The listings under their wish's limit, filtered by the server before its row limit, so newer
 * listings above the limit or for owned editions never push them out of the count.
 */
export const badgePath = '/api/deals?under_limit=true&limit=500';

let issued = 0;
let applied = 0;
/** The number of the request whose answer the badge shows. */
let confirmed = 0;

/**
 * Asks the server for the listings under the limit and counts them in the badge, so a page that
 * lists the same answer, such as the deals page's "Pod limitem", never disagrees with the badge.
 * Each request is numbered as it starts, and an answer that arrives after a later request's
 * answer still reaches its caller but leaves the badge alone. A failed request drops the
 * listings of expiring sources (forgetUnconfirmed) and rejects.
 */
export async function loadUnderLimit(fetchFn: typeof fetch): Promise<DealsView> {
	const ticket = ++issued;
	let view: DealsView;
	try {
		view = await request<DealsView>(fetchFn, 'GET', badgePath);
	} catch (err) {
		forgetUnconfirmed(ticket);
		throw err;
	}
	if (ticket >= applied) {
		applied = ticket;
		confirmed = ticket;
		dealBadge.listings = view.observations;
		dealBadge.count = underLimitCount(view.observations);
	}
	return view;
}

/**
 * Takes the listings of expiring sources out of the badge and "Pod limitem" after request ticket
 * failed, since no answer confirms they are still offered (withoutUnconfirmed), unless an answer
 * to a later request already shows. Answers to requests sent before the failed one no longer
 * move the badge; a later answer brings the listings back.
 */
function forgetUnconfirmed(ticket: number): void {
	if (ticket < confirmed) return;
	applied = Math.max(applied, ticket + 1);
	if (!dealBadge.listings) return;
	dealBadge.listings = withoutUnconfirmed(dealBadge.listings);
	dealBadge.count = underLimitCount(dealBadge.listings);
}

/**
 * Asks the server for the listings. Every page navigation asks again, so a wish limit changed on
 * another page shows at once; a shallow one, such as the scanner's sheet, stays on its page and
 * does not. The badge is only a hint, so a failed request keeps the last count but for the
 * listings of expiring sources.
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
