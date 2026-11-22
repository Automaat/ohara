import { request } from './api';
import { queueDeletesSettled } from './queue-deletes';
import type { QueueEntry } from './scan';

type Change = { seq: number; barcode: string; queued: boolean };

/**
 * The codes in the scan queue, for the scanner's "Do uzupełnienia" count, kept without
 * downloading the queue around every scan: the scanner loads the list once, then each change
 * this tab makes moves the count. A scan the queue takes adds its code. An add that creates the
 * edition for a queued code takes the code out, as the server's trigger on `edition` does, and
 * undoing that add puts it back, as the edition's delete with `?requeue=1` does.
 */
export class QueueTally {
	/** The queued codes; null before the first answer and after a failed load. */
	#codes = $state<ReadonlySet<string> | null>(null);
	/** The queued codes adds took out, by the edition each created. */
	#taken = new Map<number, string>();
	/** Editions created while the queue was unknown, so whether they took a code out is too. */
	#unsure = new Set<number>();
	/** The editions this tab created for codes, by code, until an undo removes them. */
	#editions = new Map<string, number>();
	/** Every edition this tab created, with or without a code, until an undo removes it. */
	#created = new Set<number>();
	/** Changes made while a load is out, replayed over its answer. */
	#changes: Change[] = [];
	#seq = 0;
	#loading = 0;
	#issued = 0;
	#applied = 0;
	#fetch: typeof fetch;

	/** fetchFn reads the queue again when an undo cannot tell what it put back. */
	constructor(fetchFn: typeof fetch = (input, init) => fetch(input, init)) {
		this.#fetch = fetchFn;
	}

	/** How many codes wait in the queue; null while unknown. */
	get count(): number | null {
		return this.#codes?.size ?? null;
	}

	/**
	 * Reads the queue once the queue page's deletes have settled. Changes made while the request
	 * is out apply over its answer, and an answer older than the last one applied is dropped.
	 */
	async load(fetchFn: typeof fetch): Promise<void> {
		const ticket = ++this.#issued;
		const since = this.#seq;
		this.#loading++;
		try {
			await queueDeletesSettled();
			const entries = await request<QueueEntry[]>(fetchFn, 'GET', '/api/scan-queue');
			if (ticket < this.#applied) return;
			this.#applied = ticket;
			const codes = new Set(entries.map((e) => e.barcode));
			for (const c of this.#changes) if (c.seq > since) apply(codes, c);
			this.#codes = codes;
		} catch {
			if (ticket < this.#applied) return;
			this.#applied = ticket;
			this.#codes = null;
		} finally {
			if (--this.#loading === 0) this.#changes = [];
		}
	}

	/**
	 * A scan the queue took; a code it already held stays counted once. The server queues no code
	 * that has an edition, so an answer arriving after this tab created the code's edition was
	 * queued before it: the edition took the code out again and its undo puts it back.
	 */
	queued(barcode: string) {
		const edition = this.#editions.get(barcode);
		if (edition === undefined) this.#change(barcode, true);
		else if (!this.#unsure.has(edition)) this.#taken.set(edition, barcode);
	}

	/**
	 * An add created the edition for barcode, which takes the code out of the queue. A code the
	 * queue held is remembered by the edition, so undoing the add can count it again; an edition
	 * created before the queue was known is remembered as unsure. An edition without a code takes
	 * nothing out, but is remembered as one this tab knows.
	 */
	resolved(editionID: number, barcode: string | null) {
		this.#created.add(editionID);
		if (barcode === null) return;
		this.#editions.set(barcode, editionID);
		if (this.#codes === null) this.#unsure.add(editionID);
		else if (this.#codes.has(barcode)) this.#taken.set(editionID, barcode);
		this.#change(barcode, false);
	}

	/**
	 * The edition an add created is gone, and the queue took back the code it had resolved. For
	 * an edition created while the queue was unknown, or one this tab never saw created, such as
	 * an add kept from before a reload, the queue is read again instead.
	 */
	requeued(editionID: number): Promise<void> {
		for (const [code, id] of this.#editions) if (id === editionID) this.#editions.delete(code);
		const known = this.#created.delete(editionID);
		if (this.#unsure.delete(editionID) || !known) return this.load(this.#fetch);
		const barcode = this.#taken.get(editionID);
		if (barcode !== undefined) {
			this.#taken.delete(editionID);
			this.#change(barcode, true);
		}
		return Promise.resolve();
	}

	#change(barcode: string, queued: boolean) {
		const change = { seq: ++this.#seq, barcode, queued };
		if (this.#loading > 0) this.#changes.push(change);
		if (!this.#codes) return;
		const codes = new Set(this.#codes);
		apply(codes, change);
		this.#codes = codes;
	}
}

function apply(codes: Set<string>, c: Change) {
	if (c.queued) codes.add(c.barcode);
	else codes.delete(c.barcode);
}

/** The tab's one tally: the scanner shows it, and adds and their undos keep it current. */
export const queueTally = new QueueTally();

/** How often the scanner reads the queue again while its count is unknown after a failed read. */
export const retryInterval = 20_000;

/**
 * Reads the queue again while the tally's count is unknown, as after a failed read: when the
 * browser comes online, when the app is shown again and every interval, so a count lost to one
 * failed read comes back once the API answers. Returns what stops it.
 */
export function retryUnknown(
	tally: QueueTally,
	fetchFn: typeof fetch,
	win: Pick<EventTarget, 'addEventListener' | 'removeEventListener'>,
	doc: Pick<Document, 'addEventListener' | 'removeEventListener' | 'visibilityState'>,
	every = retryInterval
): () => void {
	const retry = () => {
		if (tally.count === null) void tally.load(fetchFn);
	};
	const shown = () => {
		if (doc.visibilityState === 'visible') retry();
	};
	win.addEventListener('online', retry);
	doc.addEventListener('visibilitychange', shown);
	const timer = setInterval(retry, every);
	return () => {
		win.removeEventListener('online', retry);
		doc.removeEventListener('visibilitychange', shown);
		clearInterval(timer);
	};
}
