import { untrack } from 'svelte';
import { request } from './api';
import type { Format } from './collection';
import { marketFor, marketPath, type MarketPrice, type MarketView } from './deals';

/**
 * The used market prices of one title's formats, for the price limit of a wish form. A page
 * asks once per title it shows a wish form for. A failed request leaves no prices, as for a
 * title not priced yet, and the next ask tries again. Only the newest request's answer lands, so
 * one for a title the page has since left, or an earlier one for the same title, is dropped.
 */
export class TitleMarkets {
	#markets = $state<readonly MarketPrice[]>([]);
	#title = $state<number | null>(null);
	#issued = 0;
	#fetch: typeof fetch;

	constructor(fetchFn: typeof fetch) {
		this.#fetch = fetchFn;
	}

	/**
	 * Asks for titleId's prices unless this title's were asked for last and did not fail. It reads
	 * the asked title untracked, so an effect that calls it never reruns on its own answer, and a
	 * failure waits for the next ask instead of retrying at once.
	 */
	async load(titleId: number): Promise<void> {
		if (titleId === untrack(() => this.#title)) return;
		const ticket = ++this.#issued;
		this.#title = titleId;
		this.#markets = [];
		try {
			const view = await request<MarketView>(this.#fetch, 'GET', marketPath(titleId));
			if (ticket === this.#issued) this.#markets = view.markets;
		} catch {
			if (ticket === this.#issued) this.#title = null;
		}
	}

	/** The market price of titleId in format, null before its prices load or without any. */
	for(titleId: number, format: Format | null): MarketPrice | null {
		const markets = this.#markets;
		return titleId === this.#title ? marketFor(markets, titleId, format) : null;
	}
}
