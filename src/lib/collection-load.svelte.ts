/**
 * How the collection page loads what it lists and keeps its place: which answer may show, when
 * Back redraws a kept listing, when the scroll position comes back and how the queue notice
 * shifts the scroll. The page hands in what only it can reach, the copies' place on screen and
 * the shelf, through `CollectionScreen`.
 */
import { tick } from 'svelte';
import { errorMessage, request } from './api.js';
import {
	keepListing,
	keepOverview,
	keptListing,
	keptOverview,
	type Listing,
	type Overview
} from './collection-cache.js';
import {
	kindCounts,
	shelved,
	withoutCopies,
	type CollectionFilter,
	type CollectionView,
	type Title
} from './collection.js';
import { coversPlaced } from './cover-transition.js';
import { queueDeletesSettled } from './queue-deletes.js';
import type { QueueEntry } from './scan.js';

/** The page's scroll and the copies' place on it. */
export type CollectionScreen = {
	scrollX(): number;
	scrollY(): number;
	/** The document's height, which a queue notice above the copies grows. */
	scrollHeight(): number;
	scrollTo(x: number, y: number): void;
	/** Where the copies start on the page. */
	resultsTop(): number;
	/** Draws the shelf's tiles down to `y`; nothing while the shelf is not shown. */
	drawTo(y: number): Promise<void> | undefined;
};

export type CollectionLoadDeps = {
	screen: CollectionScreen;
	fetch?: typeof fetch;
	/** Resolves once the scan queue's removals have settled, before the queue is counted. */
	queueSettled?: () => Promise<void>;
	/** Says the covers are placed, so a cover carried back from a title lands on its tile. */
	coversPlaced?: () => void;
	/** Waits for the next task, after the navigation hands the page its snapshot. */
	nextTask?: () => Promise<void>;
};

/** What the filter lists from: the titles for "Bez egzemplarzy", else the whole collection. */
export function sourceURL(f: CollectionFilter): Listing['url'] {
	return f.status === 'none' ? '/api/titles' : '/api/collection';
}

/**
 * The collection's counts; a kind counts the copies the page lists without a status, on the shelf
 * or on order, neither sold nor wished for.
 */
function overviewOf(full: CollectionView): Overview {
	return {
		totals: full.totals,
		kinds: kindCounts(full.items.filter((item) => shelved(item.status)))
	};
}

export class CollectionLoad {
	filter = $state<CollectionFilter>({ q: '', kind: '', format: '', status: '' });
	/** The whole collection, loaded once per visit; the filter narrows it in the browser. */
	collection = $state<CollectionView | null>(null);
	/** Every title without a copy, from the last load that listed them. */
	uncopied = $state<Title[] | null>(null);
	overview = $state<Overview | null>(null);
	queued = $state(0);
	failure = $state<string | null>(null);
	loading = $state(true);

	/** "Bez egzemplarzy": the view lists titles that have no copy instead of copies. */
	readonly listsTitles = $derived(this.filter.status === 'none');

	#seq = 0;
	/** Set once the reader leaves, so an answer that arrives later changes nothing kept. */
	#left = false;
	#restoreScroll: [number, number] | undefined;
	#pendingCollection: Promise<CollectionView> | undefined;
	#pendingQueue: Promise<void> | undefined;
	#pendingUncopied: Promise<void> | undefined;

	readonly #screen: CollectionScreen;
	readonly #fetch: typeof fetch;
	readonly #queueSettled: () => Promise<void>;
	readonly #coversPlaced: () => void;
	readonly #nextTask: () => Promise<void>;

	constructor(filter: CollectionFilter, deps: CollectionLoadDeps) {
		this.filter = filter;
		this.#screen = deps.screen;
		this.#fetch = deps.fetch ?? ((input, init) => fetch(input, init));
		this.#queueSettled = deps.queueSettled ?? (() => queueDeletesSettled());
		this.#coversPlaced = deps.coversPlaced ?? coversPlaced;
		this.#nextTask = deps.nextTask ?? (() => new Promise((resolve) => setTimeout(resolve)));
	}

	/**
	 * The scroll position to keep for the way back, from the copies' top, so it survives what
	 * loads above them.
	 */
	capture(): [number, number] {
		return [this.#screen.scrollX(), this.#screen.scrollY() - this.#screen.resultsTop()];
	}

	/** Takes the scroll position kept for this entry, to restore once the copies are drawn. */
	restoreFrom(position: [number, number]): void {
		this.#restoreScroll = position;
	}

	/** Shows the whole collection and its counts. */
	#showCollection(full: CollectionView) {
		this.collection = full;
		this.overview = overviewOf(full);
		keepOverview(this.overview);
	}

	/**
	 * Asks for the whole collection and shows the answer whatever the filter lists by then, so a
	 * refresh that a switch to "Bez egzemplarzy" and back outlived still lands. An ask while one
	 * is on its way shares its answer.
	 */
	#askCollection(): Promise<CollectionView> {
		this.#pendingCollection ??= request<CollectionView>(this.#fetch, 'GET', '/api/collection')
			.then((full) => {
				if (!this.#left) this.#showCollection(full);
				return full;
			})
			.finally(() => {
				this.#pendingCollection = undefined;
			});
		return this.#pendingCollection;
	}

	/**
	 * Loads the whole collection for the counts while titles are listed, once, or again when
	 * `again` asks for fresh ones.
	 */
	#loadOverview(again = false) {
		if (this.overview && !again) return;
		return this.#askCollection().then(
			() => undefined,
			() => undefined
		);
	}

	/**
	 * Going back draws the collection as the reader left it, the filter applied to what was
	 * loaded. Loads it left pending never keep what they bring, which would replace what a newer
	 * visit kept.
	 */
	leave(): void {
		this.#seq += 1;
		this.#left = true;
		if (this.listsTitles) {
			if (this.uncopied) {
				keepListing({ url: '/api/titles', titles: this.uncopied, pending: this.loading });
			}
		} else if (this.collection) {
			keepListing({ url: '/api/collection', view: this.collection, pending: this.loading });
		}
	}

	/** Counts the scan queue once per visit; a failed count is asked again on the next load. */
	#loadQueue() {
		this.#pendingQueue ??= this.#queueSettled()
			.then(() => request<QueueEntry[]>(this.#fetch, 'GET', '/api/scan-queue'))
			.then((entries) => this.#showQueue(entries.length))
			.catch(() => {
				this.#pendingQueue = undefined;
			});
		return this.#pendingQueue;
	}

	/**
	 * Shows the queue count. A notice that appears above a scrolled view pushes the view down by
	 * its height, so the copies on screen stay where the reader left them.
	 */
	async #showQueue(count: number) {
		const scrolled = this.#screen.scrollY();
		const before = this.#screen.scrollHeight();
		this.queued = count;
		await tick();
		const grown = this.#screen.scrollHeight() - before;
		if (scrolled > 0 && grown !== 0) {
			this.#screen.scrollTo(this.#screen.scrollX(), scrolled + grown);
		}
	}

	/**
	 * Counts the titles without a copy while the collection holds none, for the empty collection's
	 * link to them; a failed count leaves the link without a number.
	 */
	loadUncopied(): void {
		if (this.#pendingUncopied) return;
		this.#pendingUncopied = request<Title[]>(this.#fetch, 'GET', '/api/titles')
			.then((titles) => {
				this.uncopied ??= withoutCopies(titles);
			})
			.catch(() => undefined)
			.finally(() => (this.#pendingUncopied = undefined));
	}

	/** Shows a listing and keeps it for the way back. */
	#show(listing: Listing) {
		if ('titles' in listing) this.uncopied = listing.titles;
		else this.#showCollection(listing.view);
		keepListing(listing);
	}

	/**
	 * Scrolls back to where the reader left the copies, once they are drawn: the shelf draws its
	 * tiles in parts, so it first draws down to that place. The position is kept from the
	 * copies' top, so the header's counts and a queue notice that arrives later cannot move the
	 * restored row.
	 */
	async #restore(mine: number, overviewReady?: Promise<void>) {
		if (!this.#restoreScroll) return;
		await overviewReady;
		await tick();
		if (mine === this.#seq && this.#restoreScroll) {
			await this.#screen.drawTo(this.#screen.resultsTop() + this.#restoreScroll[1]);
		}
		if (mine === this.#seq && this.#restoreScroll) {
			const [x, fromResults] = this.#restoreScroll;
			this.#screen.scrollTo(x, this.#screen.resultsTop() + fromResults);
			this.#restoreScroll = undefined;
		}
	}

	/**
	 * Draws a kept listing at once, restores the scroll position and places the covers, so a
	 * cover carried back from a title lands on its tile while the listing reloads. SvelteKit
	 * hands the page its snapshot only after the navigation's callbacks have run, so the restore
	 * waits a task for it.
	 */
	async #redraw(mine: number, kept: Listing) {
		this.#show(kept);
		this.overview ??= keptOverview();
		this.failure = null;
		this.loading = kept.pending ?? false;
		await this.#nextTask();
		await this.#restore(mine);
		await tick();
		if (mine === this.#seq) this.#coversPlaced();
	}

	/** Loads the whole collection, or the titles the filter lists. */
	load = (): Promise<void> => this.#loadListing(null);

	/**
	 * Reads the filter from the address after a navigation and loads what it lists; going Back
	 * first draws the listing kept for it.
	 */
	navigated(filter: CollectionFilter, back: boolean): Promise<void> {
		this.filter = filter;
		return this.#loadListing(back ? keptListing(sourceURL(filter)) : null);
	}

	/**
	 * Loads the whole collection, or the titles the filter lists, first drawing `kept` when
	 * given. The covers count as placed only after the restored scroll moves them, so a cover
	 * carried back from a title lands on its tile.
	 */
	async #loadListing(kept: Listing | null) {
		const mine = ++this.#seq;
		const titles = this.filter.status === 'none';
		if (kept) void this.#redraw(mine, kept);
		else this.loading = true;
		const overviewReady = titles ? this.#loadOverview(kept !== null) : undefined;
		void this.#loadQueue();
		try {
			if (titles) {
				const got = await request<Title[]>(this.#fetch, 'GET', '/api/titles');
				if (mine !== this.#seq) return;
				this.#show({ url: '/api/titles', titles: withoutCopies(got) });
			} else {
				const got = await this.#askCollection();
				if (mine !== this.#seq) return;
				keepListing({ url: '/api/collection', view: got });
			}
			this.failure = null;
		} catch (err) {
			if (mine === this.#seq) this.failure = errorMessage(err);
		} finally {
			if (mine === this.#seq) this.loading = false;
		}
		if (kept) return;
		if (mine === this.#seq && !this.failure) await this.#restore(mine, overviewReady);
		await tick();
		if (mine === this.#seq) this.#coversPlaced();
	}

	/**
	 * Applies a filter. `write` puts it in the address and says whether the address changed; an
	 * unchanged address loads nothing. Otherwise the kept scroll position no longer applies, and
	 * the titles or the collection load when the new filter needs them. A switch back to a
	 * collection already here drops the titles' load and shows it.
	 */
	select(next: CollectionFilter, write: (next: CollectionFilter) => boolean): void {
		const from = sourceURL(this.filter);
		this.filter = next;
		if (!write(next)) return;
		this.#restoreScroll = undefined;
		const to = sourceURL(next);
		if (this.#needsLoad(from, to)) {
			void this.load();
		} else if (from !== to) {
			this.#seq += 1;
			this.loading = false;
			this.failure = null;
		}
	}

	/**
	 * Whether a new filter needs a request: the titles each time "Bez egzemplarzy" is chosen or
	 * after their load failed, the collection only until one has loaded. A failed refresh keeps
	 * the collection on screen, and "Spróbuj ponownie" asks again.
	 */
	#needsLoad(from: Listing['url'], to: Listing['url']): boolean {
		if (to === '/api/titles') return from !== to || this.failure !== null;
		return this.collection === null;
	}
}
