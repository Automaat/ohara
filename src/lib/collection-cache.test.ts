import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { CollectionItem, CollectionView, Title } from './collection';

type Cache = typeof import('./collection-cache');
let cache: Cache;

beforeEach(async () => {
	vi.resetModules();
	cache = await import('./collection-cache');
});

function item(over: Partial<CollectionItem> = {}): CollectionItem {
	return {
		copy_id: 100,
		created_at_us: 1,
		status: 'owned',
		price_cents: 4999,
		max_price_cents: null,
		purchased_on: null,
		shop: '',
		acquired_as: null,
		condition: null,
		edition_id: 3,
		barcode: null,
		label: '',
		format: 'bluray',
		binding: null,
		title_id: 7,
		kind: 'movie',
		name: 'Interstellar',
		original_title: null,
		polish_title: null,
		year: 2014,
		cover_url: 'https://image.test/bluray.jpg',
		authors: ['Christopher Nolan'],
		...over
	};
}

function view(items: CollectionItem[]): CollectionView {
	return {
		items,
		totals: { copies: items.length, owned: items.length, wishlist: 0, priced: 0, spent_cents: 0 }
	};
}

function title(over: Partial<Title> = {}): Title {
	return {
		id: 9,
		kind: 'book',
		name: 'Solaris',
		original_title: null,
		polish_title: null,
		year: 1961,
		tmdb_id: null,
		authors: ['Stanisław Lem'],
		bn_id: null,
		openlibrary_id: null,
		notes: 'Pierwsze wydanie',
		cover_url: 'https://image.test/solaris.jpg',
		edition_count: 1,
		copy_count: 0,
		created_at: '2024-01-01T00:00:00Z',
		updated_at: '2024-01-01T00:00:00Z',
		...over
	};
}

describe('kept listing', () => {
	it('holds nothing before the collection loads', () => {
		expect(cache.keptListing('/api/collection')).toBeNull();
		expect(cache.keptOverview()).toBeNull();
		expect(cache.titlePreview(7, null)).toBeNull();
	});

	it('answers only for the address it was loaded from', () => {
		const listing = { url: '/api/collection?kind=movie', view: view([item()]) };
		cache.keepListing(listing);
		expect(cache.keptListing('/api/collection?kind=movie')).toBe(listing);
		expect(cache.keptListing('/api/collection')).toBeNull();
	});

	it('keeps only the latest listing', () => {
		cache.keepListing({ url: '/api/collection', view: view([item()]) });
		const titles = { url: '/api/titles', titles: [title()] };
		cache.keepListing(titles);
		expect(cache.keptListing('/api/collection')).toBeNull();
		expect(cache.keptListing('/api/titles')).toBe(titles);
	});

	it('keeps the latest counts', () => {
		const overview = {
			totals: { copies: 2, owned: 1, wishlist: 1, priced: 1, spent_cents: 4999 },
			kinds: { movie: 2, book: 0, music: 0 }
		};
		cache.keepOverview(overview);
		expect(cache.keptOverview()).toBe(overview);
	});
});

describe('titlePreview', () => {
	it('shows a title as its tapped tile shows it', () => {
		const dvd = item({ copy_id: 100, format: 'dvd', cover_url: 'https://image.test/dvd.jpg' });
		const bluray = item({ copy_id: 101 });
		cache.keepListing({ url: '/api/collection', view: view([dvd, bluray]) });
		expect(cache.titlePreview(7, '101')).toEqual({
			kind: 'movie',
			name: 'Interstellar',
			original_title: null,
			polish_title: null,
			year: 2014,
			authors: ['Christopher Nolan'],
			cover_url: 'https://image.test/bluray.jpg'
		});
	});

	it("falls back to the title's first tile", () => {
		const dvd = item({ copy_id: 100, cover_url: 'https://image.test/dvd.jpg' });
		const other = item({ copy_id: 200, title_id: 8, name: 'Dune' });
		cache.keepListing({ url: '/api/collection', view: view([other, dvd, item({ copy_id: 101 })]) });
		expect(cache.titlePreview(7, null)?.cover_url).toBe('https://image.test/dvd.jpg');
		expect(cache.titlePreview(7, '999')?.cover_url).toBe('https://image.test/dvd.jpg');
	});

	it('gives a tile without artists no authors', () => {
		const tile = item({ kind: 'music', authors: undefined, cover_url: undefined });
		cache.keepListing({ url: '/api/collection', view: view([tile]) });
		expect(cache.titlePreview(7, null)).toMatchObject({ authors: [], cover_url: undefined });
	});

	it('shows a title listed without copies', () => {
		cache.keepListing({ url: '/api/titles', titles: [title(), title({ id: 10, name: 'Eden' })] });
		expect(cache.titlePreview(9, null)).toEqual({
			kind: 'book',
			name: 'Solaris',
			original_title: null,
			polish_title: null,
			year: 1961,
			authors: ['Stanisław Lem'],
			cover_url: 'https://image.test/solaris.jpg'
		});
	});

	it('knows nothing of a title the listing does not hold', () => {
		cache.keepListing({ url: '/api/collection', view: view([item()]) });
		expect(cache.titlePreview(8, null)).toBeNull();
		cache.keepListing({ url: '/api/titles', titles: [title()] });
		expect(cache.titlePreview(7, null)).toBeNull();
	});
});
