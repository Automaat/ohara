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
	/** Changes made while a load is out, replayed over its answer. */
	#changes: Change[] = [];
	#seq = 0;
	#loading = 0;
	#issued = 0;
	#applied = 0;

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

	/** A scan the queue took; a code it already held stays counted once. */
	queued(barcode: string) {
		this.#change(barcode, true);
	}

	/**
	 * An add created the edition for barcode, which takes the code out of the queue. A code the
	 * queue held is remembered by the edition, so undoing the add can count it again; an edition
	 * created before the queue was known is remembered as unsure.
	 */
	resolved(editionID: number, barcode: string) {
		if (this.#codes === null) this.#unsure.add(editionID);
		else if (this.#codes.has(barcode)) this.#taken.set(editionID, barcode);
		this.#change(barcode, false);
	}

	/**
	 * The edition an add created is gone, and the queue took back the code it had resolved. For
	 * an edition created while the queue was unknown, the queue is read again instead.
	 */
	requeued(editionID: number, fetchFn: typeof fetch): Promise<void> {
		if (this.#unsure.delete(editionID)) return this.load(fetchFn);
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
