import { IDBFactory } from 'fake-indexeddb';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { requestDeadlineMs } from './api';
import { deleteQueueEntry } from './queue-deletes';
import { logScan, recentScans } from './recent.svelte';
import {
	PendingScans,
	browserLock,
	offlineDetail,
	openPendingStore,
	postTimeout,
	storageCursor,
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

	it('opens the store again after a write failed, counting it unavailable meanwhile', async () => {
		const device = memory();
		const pending = new PendingScans(api().fetchFn, device.open);
		device.state.fail = true;
		expect(await pending.keep(a)).toBe(false);
		expect(pending.available).toBe(false);
		device.state.fail = false;
		expect(await pending.keep(a)).toBe(true);
		expect(pending.available).toBe(true);
		expect(device.state.opened).toBe(2);
		expect(pending.count).toBe(1);
	});

	it.each([
		['a UPC-A and its EAN-13', '036000291452', '0036000291452'],
		['an ISBN-10 and its ISBN-13', '8324000011', '9788324000012'],
		['a padded EAN-8', '00000040170725', '40170725']
	])('keeps %s once, as the server stores it', async (_, first, second) => {
		const device = memory();
		const pending = new PendingScans(api().fetchFn, device.open);
		await pending.keep(first);
		await pending.keep(second);
		expect(pending.count).toBe(1);
		expect([...device.kept]).toEqual([second]);
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

	it('marks the waiting scans of a code the queue took as queued', async () => {
		recentScans.list = [];
		logScan(a, 'waiting', 'Zapisano bez połączenia');
		logScan(c, 'waiting', 'Zapisano bez połączenia');
		const pending = new PendingScans(api({ [c]: 422 }).fetchFn, memory([a, c]).open);
		await pending.send();
		expect(recentScans.list.map((r) => [r.barcode, r.outcome, r.href])).toEqual([
			[c, 'waiting', undefined],
			[a, 'queued', '/scan/queue']
		]);
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

	it('starts the next run after a code that got no answer, so it cannot hold back the rest', async () => {
		const device = memory([a, b, c]);
		const s = api({ [a]: 'none' });
		const pending = new PendingScans(s.fetchFn, device.open);
		await pending.send();
		expect(s.codes()).toEqual([a]);
		await pending.send();
		expect(s.codes()).toEqual([a, b, c, a]);
		expect([...device.kept]).toEqual([a]);
		await pending.send();
		expect(s.codes()).toEqual([a, b, c, a, a]);
		expect(pending.count).toBe(1);
	});

	it('moves past each code that gets no answer in turn, posting none twice in a run', async () => {
		const device = memory([a, b, c]);
		const s = api({ [a]: 'none', [b]: ['none', 201] });
		const pending = new PendingScans(s.fetchFn, device.open);
		await pending.send();
		await pending.send();
		expect(s.codes()).toEqual([a, b]);
		await pending.send();
		expect(s.codes()).toEqual([a, b, c, a]);
		await pending.send();
		expect(s.codes()).toEqual([a, b, c, a, b, a]);
		expect([...device.kept]).toEqual([a]);
	});

	it('starts the next run after a code whose POST hung', async () => {
		vi.useFakeTimers();
		try {
			const device = memory([a, b]);
			const posted: string[] = [];
			const pending = new PendingScans(async (_, init) => {
				const { barcode } = JSON.parse(String(init?.body)) as { barcode: string };
				posted.push(barcode);
				if (barcode === a) return new Promise<Response>(() => {});
				return new Response('{}', { status: 201 });
			}, device.open);
			const first = pending.send();
			await vi.advanceTimersByTimeAsync(postTimeout);
			await first;
			expect(posted).toEqual([a]);
			const second = pending.send();
			await vi.advanceTimersByTimeAsync(postTimeout);
			await second;
			expect(posted).toEqual([a, b, a]);
			expect([...device.kept]).toEqual([a]);
		} finally {
			vi.useRealTimers();
		}
	});

	it('starts after a code that got no answer after a reload or in another tab', async () => {
		const saved = new Map<string, string>();
		const storage = {
			getItem: (key: string) => saved.get(key) ?? null,
			setItem: (key: string, value: string) => void saved.set(key, value)
		};
		const device = memory([a, b]);
		const s = api({ [a]: 'none' });
		const page = () =>
			new PendingScans(
				s.fetchFn,
				device.open,
				undefined,
				storageCursor(() => storage)
			);
		await page().send();
		expect(s.codes()).toEqual([a]);
		await page().send();
		expect(s.codes()).toEqual([a, b, a]);
		expect([...device.kept]).toEqual([a]);
	});

	it('keeps the start in memory where storage refuses it', async () => {
		const refusing = {
			getItem: (): string | null => {
				throw new DOMException('denied', 'SecurityError');
			},
			setItem: () => {
				throw new DOMException('full', 'QuotaExceededError');
			}
		};
		const device = memory([a, b]);
		const s = api({ [a]: 'none' });
		const pending = new PendingScans(
			s.fetchFn,
			device.open,
			undefined,
			storageCursor(() => refusing)
		);
		await pending.send();
		await pending.send();
		expect(s.codes()).toEqual([a, b, a]);
		const missing = new PendingScans(
			s.fetchFn,
			memory([a, b]).open,
			undefined,
			storageCursor(() => undefined)
		);
		await missing.send();
		await missing.send();
		expect(s.codes()).toEqual([a, b, a, a, b, a]);
	});

	it('starts again from the first code after the sign-in turned one away', async () => {
		const device = memory([a, b]);
		const s = api({ [a]: [401, 201] });
		const pending = new PendingScans(s.fetchFn, device.open);
		await pending.send();
		await pending.send();
		expect(s.codes()).toEqual([a, a, b]);
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

	it('settles a code the queue took even when its answer breaks off', async () => {
		const device = memory([a]);
		const broken = new ReadableStream({
			start: (controller) => controller.error(new TypeError('network'))
		});
		const pending = new PendingScans(
			async () => new Response(broken, { status: 201 }),
			device.open
		);
		await pending.send();
		expect(device.kept.size).toBe(0);
	});

	it.each([
		['a 409 without the barcode field', '{"error":"conflict"}'],
		['a 409 that is not the API', 'Conflict']
	])('keeps a code on %s', async (_, body) => {
		const device = memory([a]);
		const pending = new PendingScans(async () => new Response(body, { status: 409 }), device.open);
		await pending.send();
		expect([...device.kept]).toEqual([a]);
	});

	it.each([
		['without a field', () => '{"error":"invalid"}'],
		['as plain text', () => 'Unprocessable'],
		[
			'whose body breaks off',
			() =>
				new ReadableStream({
					start: (controller) => controller.error(new TypeError('network'))
				})
		]
	])('drops a code on a 422 %s, as it can never succeed', async (_, body) => {
		const device = memory([a]);
		let posts = 0;
		const pending = new PendingScans(async () => {
			posts++;
			return new Response(body(), { status: 422 });
		}, device.open);
		await pending.send();
		await pending.send();
		expect(device.kept.size).toBe(0);
		expect(posts).toBe(1);
	});

	it('gives up on a POST that hangs, so later runs still send', async () => {
		vi.useFakeTimers();
		const device = memory([a]);
		let signal: AbortSignal | undefined;
		let calls = 0;
		const pending = new PendingScans(async (_, init) => {
			calls++;
			signal = init?.signal ?? undefined;
			if (calls === 1) return new Promise<Response>(() => {});
			return new Response('{}', { status: 201 });
		}, device.open);
		const first = pending.send();
		await vi.advanceTimersByTimeAsync(postTimeout);
		await first;
		expect(signal?.aborted).toBe(true);
		expect(pending.count).toBe(1);
		await pending.send();
		expect(device.kept.size).toBe(0);
		vi.useRealTimers();
	});

	it('waits for a queue removal on its way, and only until it fails unanswered', async () => {
		vi.useFakeTimers();
		try {
			vi.stubGlobal('fetch', () => new Promise<Response>(() => {}));
			const removal = deleteQueueEntry(9, undefined).catch(() => undefined);
			vi.unstubAllGlobals();
			const device = memory([a]);
			const s = api();
			const pending = new PendingScans(s.fetchFn, device.open);
			const run = pending.send();
			await vi.advanceTimersByTimeAsync(requestDeadlineMs - 1);
			expect(s.codes()).toEqual([]);
			await vi.advanceTimersByTimeAsync(1);
			await removal;
			await run;
			expect(s.codes()).toEqual([a]);
			expect(device.kept.size).toBe(0);
		} finally {
			vi.unstubAllGlobals();
			vi.useRealTimers();
		}
	});

	it('sends each code once when two tabs come back online together', async () => {
		const factory = new IDBFactory();
		const s = api();
		const tab = () => new PendingScans(s.fetchFn, () => openPendingStore(factory), browserLock);
		const one = tab();
		const two = tab();
		await one.keep(a);
		await one.keep(b);
		await Promise.all([one.send(), two.send()]);
		expect(s.codes().sort()).toEqual([a, b].sort());
		await two.send();
		expect(s.codes()).toHaveLength(2);
		expect(one.count).toBe(0);
		expect(two.count).toBe(0);
	});

	it('runs the send where the browser has no Web Locks', async () => {
		vi.stubGlobal('navigator', {});
		let ran = false;
		expect(
			await browserLock(async () => {
				ran = true;
			})
		).toBe(true);
		expect(ran).toBe(true);
		vi.unstubAllGlobals();
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

describe('PendingScans.departures', () => {
	it('goes up once for a run that took codes off the device', async () => {
		const device = memory([a, b]);
		const pending = new PendingScans(api().fetchFn, device.open);
		await pending.send();
		expect(pending.count).toBe(0);
		expect(pending.departures).toBe(1);
		await pending.send();
		expect(pending.departures).toBe(1);
	});

	it('stays when every code is kept for the next run', async () => {
		const device = memory([a]);
		const pending = new PendingScans(api({ [a]: 503 }).fetchFn, device.open);
		await pending.send();
		expect(pending.count).toBe(1);
		expect(pending.departures).toBe(0);
	});

	it('goes up when a code another tab kept meanwhile leaves the count level', async () => {
		const device = memory([a]);
		const there = new PendingScans(api().fetchFn, device.open);
		const s = api();
		const fetchFn: typeof fetch = async (input, init) => {
			await there.keep(b);
			return s.fetchFn(input, init);
		};
		const here = new PendingScans(fetchFn, device.open);
		await here.send();
		expect(s.codes()).toEqual([a]);
		expect(here.count).toBe(1);
		expect(here.departures).toBe(1);
	});

	it('goes up when a read finds fewer codes than this tab counted', async () => {
		const device = memory();
		const here = new PendingScans(api().fetchFn, device.open);
		await here.keep(a);
		await here.keep(b);
		expect(here.departures).toBe(0);
		device.kept.clear();
		await here.send();
		expect(here.count).toBe(0);
		expect(here.departures).toBe(1);
	});
});

describe('PendingScans.start', () => {
	it('sends on each tick a code another tab kept, though this tab counted none', async () => {
		vi.useFakeTimers();
		const device = memory();
		const s = api();
		const here = new PendingScans(s.fetchFn, device.open);
		const there = new PendingScans(s.fetchFn, device.open);
		const { win, doc } = page();
		const stop = here.start(win, doc, 1000);
		await vi.advanceTimersByTimeAsync(0);
		await there.keep(a);
		expect(here.count).toBe(0);
		await vi.advanceTimersByTimeAsync(1000);
		expect(s.codes()).toEqual([a]);
		stop();
	});

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
