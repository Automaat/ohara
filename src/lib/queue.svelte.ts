import type { ToastEnd, ToastOptions } from '@smykla-skalski/sui';
import { SvelteSet } from 'svelte/reactivity';
import { ApiError, errorMessage } from './api.js';
import { trackDelete } from './queue-deletes.js';
import type { QueueEntry } from './scan.js';

/** How long "Cofnij" stays on offer before a removal goes to the server. */
export const undoWindowMs = 8000;

/** How far a swiped row slides to show "Usuń", in CSS pixels. */
export const swipeWidth = 92;

/** How far a touch moves before it counts as a swipe or a scroll, in CSS pixels. */
const swipeSlop = 8;

/** Which way a touch goes once it has moved past the slop: `x` swipes the row, `y` scrolls. */
export function swipeAxis(dx: number, dy: number): 'x' | 'y' | null {
	if (Math.abs(dx) < swipeSlop && Math.abs(dy) < swipeSlop) return null;
	return Math.abs(dx) > Math.abs(dy) ? 'x' : 'y';
}

/** Where a row's face sits when dragged by `dx` from `base`: between shut (0) and open. */
export function swipeOffset(base: number, dx: number): number {
	return Math.min(0, Math.max(-swipeWidth, base + dx));
}

/** Whether a released row stays open: it was dragged past half of "Usuń". */
export function swipeOpens(offset: number): boolean {
	return offset <= -swipeWidth / 2;
}

/** A 404 means the entry already left the queue, as when an edition took its barcode. */
function alreadyGone(err: unknown): boolean {
	return err instanceof ApiError && err.status === 404;
}

/** What a removal needs from the page: its toasts and the queue API. */
export type RemovalHost = {
	/** Raises a toast and returns its id. */
	toast(options: ToastOptions): number;
	/** Takes a toast back; its `ondismiss` hears `reason`. */
	dismiss(id: number, reason: ToastEnd): void;
	/** Whether the reader is on the toast, by pointer or focus, so its clock waits. */
	holding(id: number): boolean;
	/** Deletes the entry on the server. */
	send(entry: QueueEntry): Promise<void>;
	/** The server no longer holds the entry. */
	removed(entry: QueueEntry): void;
};

type Pending = { entry: QueueEntry; toast: number; timer?: ReturnType<typeof setTimeout> };

/**
 * Removals from the scan queue that wait for their undo toast. A removed entry hides at once
 * and its toast offers "Cofnij"; the DELETE goes out only when the toast closes some other way:
 * dismissed, expired after `undoWindowMs`, or flushed when the page goes away.
 */
export class QueueRemovals {
	/** Ids of the entries hidden while their removal waits or is on its way. */
	readonly hidden = new SvelteSet<number>();
	readonly #host: RemovalHost;
	readonly #window: number;
	readonly #pending = new Map<number, Pending>();
	#live = true;

	constructor(host: RemovalHost, window = undoWindowMs) {
		this.#host = host;
		this.#window = window;
	}

	/** Hides the entry and offers "Cofnij"; a second removal of a waiting entry does nothing. */
	remove(entry: QueueEntry): void {
		if (this.#pending.has(entry.id)) return;
		this.hidden.add(entry.id);
		const pending: Pending = { entry, toast: 0 };
		this.#pending.set(entry.id, pending);
		pending.toast = this.#host.toast({
			title: `Usunięto kod ${entry.barcode}`,
			action: { label: 'Cofnij', run: () => this.#restore(entry.id) },
			ondismiss: (reason) => this.#closed(entry.id, reason)
		});
		this.#arm(pending);
	}

	/** Sends every waiting removal now, as when the page goes away. */
	flush(): void {
		for (const pending of [...this.#pending.values()]) {
			this.#host.dismiss(pending.toast, 'withdrawn');
		}
	}

	/** Sends every waiting removal and stops reporting failures, for a page that is gone. */
	destroy(): void {
		this.flush();
		this.#live = false;
	}

	#arm(pending: Pending): void {
		pending.timer = setTimeout(() => {
			if (this.#host.holding(pending.toast)) this.#arm(pending);
			else this.#host.dismiss(pending.toast, 'expired');
		}, this.#window);
	}

	#settle(id: number): Pending | undefined {
		const pending = this.#pending.get(id);
		if (!pending) return undefined;
		clearTimeout(pending.timer);
		this.#pending.delete(id);
		return pending;
	}

	#restore(id: number): void {
		const pending = this.#settle(id);
		if (pending) this.#host.dismiss(pending.toast, 'acted');
		this.hidden.delete(id);
	}

	/**
	 * The toast runs its action right after it closes with `acted`, so `acted` leaves the
	 * entry to `#restore`; any other close sends the removal.
	 */
	#closed(id: number, reason: ToastEnd): void {
		const pending = this.#settle(id);
		if (pending && reason !== 'acted') void this.#commit(pending.entry);
	}

	async #commit(entry: QueueEntry): Promise<void> {
		try {
			await trackDelete(this.#host.send(entry));
		} catch (err) {
			if (!alreadyGone(err)) {
				this.hidden.delete(entry.id);
				if (this.#live) {
					this.#host.toast({
						title: `Nie udało się usunąć kodu ${entry.barcode}`,
						description: errorMessage(err),
						tone: 'danger'
					});
				}
				return;
			}
		}
		this.#host.removed(entry);
		this.hidden.delete(entry.id);
	}
}
