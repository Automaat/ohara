import { request } from './api.js';

/**
 * Scan queue DELETEs on their way to the server. A removal often goes out as the queue page
 * closes, or as the browser reloads it, so a later read of the queue, or a scan that would queue
 * the same barcode again, waits for them instead of racing them. A DELETE that a reload cut
 * short is remembered in the tab's session storage and sent again by the next page.
 */
export type DeleteStore = Pick<Storage, 'getItem' | 'setItem'>;

const storeKey = 'ohara.queue.deleting';
const inFlight = new Set<Promise<unknown>>();
const resumed = new WeakSet<DeleteStore>();

/**
 * Whether the page is going away. The browser then fails the requests it still holds, though
 * `keepalive` lets them reach the server, so their ids stay stored for the next page.
 */
let leaving = false;
if (typeof addEventListener === 'function') {
	addEventListener('pagehide', () => (leaving = true));
	addEventListener('pageshow', () => (leaving = false));
}

/** A removal sent as the page goes away still reaches the server with `keepalive`. */
const keepalive: typeof fetch = (input, init) => fetch(input, { ...init, keepalive: true });

function sessionStore(): DeleteStore | undefined {
	try {
		return globalThis.sessionStorage;
	} catch {
		return undefined;
	}
}

function readIds(store: DeleteStore | undefined): number[] {
	try {
		const parsed: unknown = JSON.parse(store?.getItem(storeKey) ?? '[]');
		return Array.isArray(parsed) ? parsed.filter((id) => Number.isInteger(id)) : [];
	} catch {
		return [];
	}
}

function writeIds(store: DeleteStore | undefined, ids: number[]): boolean {
	try {
		store?.setItem(storeKey, JSON.stringify(ids));
		return true;
	} catch {
		return false;
	}
}

/** Deletes a queue entry and keeps the request in view until it settles, across a reload too. */
export function deleteQueueEntry(id: number, store = sessionStore()): Promise<void> {
	writeIds(store, [...new Set([...readIds(store), id])]);
	const sent = request<void>(keepalive, 'DELETE', `/api/scan-queue/${id}`);
	inFlight.add(sent);
	const settle = () => {
		inFlight.delete(sent);
		if (leaving) return;
		writeIds(
			store,
			readIds(store).filter((other) => other !== id)
		);
	};
	sent.then(settle, settle);
	return sent;
}

/**
 * Resolves once every queue DELETE sent so far has settled, whatever its outcome. The first call
 * in a page sends again the DELETEs a reload cut short.
 */
export async function queueDeletesSettled(store = sessionStore()): Promise<void> {
	if (store && !resumed.has(store)) {
		resumed.add(store);
		for (const id of readIds(store)) {
			deleteQueueEntry(id, store).catch(() => undefined);
		}
	}
	await Promise.allSettled([...inFlight]);
}
