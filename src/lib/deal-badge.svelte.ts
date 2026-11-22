import { request } from '#lib/api.js';
import { underLimitCount, type DealsView, type Observation } from '#lib/deals.js';

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

/**
 * Asks the server for the listings under the limit and counts them in the badge, so a page that
 * lists the same answer, such as the deals page's "Pod limitem", never disagrees with the badge.
 * Each request is numbered as it starts, and an answer that arrives after a later request's
 * answer still reaches its caller but leaves the badge alone.
 */
export async function loadUnderLimit(fetchFn: typeof fetch): Promise<DealsView> {
	const ticket = ++issued;
	const view = await request<DealsView>(fetchFn, 'GET', badgePath);
	if (ticket >= applied) {
		applied = ticket;
		dealBadge.listings = view.observations;
		dealBadge.count = underLimitCount(view.observations);
	}
	return view;
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
