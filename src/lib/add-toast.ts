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
	/** Whether the screen has room for one toast only, so each new one takes the last one's place. */
	single(): boolean;
	/** Whether the reader has the visit's list open, so the clocks of the adds it offers wait. */
	listing(): boolean;
};

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
 * Where only one toast fits, each new toast takes the last one's place, and an add whose toast
 * gave way stays in `listed`, undoable from the visit's list, for the rest of its window.
 */
export class AddToasts {
	readonly #host: ToastHost;
	readonly #undo: (add: SessionAdd) => Promise<void>;
	readonly #window: number;
	/** Each add still undoable, by add id. */
	readonly #offers = new Map<number, Offer>();
	/** Every toast still up, oldest first, with the add it offers to undo. */
	readonly #up = new Map<number, SessionAdd | null>();
	readonly #listed = new SvelteSet<number>();
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
		const id = this.#raise(
			{
				title: add.status === 'owned' ? 'Dodano do kolekcji' : 'Dodano do listy życzeń',
				description: addLine(add),
				action: { label: 'Cofnij', run: () => void this.#revert(add) },
				ondismiss: () => {
					if (this.#offers.get(add.id)?.toast === id) this.#end(add);
				}
			},
			add
		);
		this.#offer(add, id);
	}

	/** Undoes a listed add, from its "Cofnij" in the visit's list. */
	undo(add: SessionAdd): void {
		if (!this.#live || !this.#listed.has(add.id)) return;
		this.#end(add);
		void this.#revert(add);
	}

	/** Leaves only the newest toast up, for a screen that has just become too small for more. */
	collapse(): void {
		this.#giveWay([...this.#up.keys()].slice(0, -1));
	}

	/** Takes every toast back and stops reporting, for a page that is gone. */
	destroy(): void {
		this.#live = false;
		for (const id of [...this.#up.keys()]) this.#host.dismiss(id, 'withdrawn');
		for (const offer of this.#offers.values()) clearInterval(offer.timer);
		this.#offers.clear();
		this.#listed.clear();
	}

	#raise(options: ToastOptions, add: SessionAdd | null): number {
		if (this.#host.single()) this.#giveWay([...this.#up.keys()]);
		const id = this.#host.toast({
			...options,
			ondismiss: (reason) => {
				this.#up.delete(id);
				options.ondismiss?.(reason);
			}
		});
		this.#up.set(id, add);
		return id;
	}

	/**
	 * Takes the toasts back, each add they offer moving to the list: an add toast keeps what is
	 * left of its window, a failure starts a new one.
	 */
	#giveWay(ids: number[]): void {
		for (const id of ids) {
			const add = this.#up.get(id);
			if (add) {
				const offer = this.#offers.get(add.id);
				if (offer) offer.toast = null;
				else this.#offer(add, null);
				this.#listed.add(add.id);
			}
			this.#host.dismiss(id, 'withdrawn');
		}
	}

	/** Starts an add's window, on its toast or, with `toast` null, in the list. */
	#offer(add: SessionAdd, toast: number | null): void {
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
			if (this.#live) this.#raise({ title: 'Cofnięto dodanie', description: addLine(add) }, null);
		} catch (err) {
			if (!this.#live) return;
			this.#raise(
				{
					title: `Nie udało się cofnąć: ${addLine(add)}`,
					description: errorMessage(err),
					tone: 'danger',
					action: { label: 'Spróbuj ponownie', run: () => void this.#revert(add) }
				},
				add
			);
		}
	}
}
