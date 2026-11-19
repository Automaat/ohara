import { request } from '#lib/api.js';
import { underLimitCount, type DealsView } from '#lib/deals.js';

/** How many listings wait at or under their wish's price limit, for the Okazje tab's badge. */
export const dealBadge = $state<{ count: number }>({ count: 0 });

/**
 * The listings under their wish's limit, filtered by the server before its row limit, so newer
 * listings above the limit or for owned editions never push them out of the count.
 */
export const badgePath = '/api/deals?under_limit=true&limit=500';

let issued = 0;
let applied = 0;

/**
 * Asks the server for the listings. Every navigation asks again, so a wish limit changed on
 * another page shows at once. Each request is numbered as it starts, and an answer that arrives
 * after a later request's answer is dropped. The badge is only a hint, so a failed request keeps
 * the last count.
 */
export function refreshDealBadge(fetchFn: typeof fetch): Promise<void> {
	const ticket = ++issued;
	return request<DealsView>(fetchFn, 'GET', badgePath)
		.then((view) => {
			if (ticket < applied) return;
			applied = ticket;
			dealBadge.count = underLimitCount(view.observations);
		})
		.catch(() => undefined);
}
