import type { ToastEnd, ToastOptions } from '@smykla-skalski/sui';
import { SvelteSet } from 'svelte/reactivity';
import { errorMessage } from './api.js';
import { addLine, type SessionAdd } from './session.svelte.js';

/** How long "Cofnij" stays on offer after an add while the reader is away from its toast. */
export const addToastMs = 6000;

/** How often the clock checks whether the reader is on the toast, in milliseconds. */
const holdCheckMs = 250;

/** What the add toasts need from the page: its toasts. */
export type ToastHost = {
	/** Raises a toast and returns its id. */
	toast(options: ToastOptions): number;
	/** Takes a toast back; its `ondismiss` hears `reason`. */
	dismiss(id: number, reason: ToastEnd): void;
	/** Whether the reader is on the toast, by pointer or focus, so its clock waits. */
	holding(id: number): boolean;
	/** Whether the screen has room for one toast only, which the newest add's toast takes first. */
	single(): boolean;
	/** Whether the reader has the visit's list open, so the clocks of the adds it offers wait. */
	listing(): boolean;
};

/** A toast to raise, with the add whose toast it is: its add toast or the failure of its undo. */
type Pending = { options: ToastOptions; add: SessionAdd | null; offer: boolean };

/** An add's way back while its window runs: on its toast, or in the list once the toast gave way. */
type Offer = {
	/** The toast that offers it, or null when only the list does. */
	toast: number | null;
	left: number;
	timer: ReturnType<typeof setInterval>;
};

/**
 * The toast after each add from the scanner: what went in and "Cofnij", for `addToastMs` away
 * from the toast, the clock restarting in full each time the reader comes back to it. Each add
 * keeps its own toast and clock, so a quick next add never cuts the last one's window short.
 * "Cofnij" undoes the add and says so; a failed undo stays as a failure with "Spróbuj ponownie".
 *
 * Where only one toast fits, the newest add's toast takes the place: an add whose toast gave way
 * or was closed stays in `listed`, undoable from the visit's list, for the rest of its window,
 * and any other toast waits its turn until the place is free.
 */
export class AddToasts {
	readonly #host: ToastHost;
	readonly #undo: (add: SessionAdd) => Promise<void>;
	readonly #window: number;
	/** Each add still undoable, by add id. */
	readonly #offers = new Map<number, Offer>();
	/** Every toast still up, oldest first. */
	readonly #up = new Map<number, Pending>();
	/** Toasts waiting for the one place, oldest first. */
	#waiting: Pending[] = [];
	readonly #listed = new SvelteSet<number>();
	/** Set while toasts give way, so the place they leave is not handed on yet. */
	#yielding = false;
	#live = true;

	constructor(
		host: ToastHost,
		undo: (add: SessionAdd) => Promise<void>,
		window: number = addToastMs
	) {
		this.#host = host;
		this.#undo = undo;
		this.#window = window;
	}

	/** The ids of the adds whose toast gave way, undoable from the visit's list. */
	get listed(): ReadonlySet<number> {
		return this.#listed;
	}

	/** Shows what the add put in, with "Cofnij". */
	show(add: SessionAdd): void {
		if (!this.#live) return;
		const options: ToastOptions = {
			title: add.status === 'owned' ? 'Dodano do kolekcji' : 'Dodano do listy życzeń',
			description: addLine(add),
			action: { label: 'Cofnij', run: () => void this.#revert(add) }
		};
		if (this.#host.single()) this.#giveWay([...this.#up.keys()]);
		this.#offer(add, this.#raise({ options, add, offer: true }));
	}

	/** Undoes a listed add, from its "Cofnij" in the visit's list. */
	undo(add: SessionAdd): void {
		if (!this.#live || !this.#listed.has(add.id)) return;
		this.#end(add);
		void this.#revert(add);
	}

	/**
	 * Fits the toasts to a screen that has just changed: where only one fits (`single`), the newest
	 * add's toast stays up, else the newest toast; where more fit, every waiting toast comes up.
	 */
	fit(single: boolean): void {
		if (!this.#live) return;
		if (!single) {
			this.#next();
			return;
		}
		const ids = [...this.#up.keys()];
		const keep = ids.findLast((id) => this.#up.get(id)?.offer) ?? ids.at(-1);
		this.#giveWay(ids.filter((id) => id !== keep));
	}

	/** Takes every toast back and stops reporting, for a page that is gone. */
	destroy(): void {
		this.#live = false;
		this.#waiting = [];
		for (const id of [...this.#up.keys()]) this.#host.dismiss(id, 'withdrawn');
		for (const offer of this.#offers.values()) clearInterval(offer.timer);
		this.#offers.clear();
		this.#listed.clear();
	}

	/** Raises a toast other than an add's, which waits while another holds the one place. */
	#tell(pending: Pending): void {
		if (this.#host.single() && this.#up.size > 0) this.#waiting.push(pending);
		else this.#raise(pending);
	}

	#raise(pending: Pending): number {
		const { options, add } = pending;
		const id = this.#host.toast({
			...options,
			ondismiss: (reason) => {
				this.#up.delete(id);
				options.ondismiss?.(reason);
				const offer = add && this.#offers.get(add.id);
				if (add && offer && offer.toast === id) {
					// Closed where one toast fits, its add stays undoable from the list.
					if (reason === 'dismissed' && this.#host.single()) this.#list(add, offer);
					else this.#end(add);
				}
				if (!this.#yielding) this.#next();
			}
		});
		this.#up.set(id, pending);
		return id;
	}

	/** Hands the place on: to the next waiting toast where one fits, to all of them where more do. */
	#next(): void {
		if (!this.#live) return;
		const single = this.#host.single();
		if (single && this.#up.size > 0) return;
		const next = single ? this.#waiting.splice(0, 1) : this.#waiting.splice(0);
		for (const pending of next) this.#raise(pending);
	}

	/**
	 * Takes the toasts back: an add toast's add moves to the list with what is left of its window,
	 * any other toast goes back to the head of the line.
	 */
	#giveWay(ids: number[]): void {
		const back: Pending[] = [];
		this.#yielding = true;
		for (const id of ids) {
			const pending = this.#up.get(id);
			const offer = pending?.add && this.#offers.get(pending.add.id);
			if (pending?.add && offer && offer.toast === id) {
				this.#list(pending.add, offer);
			} else if (pending) {
				back.push(pending);
			}
			this.#host.dismiss(id, 'withdrawn');
		}
		this.#yielding = false;
		this.#waiting = [...back, ...this.#waiting];
	}

	/** Moves an add from its toast to the list, with what is left of its window. */
	#list(add: SessionAdd, offer: Offer): void {
		offer.toast = null;
		this.#listed.add(add.id);
	}

	/** Starts an add's window on its toast; once the toast gives way, the list offers it. */
	#offer(add: SessionAdd, toast: number): void {
		const offer: Offer = {
			toast,
			left: this.#window,
			timer: setInterval(() => {
				const held = offer.toast === null ? this.#host.listing() : this.#host.holding(offer.toast);
				if (held) {
					offer.left = this.#window;
					return;
				}
				offer.left -= holdCheckMs;
				if (offer.left > 0) return;
				if (offer.toast === null) this.#end(add);
				else this.#host.dismiss(offer.toast, 'expired');
			}, holdCheckMs)
		};
		this.#offers.set(add.id, offer);
	}

	#end(add: SessionAdd): void {
		clearInterval(this.#offers.get(add.id)?.timer);
		this.#offers.delete(add.id);
		this.#listed.delete(add.id);
	}

	async #revert(add: SessionAdd): Promise<void> {
		try {
			await this.#undo(add);
			if (this.#live) {
				this.#tell({
					options: { title: 'Cofnięto dodanie', description: addLine(add) },
					add: null,
					offer: false
				});
			}
		} catch (err) {
			if (!this.#live) return;
			this.#tell({
				options: {
					title: `Nie udało się cofnąć: ${addLine(add)}`,
					description: errorMessage(err),
					tone: 'danger',
					action: { label: 'Spróbuj ponownie', run: () => void this.#revert(add) }
				},
				add,
				offer: false
			});
		}
	}
}
