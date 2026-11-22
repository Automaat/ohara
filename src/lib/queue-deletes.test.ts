import { afterEach, describe, expect, it, vi } from 'vitest';
import { noAnswerMessage, requestDeadlineMs } from './api';
import { deleteQueueEntry, queueDeletesSettled, type DeleteStore } from './queue-deletes';

/** A tab's session storage. */
function store(ids?: number[]): DeleteStore & { items: Map<string, string> } {
	const items = new Map<string, string>();
	if (ids) items.set('ohara.queue.deleting', JSON.stringify(ids));
	return {
		items,
		getItem: (key) => items.get(key) ?? null,
		setItem: (key, value) => void items.set(key, value)
	};
}

/** A server whose DELETE answers wait until the test releases them. */
function server(status = 204) {
	const sent: { url: string; init?: RequestInit }[] = [];
	const waiting: (() => void)[] = [];
	vi.stubGlobal('fetch', (input: RequestInfo | URL, init?: RequestInit) => {
		sent.push({ url: String(input), init });
		return new Promise<Response>((resolve) =>
			waiting.push(() =>
				resolve(new Response(status === 204 ? null : '{"error":"boom"}', { status }))
			)
		);
	});
	return {
		sent,
		answerAll: () => waiting.splice(0).forEach((answer) => answer())
	};
}

async function isSettled(promise: Promise<void>): Promise<boolean> {
	let done = false;
	void promise.then(() => (done = true));
	await new Promise((resolve) => setTimeout(resolve, 0));
	return done;
}

describe('queue deletes in flight', () => {
	afterEach(() => {
		vi.unstubAllGlobals();
	});

	it('sends the DELETE with keepalive and holds later reads until it settles', async () => {
		const s = server();
		const tab = store();
		const removal = deleteQueueEntry(7, tab);
		expect(s.sent.map((r) => [r.url, r.init?.method, r.init?.keepalive])).toEqual([
			['/api/scan-queue/7', 'DELETE', true]
		]);
		expect(JSON.parse(tab.items.get('ohara.queue.deleting') ?? '')).toEqual([7]);
		const read = queueDeletesSettled(tab);
		expect(await isSettled(read)).toBe(false);
		s.answerAll();
		await removal;
		expect(await isSettled(read)).toBe(true);
		expect(JSON.parse(tab.items.get('ohara.queue.deleting') ?? '')).toEqual([]);
	});

	it('lets later reads go on and forgets the entry when the DELETE fails', async () => {
		const s = server(503);
		const tab = store();
		const removal = deleteQueueEntry(7, tab);
		const read = queueDeletesSettled(tab);
		s.answerAll();
		await expect(removal).rejects.toThrow();
		expect(await isSettled(read)).toBe(true);
		expect(JSON.parse(tab.items.get('ohara.queue.deleting') ?? '')).toEqual([]);
	});

	it('fails a DELETE that gets no answer by the deadline, so later reads go on', async () => {
		vi.useFakeTimers();
		try {
			const signals: AbortSignal[] = [];
			vi.stubGlobal('fetch', (_: RequestInfo | URL, init?: RequestInit) => {
				if (init?.signal) signals.push(init.signal);
				return new Promise<Response>(() => {});
			});
			const tab = store();
			const removal = deleteQueueEntry(7, tab);
			const failed = removal.catch((err: unknown) => err);
			let read = false;
			void queueDeletesSettled(tab).then(() => (read = true));
			await vi.advanceTimersByTimeAsync(requestDeadlineMs - 1);
			expect(read).toBe(false);
			await vi.advanceTimersByTimeAsync(1);
			expect(await failed).toMatchObject({ status: 0, message: noAnswerMessage });
			expect(read).toBe(true);
			expect(signals[0]?.aborted).toBe(true);
			expect(JSON.parse(tab.items.get('ohara.queue.deleting') ?? '')).toEqual([]);
		} finally {
			vi.useRealTimers();
		}
	});

	it('sends again, once, the DELETEs a reload cut short before reading the queue', async () => {
		const s = server();
		const tab = store([3, 4]);
		const read = queueDeletesSettled(tab);
		expect(s.sent.map((r) => r.url)).toEqual(['/api/scan-queue/3', '/api/scan-queue/4']);
		expect(await isSettled(read)).toBe(false);
		s.answerAll();
		expect(await isSettled(read)).toBe(true);
		expect(JSON.parse(tab.items.get('ohara.queue.deleting') ?? '')).toEqual([]);
		await queueDeletesSettled(tab);
		expect(s.sent).toHaveLength(2);
	});

	it('settles a resent DELETE that the first request already made', async () => {
		const s = server(404);
		const tab = store([3]);
		const read = queueDeletesSettled(tab);
		s.answerAll();
		expect(await isSettled(read)).toBe(true);
		expect(JSON.parse(tab.items.get('ohara.queue.deleting') ?? '')).toEqual([]);
	});

	it('works without session storage or with storage that refuses', async () => {
		const s = server();
		const refusing: DeleteStore = {
			getItem: () => {
				throw new Error('denied');
			},
			setItem: () => {
				throw new Error('denied');
			}
		};
		const removal = deleteQueueEntry(1, refusing);
		const bare = deleteQueueEntry(2, undefined);
		s.answerAll();
		await Promise.all([removal, bare]);
		expect(await isSettled(queueDeletesSettled(refusing))).toBe(true);
		expect(await isSettled(queueDeletesSettled(undefined))).toBe(true);
		expect(s.sent).toHaveLength(2);
	});

	it('ignores a stored value that is not a list of ids', async () => {
		const s = server();
		const tab = store();
		tab.items.set('ohara.queue.deleting', '{"id":3}');
		expect(await isSettled(queueDeletesSettled(tab))).toBe(true);
		tab.items.set('ohara.queue.deleting', 'not json');
		const other = store();
		other.items.set('ohara.queue.deleting', '[1.5, "2", 8]');
		const read = queueDeletesSettled(other);
		expect(s.sent.map((r) => r.url)).toEqual(['/api/scan-queue/8']);
		s.answerAll();
		expect(await isSettled(read)).toBe(true);
	});
});
