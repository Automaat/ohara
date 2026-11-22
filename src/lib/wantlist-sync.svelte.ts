import { ApiError, errorMessage } from './api.js';
import type { WantlistStatus } from './deals.js';

/** What the Discogs wantlist sync needs from the page: the two API calls. */
export type WantlistHost = {
	/** GET /api/discogs/wantlist. */
	status(): Promise<WantlistStatus>;
	/** POST /api/discogs/wantlist/sync. */
	start(): Promise<unknown>;
};

/** How often the status is asked again while a sync runs. */
export const wantlistPollMs = 2000;

/**
 * "Synchronizuj z Discogs" on Okazje. The status is asked again every 2 s while a sync runs. A
 * failed status request keeps the last status and says why in `failure`, so the page can offer a
 * retry even before the first status arrives. `destroy` stops the polling: an answer that
 * arrives after the page is left asks nothing more.
 */
export class WantlistSync {
	status = $state<WantlistStatus | null>(null);
	/** Why the last status request failed, null after one that answered. */
	failure = $state<string | null>(null);
	/** Why the last start failed. */
	error = $state<string | null>(null);
	starting = $state(false);
	loading = $state(false);
	readonly #host: WantlistHost;
	#timer: ReturnType<typeof setTimeout> | undefined;
	#live = true;

	constructor(host: WantlistHost) {
		this.#host = host;
	}

	/** A sync is starting or running. */
	get syncing(): boolean {
		return this.starting || !!this.status?.running;
	}

	/** Asks for the status, and again in 2 s while a sync runs. */
	async load(): Promise<void> {
		this.loading = true;
		try {
			const status = await this.#host.status();
			if (!this.#live) return;
			this.status = status;
			this.failure = null;
		} catch (err) {
			if (!this.#live) return;
			this.failure = errorMessage(err);
		} finally {
			this.loading = false;
		}
		clearTimeout(this.#timer);
		if (this.status?.running) this.#timer = setTimeout(() => void this.load(), wantlistPollMs);
	}

	/** Starts a sync, then asks for the status. */
	async sync(): Promise<void> {
		this.starting = true;
		this.error = null;
		try {
			await this.#host.start();
		} catch (err) {
			if (!this.#live) return;
			this.error =
				err instanceof ApiError && err.status === 409
					? 'Synchronizacja już trwa.'
					: errorMessage(err);
		}
		await this.load();
		this.starting = false;
	}

	/** Stops the polling for good. */
	destroy(): void {
		this.#live = false;
		clearTimeout(this.#timer);
	}
}
