import { isoDate } from './collection';

/** Where the page tells it was shown again: a window for `pageshow`, a document for `visibilitychange`. */
export type PageEvents = { win: EventTarget; doc: EventTarget };

/**
 * The reader's local day as YYYY-MM-DD, which tells a late delivery or loan. While started it
 * moves on at local midnight and whenever the page is shown again, since a phone's timers sleep
 * while the app is in the background. A timer that fires a little early finds the same day and
 * waits for midnight again.
 */
export class LocalDay {
	current = $state(isoDate(new Date()));
	#timer: ReturnType<typeof setTimeout> | undefined;

	#refresh = () => {
		this.current = isoDate(new Date());
		clearTimeout(this.#timer);
		const now = new Date();
		const midnight = new Date(now.getFullYear(), now.getMonth(), now.getDate() + 1);
		this.#timer = setTimeout(this.#refresh, midnight.getTime() - now.getTime());
	};

	/** Keeps `current` up to date until the returned function stops it. */
	start(page: PageEvents = { win: window, doc: document }): () => void {
		this.#refresh();
		page.win.addEventListener('pageshow', this.#refresh);
		page.doc.addEventListener('visibilitychange', this.#refresh);
		return () => {
			clearTimeout(this.#timer);
			page.win.removeEventListener('pageshow', this.#refresh);
			page.doc.removeEventListener('visibilitychange', this.#refresh);
		};
	}
}
