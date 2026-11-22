import type { PageEvents } from './local-day.svelte';
import { warsawDay } from './stats';

/** How often a started clock reads the time, in milliseconds. */
export const monthCheckEvery = 60_000;

function monthAt(at: Date): string {
	return warsawDay(at).slice(0, 7);
}

/**
 * The instant Statystyki counts its 12 months up to. While started it moves on once the calendar
 * month in Poland has changed: it reads the clock every minute and whenever the page is shown
 * again, since a phone's timers sleep while the app is in the background. Within a month it keeps
 * its instant, so the charts are not drawn again for nothing.
 */
export class StatsMonth {
	now = $state(new Date());
	#timer: ReturnType<typeof setInterval> | undefined;

	#refresh = () => {
		const at = new Date();
		if (monthAt(at) !== monthAt(this.now)) this.now = at;
	};

	/** Keeps `now` in the current month until the returned function stops it. */
	start(page: PageEvents = { win: window, doc: document }): () => void {
		this.#refresh();
		clearInterval(this.#timer);
		this.#timer = setInterval(this.#refresh, monthCheckEvery);
		page.win.addEventListener('pageshow', this.#refresh);
		page.doc.addEventListener('visibilitychange', this.#refresh);
		return () => {
			clearInterval(this.#timer);
			page.win.removeEventListener('pageshow', this.#refresh);
			page.doc.removeEventListener('visibilitychange', this.#refresh);
		};
	}
}
