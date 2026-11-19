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
	it('asks the server for the listings under their limit and counts them', async () => {
		const fetchFn = deals([listing(3500, 4000), listing(1000, null)]);
		await badge.refreshDealBadge(fetchFn);
		expect(fetchFn).toHaveBeenCalledWith(
			'/api/deals?under_limit=true&limit=500',
			expect.objectContaining({ method: 'GET' })
		);
		expect(badge.dealBadge.count).toBe(1);
	});

	it('asks again on every call, so a lowered limit shows at the next navigation', async () => {
		await badge.refreshDealBadge(deals([listing(3500, 4000)]));
		expect(badge.dealBadge.count).toBe(1);
		const lowered = deals([]);
		await badge.refreshDealBadge(lowered);
		expect(lowered).toHaveBeenCalledTimes(1);
		expect(badge.dealBadge.count).toBe(0);
	});

	it('keeps the last count when the server fails', async () => {
		await badge.refreshDealBadge(deals([listing(100, 200), listing(300, 400)]));
		const failing = vi.fn(async (): Promise<Response> => {
			throw new TypeError('offline');
		});
		await badge.refreshDealBadge(failing);
		expect(failing).toHaveBeenCalledTimes(1);
		expect(badge.dealBadge.count).toBe(2);
	});

	it('ignores an answer that arrives after a later request answered', async () => {
		const older = Promise.withResolvers<Response>();
		const stale = badge.refreshDealBadge(vi.fn(() => older.promise));
		await badge.refreshDealBadge(deals([listing(3500, 4000)]));
		expect(badge.dealBadge.count).toBe(1);
		older.resolve(dealsResponse([]));
		await stale;
		expect(badge.dealBadge.count).toBe(1);
	});

	it('applies an older answer that arrives before the later one', async () => {
		const later = Promise.withResolvers<Response>();
		const first = badge.refreshDealBadge(deals([listing(100, 200), listing(300, 400)]));
		const second = badge.refreshDealBadge(vi.fn(() => later.promise));
		await first;
		expect(badge.dealBadge.count).toBe(2);
		later.resolve(dealsResponse([]));
		await second;
		expect(badge.dealBadge.count).toBe(0);
	});
});

describe('loadUnderLimit', () => {
	it('returns the listings it counts in the badge', async () => {
		const view = await badge.loadUnderLimit(deals([listing(3500, 4000), listing(7900, 8000)]));
		expect(view.observations).toHaveLength(2);
		expect(badge.dealBadge.count).toBe(2);
	});

	it('hands a stale answer to its caller without moving the badge', async () => {
		let resolve!: (r: Response) => void;
		const stale = badge.loadUnderLimit(vi.fn(() => new Promise<Response>((r) => (resolve = r))));
		await badge.loadUnderLimit(deals([listing(3500, 4000)]));
		resolve(dealsResponse([listing(100, 200), listing(300, 400)]));
		expect((await stale).observations).toHaveLength(2);
		expect(badge.dealBadge.count).toBe(1);
		expect(badge.dealBadge.listings?.map((l) => l.price_cents)).toEqual([3500]);
	});

	it('keeps the listings a refresh from another page counted, for the deals page', async () => {
		expect(badge.dealBadge.listings).toBeNull();
		await badge.loadUnderLimit(deals([listing(3500, 4000), listing(3900, 4000)]));
		await badge.refreshDealBadge(deals([listing(3500, 3600)]));
		expect(badge.dealBadge.count).toBe(1);
		expect(badge.dealBadge.listings?.map((l) => l.price_cents)).toEqual([3500]);
	});

	it('rejects a failed request, keeping the last count', async () => {
		await badge.loadUnderLimit(deals([listing(3500, 4000)]));
		await expect(
			badge.loadUnderLimit(vi.fn(async () => new Response('{}', { status: 500 })))
		).rejects.toThrow();
		expect(badge.dealBadge.count).toBe(1);
	});
});
