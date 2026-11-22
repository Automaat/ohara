import { request } from './api';
import type { Format } from './collection';
import { marketFor, marketPath, type MarketPrice, type MarketView } from './deals';

/**
 * The used market prices of one title's formats, for the price limit of a wish form. A page
 * asks once per title it shows a wish form for. A failed request leaves no prices, as for a
 * title not priced yet, and the next ask tries again; an answer for a title the page has since
 * left is dropped.
 */
export class TitleMarkets {
	#markets = $state<readonly MarketPrice[]>([]);
	#title: number | null = null;
	#fetch: typeof fetch;

	constructor(fetchFn: typeof fetch) {
		this.#fetch = fetchFn;
	}

	/** Asks for titleId's prices unless this title's were asked for last and did not fail. */
	async load(titleId: number): Promise<void> {
		if (titleId === this.#title) return;
		this.#title = titleId;
		this.#markets = [];
		try {
			const view = await request<MarketView>(this.#fetch, 'GET', marketPath(titleId));
			if (titleId === this.#title) this.#markets = view.markets;
		} catch {
			if (titleId === this.#title) this.#title = null;
		}
	}

	/** The market price of titleId in format, null before its prices load or without any. */
	for(titleId: number, format: Format | null): MarketPrice | null {
		return titleId === this.#title ? marketFor(this.#markets, titleId, format) : null;
	}
}
