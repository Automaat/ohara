import { request } from '#lib/api.js';
import { underLimitCount, type DealsView, type Observation } from '#lib/deals.js';

/** How many listings wait at or under their wish's price limit, for the Okazje tab's badge. */
export const dealBadge = $state<{ count: number }>({ count: 0 });

let issued = 0;
let applied = 0;

/**
 * Numbers a request for the listings as it starts. An answer carries its number to `showDeals`,
 * so one that arrives after a later request's answer cannot bring back an older count.
 */
export function startDealsRequest(): number {
	return ++issued;
}

/** Sets the badge from the listings of the request numbered `ticket`, unless a later one answered. */
export function showDeals(observations: Observation[], ticket: number): void {
	if (ticket < applied) return;
	applied = ticket;
	dealBadge.count = underLimitCount(observations);
}

/**
 * Asks the server for the listings. Every navigation asks again, so a wish limit changed on
 * another page shows at once. The badge is only a hint, so a failed request keeps the last count.
 */
export function refreshDealBadge(fetchFn: typeof fetch): Promise<void> {
	const ticket = startDealsRequest();
	return request<DealsView>(fetchFn, 'GET', '/api/deals')
		.then((view) => showDeals(view.observations, ticket))
		.catch(() => undefined);
}
