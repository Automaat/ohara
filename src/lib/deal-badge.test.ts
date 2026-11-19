import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Observation } from './deals';

type Badge = typeof import('./deal-badge.svelte');

const listing = (price: number, limit: number | null): Observation => ({
	id: price,
	source: 'olx',
	external_id: String(price),
	observed_at: '2026-11-19T08:00:00Z',
	price_cents: price,
	acquired_as: null,
	url: 'https://example.com',
	listing_title: 'Diuna',
	title_id: 1,
	title_name: 'Diuna',
	edition_id: 1,
	format: 'bluray',
	listing_photo_url: null,
	cover_url: null,
	max_price_cents: limit,
	alert: 'none',
	alert_error: '',
	alert_attempts: 0
});

const dealsResponse = (observations: Observation[]) =>
	new Response(
		JSON.stringify({
			status: {
				running: false,
				scheduled: false,
				interval_seconds: 0,
				telegram: false,
				last_started_at: null,
				last_finished_at: null,
				last_result: null
			},
			observations
		}),
		{ status: 200, headers: { 'content-type': 'application/json' } }
	);

const deals = (observations: Observation[]) => vi.fn(async () => dealsResponse(observations));

let badge: Badge;

beforeEach(async () => {
	vi.resetModules();
	badge = await import('./deal-badge.svelte');
});

describe('refreshDealBadge', () => {
	it('counts the listings under the wish limit', async () => {
		const fetchFn = deals([listing(3500, 4000), listing(5500, 4000), listing(1000, null)]);
		await badge.refreshDealBadge(fetchFn);
		expect(fetchFn).toHaveBeenCalledWith('/api/deals', expect.objectContaining({ method: 'GET' }));
		expect(badge.dealBadge.count).toBe(1);
	});

	it('asks again on every call, so a lowered limit shows at the next navigation', async () => {
		await badge.refreshDealBadge(deals([listing(3500, 4000)]));
		expect(badge.dealBadge.count).toBe(1);
		const lowered = deals([listing(3500, 3000)]);
		await badge.refreshDealBadge(lowered);
		expect(lowered).toHaveBeenCalledTimes(1);
		expect(badge.dealBadge.count).toBe(0);
	});

	it('keeps the last count when the server fails', async () => {
		badge.showDeals([listing(100, 200), listing(300, 400)], badge.startDealsRequest());
		const failing = vi.fn(async (): Promise<Response> => {
			throw new TypeError('offline');
		});
		await badge.refreshDealBadge(failing);
		expect(failing).toHaveBeenCalledTimes(1);
		expect(badge.dealBadge.count).toBe(2);
	});

	it('ignores an answer that arrives after a later request answered', async () => {
		const older = Promise.withResolvers<Response>();
		const slow = vi.fn(() => older.promise);
		const stale = badge.refreshDealBadge(slow);
		await badge.refreshDealBadge(deals([listing(3500, 4000)]));
		expect(badge.dealBadge.count).toBe(1);
		older.resolve(dealsResponse([]));
		await stale;
		expect(badge.dealBadge.count).toBe(1);
	});

	it('lets the deals page win over an older refresh and lose to a newer one', async () => {
		const older = Promise.withResolvers<Response>();
		const refreshing = badge.refreshDealBadge(vi.fn(() => older.promise));
		const pageTicket = badge.startDealsRequest();
		badge.showDeals([listing(100, 200), listing(300, 400), listing(500, 600)], pageTicket);
		older.resolve(dealsResponse([]));
		await refreshing;
		expect(badge.dealBadge.count).toBe(3);

		const leftPage = badge.startDealsRequest();
		await badge.refreshDealBadge(deals([listing(100, 200)]));
		badge.showDeals([], leftPage);
		expect(badge.dealBadge.count).toBe(1);
	});
});

describe('showDeals', () => {
	it('shows zero when nothing is under the limit', () => {
		badge.showDeals([listing(5500, 4000)], badge.startDealsRequest());
		expect(badge.dealBadge.count).toBe(0);
	});
});
