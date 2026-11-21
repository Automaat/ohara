import { IDBFactory } from 'fake-indexeddb';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
	PendingScans,
	offlineDetail,
	openPendingStore,
	type PendingStore
} from './pending-scans.svelte';

const a = '5901234123457';
const b = '9788324000012';
const c = '4006381333931';

/** A device's store in memory; fail makes its operations throw, as a lost database does. */
function memory(codes: string[] = []) {
	const kept = new Set(codes);
	const state = { fail: false, opened: 0 };
	const check = () => {
		if (state.fail) throw new DOMException('gone', 'InvalidStateError');
	};
	const store: PendingStore = {
		list: async () => (check(), [...kept]),
		add: async (code) => void (check(), kept.add(code)),
		remove: async (code) => void (check(), kept.delete(code))
	};
	const open = async () => {
		state.opened++;
		return store;
	};
	return { kept, state, open };
}

type Answer = number | 'none';

/** The scan queue's answers to POSTs, by barcode; a code without one gets 201. */
function api(answers: Record<string, Answer | Answer[]> = {}) {
	const posted: { barcode: string; reason: string; detail: string }[] = [];
	const fetchFn: typeof fetch = async (input, init) => {
		expect([init?.method, String(input)]).toEqual(['POST', '/api/scan-queue']);
		const body = JSON.parse(String(init?.body)) as (typeof posted)[number];
		posted.push(body);
		const given = answers[body.barcode] ?? 201;
		const status = Array.isArray(given) ? (given.shift() ?? 201) : given;
		if (status === 'none') throw new TypeError('Failed to fetch');
		const text =
			status === 401
				? 'Unauthorized'
				: JSON.stringify(status < 300 ? { id: 1 } : { error: 'x', field: 'barcode' });
		return new Response(text, { status });
	};
	return { fetchFn, posted, codes: () => posted.map((p) => p.barcode) };
}

describe('openPendingStore', () => {
	it('keeps each code once across reopening', async () => {
		const factory = new IDBFactory();
		const first = await openPendingStore(factory);
		await first?.add(a);
		await first?.add(b);
		await first?.add(a);
		const again = await openPendingStore(factory);
		expect(await again?.list()).toEqual([a, b].sort());
		await again?.remove(a);
		expect(await first?.list()).toEqual([b]);
	});

	it('has no store without IndexedDB or when it refuses to open', async () => {
		expect(await openPendingStore(undefined)).toBeNull();
		const throwing = {
			open: () => {
				throw new DOMException('denied', 'SecurityError');
			}
		} as unknown as IDBFactory;
		expect(await openPendingStore(throwing)).toBeNull();
	});

	it('has no store when opening fails', async () => {
		const failing = {
			open: () => {
				const req = {} as IDBOpenDBRequest;
				queueMicrotask(() => req.onerror?.(new Event('error')));
				return req;
			}
		} as unknown as IDBFactory;
		expect(await openPendingStore(failing)).toBeNull();
	});
});

describe('PendingScans.keep', () => {
	it('counts each kept code once', async () => {
		const device = memory();
		const pending = new PendingScans(api().fetchFn, device.open);
		expect(await pending.keep(a)).toBe(true);
		expect(await pending.keep(b)).toBe(true);
		expect(await pending.keep(a)).toBe(true);
		expect(pending.count).toBe(2);
		expect(pending.available).toBe(true);
		expect(device.state.opened).toBe(1);
	});

	it('keeps nothing where the device has no store', async () => {
		const pending = new PendingScans(api().fetchFn, async () => null);
		expect(await pending.keep(a)).toBe(false);
		expect(pending.available).toBe(false);
		expect(pending.count).toBe(0);
		const broken = new PendingScans(api().fetchFn, () => Promise.reject(new Error('boom')));
		expect(await broken.keep(a)).toBe(false);
		expect(broken.available).toBe(false);
	});

	it('opens the store again after a write failed', async () => {
		const device = memory();
		const pending = new PendingScans(api().fetchFn, device.open);
		device.state.fail = true;
		expect(await pending.keep(a)).toBe(false);
		device.state.fail = false;
		expect(await pending.keep(a)).toBe(true);
		expect(device.state.opened).toBe(2);
		expect(pending.count).toBe(1);
	});

	it('keeps codes in the browser database across a new page', async () => {
		const factory = new IDBFactory();
		const s = api();
		const before = new PendingScans(s.fetchFn, () => openPendingStore(factory));
		await before.keep(a);
		await before.keep(b);
		const after = new PendingScans(s.fetchFn, () => openPendingStore(factory));
		await after.send();
		expect(s.codes()).toEqual([a, b].sort());
		expect(after.count).toBe(0);
		expect(await (await openPendingStore(factory))?.list()).toEqual([]);
	});
});

describe('PendingScans.send', () => {
	it('queues each code as a failed lookup scanned without a connection', async () => {
		const device = memory([a, b]);
		const s = api({ [b]: 200 });
		const pending = new PendingScans(s.fetchFn, device.open);
		await pending.send();
		expect(s.posted).toEqual([
			{ barcode: a, reason: 'lookup_failed', detail: offlineDetail },
			{ barcode: b, reason: 'lookup_failed', detail: offlineDetail }
		]);
		expect(device.kept.size).toBe(0);
		expect(pending.count).toBe(0);
	});

	it('settles a code an edition carries or the queue refuses, and keeps one the server failed', async () => {
		const device = memory([a, b, c]);
		const s = api({ [a]: 409, [b]: 503, [c]: 422 });
		const pending = new PendingScans(s.fetchFn, device.open);
		await pending.send();
		expect(s.codes()).toEqual([a, b, c]);
		expect([...device.kept]).toEqual([b]);
		expect(pending.count).toBe(1);
	});

	it('keeps a code another refusal answers for the next run', async () => {
		const device = memory([a]);
		const s = api({ [a]: [400, 201] });
		const pending = new PendingScans(s.fetchFn, device.open);
		await pending.send();
		expect(pending.count).toBe(1);
		await pending.send();
		expect(pending.count).toBe(0);
		expect(s.codes()).toEqual([a, a]);
	});

	it('stops at the first code that gets no answer and keeps the rest', async () => {
		const device = memory([a, b]);
		const s = api({ [a]: 'none' });
		const pending = new PendingScans(s.fetchFn, device.open);
		await pending.send();
		expect(s.codes()).toEqual([a]);
		expect(pending.count).toBe(2);
		expect(pending.expired).toBe(false);
	});

	it('stops and asks to sign in again when the sign-in turns a code away', async () => {
		const device = memory([a, b]);
		const s = api({ [a]: [401, 201] });
		const pending = new PendingScans(s.fetchFn, device.open);
		await pending.send();
		expect(s.codes()).toEqual([a]);
		expect(pending.expired).toBe(true);
		expect(pending.count).toBe(2);
		await pending.send();
		expect(pending.expired).toBe(false);
		expect(pending.count).toBe(0);
	});

	it('sends nothing with no code waiting or no store', async () => {
		const s = api();
		const empty = new PendingScans(s.fetchFn, memory().open);
		await empty.send();
		const none = new PendingScans(s.fetchFn, async () => null);
		await none.send();
		expect(s.posted).toHaveLength(0);
		expect(empty.count).toBe(0);
	});

	it('runs once at a time', async () => {
		const device = memory([a]);
		const s = api();
		const pending = new PendingScans(s.fetchFn, device.open);
		const first = pending.send();
		expect(pending.send()).toBe(first);
		await first;
		await pending.send();
		expect(s.codes()).toEqual([a]);
	});

	it('opens the store again after it failed', async () => {
		const device = memory([a]);
		const s = api();
		const pending = new PendingScans(s.fetchFn, device.open);
		device.state.fail = true;
		await pending.send();
		device.state.fail = false;
		await pending.send();
		expect(device.state.opened).toBe(2);
		expect(s.codes()).toEqual([a]);
	});

	it('keeps a code whose removal failed and opens the store again', async () => {
		const device = memory([a]);
		const pending = new PendingScans(async () => {
			device.state.fail = true;
			return new Response('{}', { status: 201 });
		}, device.open);
		await pending.send();
		expect([...device.kept]).toEqual([a]);
		device.state.fail = false;
		await pending.keep(b);
		expect(device.state.opened).toBe(2);
	});
});

describe('PendingScans.start', () => {
	afterEach(() => {
		vi.useRealTimers();
	});

	function page() {
		const win = new EventTarget();
		const doc = Object.assign(new EventTarget(), {
			visibilityState: 'visible' as DocumentVisibilityState
		});
		return { win, doc };
	}

	it('sends at once, when the browser comes online, when shown and while codes wait', async () => {
		vi.useFakeTimers();
		const device = memory([a]);
		const s = api({ [a]: ['none', 'none', 'none', 'none', 201] });
		const pending = new PendingScans(s.fetchFn, device.open);
		const { win, doc } = page();
		const stop = pending.start(win, doc, 1000);
		await vi.advanceTimersByTimeAsync(0);
		expect(s.codes()).toEqual([a]);

		win.dispatchEvent(new Event('online'));
		await vi.advanceTimersByTimeAsync(0);
		expect(s.codes()).toHaveLength(2);

		doc.visibilityState = 'hidden';
		doc.dispatchEvent(new Event('visibilitychange'));
		await vi.advanceTimersByTimeAsync(0);
		expect(s.codes()).toHaveLength(2);
		doc.visibilityState = 'visible';
		doc.dispatchEvent(new Event('visibilitychange'));
		await vi.advanceTimersByTimeAsync(0);
		expect(s.codes()).toHaveLength(3);

		await vi.advanceTimersByTimeAsync(1000);
		expect(s.codes()).toHaveLength(4);
		await vi.advanceTimersByTimeAsync(1000);
		expect(s.codes()).toHaveLength(5);
		expect(pending.count).toBe(0);
		await vi.advanceTimersByTimeAsync(5000);
		expect(s.codes()).toHaveLength(5);

		stop();
		await pending.keep(b);
		win.dispatchEvent(new Event('online'));
		await vi.advanceTimersByTimeAsync(5000);
		expect(s.codes()).toHaveLength(5);
	});
});
