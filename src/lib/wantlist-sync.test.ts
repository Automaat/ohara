import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from './api';
import type { WantlistStatus } from './deals';
import { WantlistSync, wantlistPollMs } from './wantlist-sync.svelte';

function status(over: Partial<WantlistStatus> = {}): WantlistStatus {
	return {
		enabled: true,
		running: false,
		last_started_at: null,
		last_finished_at: null,
		last_result: null,
		...over
	};
}

const unavailable = () => new ApiError('Usługa niedostępna', 502);

describe('WantlistSync', () => {
	beforeEach(() => {
		vi.useFakeTimers();
	});
	afterEach(() => {
		vi.useRealTimers();
	});

	it('says why the first status request failed, so the page can offer a retry', async () => {
		const get = vi.fn<() => Promise<WantlistStatus>>().mockRejectedValueOnce(unavailable());
		const sync = new WantlistSync({ status: get, start: async () => undefined });

		await sync.load();
		expect(sync.status).toBeNull();
		expect(sync.failure).toBe('Usługa niedostępna');
		expect(vi.getTimerCount()).toBe(0);

		get.mockResolvedValueOnce(status());
		await sync.load();
		expect(sync.status?.enabled).toBe(true);
		expect(sync.failure).toBeNull();
	});

	it('keeps the last status when a later request fails', async () => {
		const get = vi
			.fn<() => Promise<WantlistStatus>>()
			.mockResolvedValueOnce(status())
			.mockRejectedValueOnce(unavailable());
		const sync = new WantlistSync({ status: get, start: async () => undefined });

		await sync.load();
		await sync.load();
		expect(sync.status?.enabled).toBe(true);
		expect(sync.failure).toBe('Usługa niedostępna');
	});

	it('asks again every 2 s while a sync runs, and stops once it ends', async () => {
		const get = vi
			.fn<() => Promise<WantlistStatus>>()
			.mockResolvedValueOnce(status({ running: true }))
			.mockResolvedValueOnce(status());
		const sync = new WantlistSync({ status: get, start: async () => undefined });

		await sync.load();
		expect(sync.syncing).toBe(true);
		await vi.advanceTimersByTimeAsync(wantlistPollMs);
		expect(get).toHaveBeenCalledTimes(2);
		expect(sync.syncing).toBe(false);
		expect(vi.getTimerCount()).toBe(0);
	});

	it('asks nothing more once the page is left, even for an answer still on its way', async () => {
		let answer: (s: WantlistStatus) => void = () => undefined;
		const get = vi.fn(
			() =>
				new Promise<WantlistStatus>((resolve) => {
					answer = resolve;
				})
		);
		const sync = new WantlistSync({ status: get, start: async () => undefined });

		const pending = sync.load();
		sync.destroy();
		answer(status({ running: true }));
		await pending;
		await vi.advanceTimersByTimeAsync(wantlistPollMs * 3);
		expect(get).toHaveBeenCalledTimes(1);
		expect(vi.getTimerCount()).toBe(0);
	});

	it('names a sync already running, then reads the status', async () => {
		const get = vi.fn(async () => status({ running: true }));
		const sync = new WantlistSync({
			status: get,
			start: async () => {
				throw new ApiError('a Discogs wantlist sync is already running', 409);
			}
		});

		await sync.sync();
		expect(sync.error).toBe('Synchronizacja już trwa.');
		expect(sync.status?.running).toBe(true);
		sync.destroy();
	});
});
