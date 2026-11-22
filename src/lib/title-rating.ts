/** What the title page shows for a rating: the value and why the last pick was not stored. */
export type RatingView = { rating: number | null; failed: unknown };

/**
 * Stores the ratings a reader picks for one title, one request after another, and says what the
 * page should show: the latest pick while it is on its way, the stored rating once it is stored,
 * and, when the latest pick fails, the last rating the server confirmed with the failure. An
 * earlier pick's answer never changes what a later pick shows.
 */
export class RatingSaver {
	#picks = 0;
	#pending = 0;
	#confirmed: number | null;
	#shown: number | null;
	#queue: Promise<void> = Promise.resolve();

	constructor(
		readonly titleID: number,
		stored: number | null,
		private readonly send: (titleID: number, rating: number | null) => Promise<number | null>,
		private readonly show: (view: RatingView) => void
	) {
		this.#confirmed = stored;
		this.#shown = stored;
	}

	/**
	 * Marks the start of a read of the title, for `loaded`. A read that starts while a pick is on
	 * its way may read the rating from before it, so its mark never matches.
	 */
	mark(): number {
		return this.#pending === 0 ? this.#picks : -1;
	}

	/**
	 * The rating to show from a read of the title that started at `mark`: the rating it read when
	 * nothing was picked since, which becomes the confirmed one, else the one already shown.
	 */
	loaded(rating: number | null, mark: number): number | null {
		if (mark === this.#picks && this.#pending === 0) {
			this.#confirmed = rating;
			this.#shown = rating;
		}
		return this.#shown;
	}

	/** Shows `rating` at once and stores it after the picks before it; resolves once stored or failed. */
	pick(rating: number | null): Promise<void> {
		const pick = ++this.#picks;
		this.#pending++;
		this.#shown = rating;
		this.show({ rating, failed: null });
		this.#queue = this.#queue.then(async () => {
			try {
				const stored = await this.send(this.titleID, rating);
				this.#confirmed = stored;
				if (pick === this.#picks) this.#settle(stored, null);
			} catch (err) {
				if (pick === this.#picks) this.#settle(this.#confirmed, err);
			} finally {
				this.#pending--;
			}
		});
		return this.#queue;
	}

	#settle(rating: number | null, failed: unknown) {
		this.#shown = rating;
		this.show({ rating, failed });
	}
}
