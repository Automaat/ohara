import type { ToastEnd, ToastOptions } from '@smykla-skalski/sui';
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
};

/**
 * The toast after each add from the scanner: what went in and "Cofnij", for `addToastMs` away
 * from the toast, the clock restarting in full each time the reader comes back to it. A newer
 * add replaces it, so it never stacks over the camera. "Cofnij" undoes the add and says so; a
 * failed undo stays as a failure with "Spróbuj ponownie".
 */
export class AddToasts {
	readonly #host: ToastHost;
	readonly #undo: (add: SessionAdd) => Promise<void>;
	readonly #window: number;
	/** The add toast on show and its clock. */
	#shown: { id: number; timer: ReturnType<typeof setInterval> } | null = null;
	/** Every toast still up, withdrawn when the page goes away. */
	readonly #up = new Set<number>();
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

	/** Shows what the add put in, with "Cofnij", in place of the last add's toast. */
	show(add: SessionAdd): void {
		if (!this.#live) return;
		if (this.#shown) this.#host.dismiss(this.#shown.id, 'withdrawn');
		const id = this.#raise({
			title: add.status === 'owned' ? 'Dodano do kolekcji' : 'Dodano do listy życzeń',
			description: addLine(add),
			action: { label: 'Cofnij', run: () => void this.#revert(add) },
			ondismiss: () => this.#settle(id)
		});
		let left = this.#window;
		const timer = setInterval(() => {
			if (this.#host.holding(id)) {
				left = this.#window;
				return;
			}
			left -= holdCheckMs;
			if (left <= 0) this.#host.dismiss(id, 'expired');
		}, holdCheckMs);
		this.#shown = { id, timer };
	}

	/** Takes every toast back and stops reporting, for a page that is gone. */
	destroy(): void {
		this.#live = false;
		for (const id of [...this.#up]) this.#host.dismiss(id, 'withdrawn');
	}

	#raise(options: ToastOptions): number {
		const id = this.#host.toast({
			...options,
			ondismiss: (reason) => {
				this.#up.delete(id);
				options.ondismiss?.(reason);
			}
		});
		this.#up.add(id);
		return id;
	}

	#settle(id: number): void {
		if (this.#shown?.id !== id) return;
		clearInterval(this.#shown.timer);
		this.#shown = null;
	}

	async #revert(add: SessionAdd): Promise<void> {
		try {
			await this.#undo(add);
			if (this.#live) this.#raise({ title: 'Cofnięto dodanie', description: addLine(add) });
		} catch (err) {
			if (!this.#live) return;
			this.#raise({
				title: `Nie udało się cofnąć: ${addLine(add)}`,
				description: errorMessage(err),
				tone: 'danger',
				action: { label: 'Spróbuj ponownie', run: () => void this.#revert(add) }
			});
		}
	}
}
