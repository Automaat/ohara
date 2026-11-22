import { beforeEach, describe, expect, it } from 'vitest';
import { keepListing, keptListing, keptOverview } from './collection-cache';
import { CollectionLoad, sourceURL, type CollectionScreen } from './collection-load.svelte';
import type { CollectionFilter, CollectionItem, CollectionView, Title } from './collection';

function item(over: Partial<CollectionItem> = {}): CollectionItem {
	return {
		copy_id: 1,
		created_at_us: 0,
		status: 'owned',
		price_cents: null,
		max_price_cents: null,
		purchased_on: null,
		shop: '',
		acquired_as: null,
		condition: null,
		edition_id: 1,
		barcode: null,
		label: '',
		format: 'dvd',
		binding: null,
		title_id: 1,
		kind: 'movie',
		name: 'Alien',
		original_title: null,
		polish_title: null,
		year: null,
		finished: false,
		...over
	};
}

function view(...items: CollectionItem[]): CollectionView {
	return {
		items,
		totals: { copies: items.length, owned: items.length, wishlist: 0, priced: 0, spent_cents: 0 }
	};
}

function title(over: Partial<Title> = {}): Title {
	return {
		id: 1,
		kind: 'book',
		name: 'Lalka',
		original_title: null,
		polish_title: null,
		year: null,
		tmdb_id: null,
		authors: [],
		bn_id: null,
		openlibrary_id: null,
		notes: '',
		tags: [],
		edition_count: 0,
		copy_count: 0,
		created_at: '2026-11-22T08:00:00Z',
		updated_at: '2026-11-22T08:00:00Z',
		...over
	};
}

const all: CollectionFilter = { q: '', kind: '', format: '', status: '' };
const none: CollectionFilter = { ...all, status: 'none' };

type Held = { path: string; answer: (status: number, body: unknown) => Promise<void> };

/** A fetch whose every request waits until the test answers it. */
function server() {
	const held: Held[] = [];
	const fetchFn: typeof fetch = (input) =>
		new Promise<Response>((resolve) => {
			held.push({
				path: String(input),
				answer: async (status, body) => {
					resolve(new Response(JSON.stringify(body), { status }));
					await settle();
				}
			});
		});
	/** The oldest request for `path` still waiting. */
	const take = (path: string): Held => {
		const at = held.findIndex((h) => h.path === path);
		if (at < 0) throw new Error(`no request for ${path}; waiting: ${held.map((h) => h.path)}`);
		return held.splice(at, 1)[0];
	};
	return { fetchFn, held, take };
}

/** Lets every answered request and the work it starts run. */
async function settle() {
	for (let i = 0; i < 5; i++) await new Promise((resolve) => setTimeout(resolve));
}

type FakeScreen = CollectionScreen & {
	x: number;
	y: number;
	height: number;
	top: number;
	scrolls: [number, number][];
	draws: number[];
};

function fakeScreen(): FakeScreen {
	const s: FakeScreen = {
		x: 0,
		y: 0,
		height: 2000,
		top: 100,
		scrolls: [],
		draws: [],
		scrollX: () => s.x,
		scrollY: () => s.y,
		scrollHeight: () => s.height,
		scrollTo: (x, y) => {
			s.scrolls.push([x, y]);
			s.x = x;
			s.y = y;
		},
		resultsTop: () => s.top,
		drawTo: async (y) => {
			s.draws.push(y);
		}
	};
	return s;
}

function setup(filter: CollectionFilter = all) {
	const srv = server();
	const screen = fakeScreen();
	let placed = 0;
	const load = new CollectionLoad(filter, {
		screen,
		fetch: srv.fetchFn,
		queueSettled: () => Promise.resolve(),
		coversPlaced: () => (placed += 1)
	});
	return { srv, screen, load, placed: () => placed };
}

/** What the page shows for a request the server failed. */
const failed = 'Serwer zwrócił błąd 500.';

/** An address writer that always reports a change, as a new filter does. */
const changed = () => true;

beforeEach(() => {
	keepListing({ url: '/elsewhere', titles: [] });
});

describe('sourceURL', () => {
	it('lists titles for "Bez egzemplarzy" and the collection otherwise', () => {
		expect(sourceURL(none)).toBe('/api/titles');
		expect(sourceURL({ ...all, status: 'ordered' })).toBe('/api/collection');
	});
});

describe('CollectionLoad answers', () => {
	it('counts a kind by the copies on the shelf or on order, never a wish or a sold copy', async () => {
		const { srv, load } = setup();
		const loading = load.navigated(all, false);
		await settle();
		await srv.take('/api/scan-queue').answer(200, []);
		await srv
			.take('/api/collection')
			.answer(
				200,
				view(
					item({ copy_id: 1 }),
					item({ copy_id: 2, status: 'ordered' }),
					item({ copy_id: 3, status: 'wishlist' }),
					item({ copy_id: 4, status: 'sold' }),
					item({ copy_id: 5, kind: 'book', status: 'wishlist' })
				)
			);
		await loading;
		expect(load.overview?.kinds).toEqual({ movie: 2, book: 0, music: 0 });
	});

	it('loads the collection and the queue count, then places the covers', async () => {
		const { srv, load, placed } = setup();
		const loading = load.navigated(all, false);
		expect(load.loading).toBe(true);
		await settle();
		await srv.take('/api/scan-queue').answer(200, [{ id: 1 }, { id: 2 }]);
		await srv.take('/api/collection').answer(200, view(item()));
		await loading;
		expect(load.collection?.items).toHaveLength(1);
		expect(load.overview?.kinds.movie).toBe(1);
		expect(keptOverview()?.totals.copies).toBe(1);
		expect(load.queued).toBe(2);
		expect(load.loading).toBe(false);
		expect(load.failure).toBeNull();
		expect(placed()).toBe(1);
		expect(keptListing('/api/collection')).toMatchObject({ view: load.collection });
	});

	it('never lets a late answer for an older filter replace a newer one', async () => {
		const { srv, load } = setup(none);
		void load.navigated(none, false);
		await settle();
		const older = srv.take('/api/titles');
		load.select(all, changed);
		await srv.take('/api/collection').answer(200, view(item()));
		load.select(none, changed);
		await settle();
		await srv.take('/api/titles').answer(200, [title({ id: 2, name: 'Nowsza' })]);
		await older.answer(200, [title({ id: 1, name: 'Starsza' })]);
		expect(load.uncopied?.map((t) => t.name)).toEqual(['Nowsza']);
		expect(keptListing('/api/titles')).toMatchObject({ titles: [{ name: 'Nowsza' }] });
		expect(load.loading).toBe(false);
	});

	it('never lets a late failure for an older filter show its error', async () => {
		const { srv, load } = setup(none);
		void load.navigated(none, false);
		await settle();
		const older = srv.take('/api/titles');
		load.select(all, changed);
		await srv.take('/api/collection').answer(200, view(item()));
		await older.answer(500, { error: 'Stary błąd' });
		expect(load.failure).toBeNull();
		expect(load.collection?.items).toHaveLength(1);
	});

	it('shows nothing that answers after the reader left', async () => {
		const { srv, load } = setup();
		void load.navigated(all, false);
		await settle();
		load.leave();
		await srv.take('/api/collection').answer(200, view(item()));
		expect(load.collection).toBeNull();
		expect(keptListing('/api/collection')).toBeNull();
	});
});

describe('CollectionLoad going Back', () => {
	it('draws the kept listing at once, then refreshes it', async () => {
		const { srv, load, placed } = setup();
		const kept = view(item({ copy_id: 1, name: 'Kept' }));
		keepListing({ url: '/api/collection', view: kept });
		void load.navigated(all, true);
		expect(load.collection).toBe(kept);
		expect(load.loading).toBe(false);
		expect(load.failure).toBeNull();
		await settle();
		expect(placed()).toBe(1);
		const fresh = view(item({ copy_id: 1 }), item({ copy_id: 2 }));
		await srv.take('/api/collection').answer(200, fresh);
		expect(load.collection).toEqual(fresh);
		expect(keptListing('/api/collection')).toMatchObject({ view: fresh });
		expect(placed()).toBe(1);
	});

	it('keeps a listing left loading as still loading', async () => {
		const { srv, load } = setup();
		keepListing({ url: '/api/collection', view: view(item()), pending: true });
		void load.navigated(all, true);
		expect(load.loading).toBe(true);
		await srv.take('/api/collection').answer(200, view(item()));
		expect(load.loading).toBe(false);
	});

	it('loads afresh when the kept listing is for another filter', async () => {
		const { load } = setup(none);
		keepListing({ url: '/api/collection', view: view(item()) });
		void load.navigated(none, true);
		expect(load.uncopied).toBeNull();
		expect(load.loading).toBe(true);
	});

	it('keeps the kept listing and shows the error when the refresh fails', async () => {
		const { srv, load } = setup();
		const kept = view(item({ name: 'Kept' }));
		keepListing({ url: '/api/collection', view: kept });
		void load.navigated(all, true);
		await settle();
		await srv.take('/api/collection').answer(500, { error: 'Błąd' });
		expect(load.collection).toBe(kept);
		expect(load.failure).toBe(failed);
		expect(load.loading).toBe(false);
	});

	it('restores the scroll from the copies, after the shelf draws down to it', async () => {
		const { screen, load } = setup();
		keepListing({ url: '/api/collection', view: view(item()) });
		void load.navigated(all, true);
		load.restoreFrom([0, 600]);
		screen.top = 140;
		await settle();
		expect(screen.draws).toEqual([740]);
		expect(screen.scrolls).toEqual([[0, 740]]);
	});

	it('keeps the titles listed when leaving them and draws them on the way back', async () => {
		const first = setup(none);
		void first.load.navigated(none, false);
		await settle();
		await first.srv.take('/api/titles').answer(200, [title()]);
		first.load.leave();
		expect(keptListing('/api/titles')).toMatchObject({ titles: [{ id: 1 }], pending: false });

		const { load } = setup(none);
		void load.navigated(none, true);
		expect(load.uncopied?.map((t) => t.id)).toEqual([1]);
	});

	it('keeps the copies on screen when leaving them, marked while they still load', async () => {
		const { srv, load } = setup();
		keepListing({ url: '/api/collection', view: view(item()) });
		void load.navigated(all, true);
		await settle();
		await srv.take('/api/collection').answer(200, view(item()));
		void load.load();
		load.leave();
		expect(keptListing('/api/collection')).toMatchObject({ pending: true });
	});
});

describe('CollectionLoad scroll', () => {
	it('restores the position only after the counts above the list load', async () => {
		const { srv, screen, load } = setup(none);
		load.restoreFrom([0, 500]);
		void load.navigated(none, false);
		await settle();
		await srv.take('/api/titles').answer(200, [title()]);
		expect(screen.scrolls).toEqual([]);
		screen.top = 180;
		await srv.take('/api/collection').answer(200, view(item()));
		expect(screen.draws).toEqual([680]);
		expect(screen.scrolls).toEqual([[0, 680]]);
	});

	it('restores nothing after a failed load', async () => {
		const { srv, screen, load } = setup();
		load.restoreFrom([0, 500]);
		void load.navigated(all, false);
		await settle();
		await srv.take('/api/collection').answer(500, { error: 'Błąd' });
		expect(load.failure).toBe(failed);
		expect(screen.scrolls).toEqual([]);
	});

	it('drops the kept position when the reader picks another filter', async () => {
		const { srv, screen, load } = setup();
		void load.navigated(all, false);
		await settle();
		load.restoreFrom([0, 500]);
		load.select({ ...all, kind: 'book' }, changed);
		await srv.take('/api/collection').answer(200, view(item()));
		expect(screen.scrolls).toEqual([]);
	});

	it('captures the position from the copies top', () => {
		const { screen, load } = setup();
		screen.x = 3;
		screen.y = 900;
		screen.top = 150;
		expect(load.capture()).toEqual([3, 750]);
	});

	it('pushes a scrolled view down by the height of a queue notice that appears', async () => {
		const { srv, screen, load } = setup();
		screen.y = 400;
		void load.navigated(all, false);
		await settle();
		const queue = srv.take('/api/scan-queue');
		screen.scrollHeight = () => (load.queued > 0 ? 2056 : 2000);
		await queue.answer(200, [{ id: 1 }]);
		expect(load.queued).toBe(1);
		expect(screen.scrolls).toEqual([[0, 456]]);
	});

	it('leaves a view at the top where a queue notice appears', async () => {
		const { srv, screen, load } = setup();
		void load.navigated(all, false);
		await settle();
		screen.scrollHeight = () => (load.queued > 0 ? 2056 : 2000);
		await srv.take('/api/scan-queue').answer(200, [{ id: 1 }]);
		expect(screen.scrolls).toEqual([]);
	});

	it('counts the queue once per visit, and again after a failed count', async () => {
		const { srv, load } = setup();
		void load.navigated(all, false);
		await settle();
		await srv.take('/api/scan-queue').answer(500, { error: 'Błąd' });
		await srv.take('/api/collection').answer(200, view(item()));
		void load.load();
		await settle();
		await srv.take('/api/scan-queue').answer(200, []);
		void load.load();
		await settle();
		expect(srv.held.map((h) => h.path)).toEqual(['/api/collection']);
	});
});

describe('CollectionLoad filters', () => {
	it('loads nothing when the address does not change', () => {
		const { srv, load } = setup();
		load.select({ ...all, kind: 'movie' }, () => false);
		expect(load.filter.kind).toBe('movie');
		expect(srv.held).toHaveLength(0);
	});

	it('filters a loaded collection in the browser without a request', async () => {
		const { srv, load } = setup();
		void load.navigated(all, false);
		await settle();
		await srv.take('/api/collection').answer(200, view(item()));
		srv.held.length = 0;
		load.select({ ...all, kind: 'book' }, changed);
		await settle();
		expect(srv.held).toHaveLength(0);
	});

	it('shows the collection at once when switching back from a loading titles list', async () => {
		const { srv, load } = setup();
		void load.navigated(all, false);
		await settle();
		await srv.take('/api/collection').answer(200, view(item()));
		load.select(none, changed);
		expect(load.loading).toBe(true);
		await settle();
		const titles = srv.take('/api/titles');
		load.select(all, changed);
		expect(load.loading).toBe(false);
		await titles.answer(200, [title()]);
		expect(load.uncopied).toBeNull();
		expect(load.loading).toBe(false);
	});

	it('asks for the titles again after their load failed', async () => {
		const { srv, load } = setup(none);
		void load.navigated(none, false);
		await settle();
		await srv.take('/api/collection').answer(200, view(item()));
		await srv.take('/api/titles').answer(500, { error: 'Błąd' });
		expect(load.failure).toBe(failed);
		load.select({ ...none, kind: 'book' }, changed);
		await settle();
		await srv.take('/api/titles').answer(200, [title()]);
		expect(load.failure).toBeNull();
		expect(load.uncopied).toHaveLength(1);
	});

	it('loads the counts once while titles are listed, and again on the way back', async () => {
		const { srv, load } = setup(none);
		void load.navigated(none, false);
		await settle();
		await srv.take('/api/scan-queue').answer(200, []);
		await srv.take('/api/collection').answer(200, view(item()));
		await srv.take('/api/titles').answer(200, [title()]);
		void load.load();
		await settle();
		expect(srv.held.map((h) => h.path)).toEqual(['/api/titles']);
		srv.held.length = 0;
		void load.navigated(none, true);
		await settle();
		expect(srv.held.map((h) => h.path).sort()).toEqual(['/api/collection', '/api/titles']);
	});

	it('keeps titles with copies out of the titles list', async () => {
		const { srv, load } = setup(none);
		void load.navigated(none, false);
		await settle();
		await srv.take('/api/titles').answer(200, [title({ id: 1 }), title({ id: 2, copy_count: 1 })]);
		expect(load.uncopied?.map((t) => t.id)).toEqual([1]);
	});
});

describe('CollectionLoad uncopied count', () => {
	it('counts the titles without a copy once', async () => {
		const { srv, load } = setup();
		load.loadUncopied();
		load.loadUncopied();
		expect(srv.held.map((h) => h.path)).toEqual(['/api/titles']);
		await srv.take('/api/titles').answer(200, [title(), title({ id: 2, copy_count: 2 })]);
		expect(load.uncopied).toHaveLength(1);
	});

	it('leaves the count unknown when it fails', async () => {
		const { srv, load } = setup();
		load.loadUncopied();
		await srv.take('/api/titles').answer(500, { error: 'Błąd' });
		expect(load.uncopied).toBeNull();
		load.loadUncopied();
		expect(srv.held).toHaveLength(1);
	});
});
