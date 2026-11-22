import type { ToastEnd, ToastOptions } from '@smykla-skalski/sui';
import { SvelteSet } from 'svelte/reactivity';
import { errorMessage } from './api.js';
import type { Observation } from './deals.js';

/** What dismissals need from the deals page: its toasts, the API, the badge and a reload. */
export type DismissalHost = {
	/** Raises a toast and returns its id. */
	toast(options: ToastOptions): number;
	/** Takes a toast back; its `ondismiss` hears `reason`. */
	dismiss(id: number, reason: ToastEnd): void;
	/** POST dismisses the listing on the server, DELETE brings it back. */
	send(method: 'POST' | 'DELETE', id: number): Promise<unknown>;
	/** Takes the listing out of the badge and drops badge answers to earlier requests. */
	forget(id: number): void;
	/** Asks the server for the listings again. */
	reload(): Promise<void>;
};

type Deal = Pick<Observation, 'id' | 'listing_title'>;

/**
 * "Ignoruj" on the deals page. A dismissed listing hides at once and its toast offers "Cofnij".
 * Both requests are idempotent, so a failed one, whose outcome is unknown, raises a toast that
 * sends it again. The toasts belong to the page: `destroy` takes them back, so a later visit never
 * runs an action for a page that is gone.
 */
export class DealDismissals {
	/** Ids of the listings dismissed here, hidden before the server's next answer leaves them out. */
	readonly hidden = new SvelteSet<number>();
	readonly #host: DismissalHost;
	readonly #toasts = new Set<number>();
	#live = true;

	constructor(host: DismissalHost) {
		this.#host = host;
	}

	/** Hides the listing, dismisses it on the server and offers "Cofnij". */
	async dismiss(deal: Deal): Promise<void> {
		this.hidden.add(deal.id);
		this.#host.forget(deal.id);
		try {
			await this.#host.send('POST', deal.id);
		} catch (err) {
			this.hidden.delete(deal.id);
			this.#raise({
				title: 'Nie udało się zignorować oferty',
				description: errorMessage(err),
				tone: 'danger',
				action: { label: 'Spróbuj ponownie', run: () => void this.dismiss(deal) }
			});
			await this.#reload();
			return;
		}
		this.#host.forget(deal.id);
		this.#raise({
			title: 'Zignorowano ofertę',
			description: deal.listing_title || undefined,
			action: { label: 'Cofnij', run: () => void this.restore(deal) }
		});
		await this.#reload();
	}

	/** Brings the listing back; a failed request offers to try again. */
	async restore(deal: Deal): Promise<void> {
		try {
			await this.#host.send('DELETE', deal.id);
			this.hidden.delete(deal.id);
		} catch (err) {
			this.#raise({
				title: 'Nie udało się przywrócić oferty',
				description: errorMessage(err),
				tone: 'danger',
				action: { label: 'Spróbuj ponownie', run: () => void this.restore(deal) }
			});
		}
		await this.#reload();
	}

	/** Takes back this page's toasts and raises no more, for a page that is gone. */
	destroy(): void {
		this.#live = false;
		for (const id of [...this.#toasts]) this.#host.dismiss(id, 'withdrawn');
	}

	#raise(options: ToastOptions): void {
		if (!this.#live) return;
		const id: number = this.#host.toast({
			...options,
			ondismiss: () => {
				this.#toasts.delete(id);
			}
		});
		this.#toasts.add(id);
	}

	async #reload(): Promise<void> {
		if (this.#live) await this.#host.reload();
	}
}
