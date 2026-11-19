import { request } from '#lib/api.js';
import { underLimitCount, type DealsView, type Observation } from '#lib/deals.js';

/** How many listings wait at or under their wish's price limit, for the Okazje tab's badge. */
export const dealBadge = $state<{ count: number }>({ count: 0 });

/** How long a count stays fresh before the tab bar asks the server again. */
export const badgeMaxAge = 60_000;

let checkedAt = Number.NEGATIVE_INFINITY;
let version = 0;
let pending: Promise<void> | null = null;

/** Sets the badge from listings already loaded, as the deals page loads them. */
export function showDeals(observations: Observation[], now = Date.now()): void {
	dealBadge.count = underLimitCount(observations);
	checkedAt = now;
	version++;
}

/**
 * Asks the server for the listings once the count is older than `badgeMaxAge`. The badge is only
 * a hint, so a failed request keeps the last count until the next attempt, and a count the deals
 * page set meanwhile wins over the answer.
 */
export function refreshDealBadge(fetchFn: typeof fetch, now = Date.now()): Promise<void> {
	if (pending) return pending;
	if (now - checkedAt < badgeMaxAge) return Promise.resolve();
	checkedAt = now;
	const started = version;
	pending = request<DealsView>(fetchFn, 'GET', '/api/deals')
		.then((view) => {
			if (version === started) showDeals(view.observations, now);
		})
		.catch(() => undefined)
		.finally(() => {
			pending = null;
		});
	return pending;
}
