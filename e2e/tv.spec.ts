import type { Page } from '@playwright/test';
import { expect, expectNoSourceRequests, test, type Item } from './fixtures.js';

/** Kolekcja offers "Dodaj tytuł" once the shelf holds a copy; an empty one shows its own links. */
test.beforeEach(async ({ request }) => {
	const title = await request.post('/api/titles', {
		data: { kind: 'movie', name: `Na półce ${Date.now()}` }
	});
	expect(title.ok()).toBe(true);
	const edition = await request.post('/api/editions', {
		data: { title_id: ((await title.json()) as { id: number }).id, format: 'bluray' }
	});
	expect(edition.ok()).toBe(true);
	const copy = await request.post('/api/copies', {
		data: { edition_id: ((await edition.json()) as { id: number }).id, status: 'owned' }
	});
	expect(copy.ok()).toBe(true);
});

/**
 * Answers the title search with two seasons of show, as TMDB's would come back: the smoke image
 * has no TMDB key. Returns the kinds searched for.
 */
async function answerSeasons(page: Page, show: string, tmdbID: number): Promise<string[]> {
	const searched: string[] = [];
	await page.route('**/api/search?*', async (route) => {
		const url = new URL(route.request().url());
		searched.push(url.searchParams.get('kind') ?? '');
		const season = (n: number, year: number) => ({
			type: 'tv',
			title: {
				name: show,
				original_title: show,
				polish_title: show,
				year,
				tmdb_id: tmdbID,
				season: n
			},
			edition: { barcode: '' },
			provider: 'tmdb',
			sources: ['tmdb'],
			confidence: 0.6
		});
		await route.fulfill({ json: { candidates: [season(1, 2017), season(2, 2019)] } });
	});
	return searched;
}

test('Dodaj tytuł finds a show, saves its second season and lists it under Seriale', async ({
	page,
	request
}) => {
	const show = `Dark ${Date.now()}`;
	const searched = await answerSeasons(page, show, Date.now() % 1_000_000_000);

	await page.goto('/');
	await page.getByRole('link', { name: 'Dodaj tytuł' }).click();
	await expect(page).toHaveURL(/\/titles\/new$/);

	const form = page.getByRole('form', { name: 'Potwierdź pozycję' });
	await form.getByRole('radio', { name: 'Serial' }).check();
	await page.getByRole('textbox', { name: 'Szukaj po tytule' }).fill('Dark');
	await page.getByRole('button', { name: 'Szukaj', exact: true }).click();
	await page.getByRole('button', { name: new RegExp(`${show} · sezon 2`) }).click();
	expect(searched).toEqual(['tv']);

	await expect(form.getByRole('radio', { name: 'Serial' })).toBeChecked();
	await expect(form.getByRole('textbox', { name: 'Sezon' })).toHaveValue('2');
	await form.getByRole('radio', { name: 'Blu-ray' }).check();
	await form.getByRole('button', { name: 'Dodaj do kolekcji' }).click();
	await expect(page).toHaveURL(/\/titles\/\d+$/);
	await expect(page.getByRole('heading', { name: `${show} · sezon 2` })).toBeVisible();

	await page
		.getByRole('navigation', { name: 'Główne strony' })
		.getByRole('link', { name: 'Kolekcja' })
		.click();
	await expect(page).toHaveURL(/\/$/);
	await page
		.getByRole('group', { name: 'Typ' })
		.getByRole('button', { name: /^Seriale/ })
		.tap();
	await expect(page).toHaveURL(/\?kind=tv$/);
	const shelf = page.getByRole('list', { name: 'Egzemplarze w kolekcji' });
	await expect(shelf.getByRole('link', { name: `${show} · sezon 2` })).toBeVisible();
	await expect(shelf.getByRole('link', { name: /^Na półce/ })).toHaveCount(0);

	const res = await request.get('/api/collection?kind=tv');
	expect(res.ok()).toBe(true);
	const { items } = (await res.json()) as { items: (Item & { season?: number })[] };
	expect(items.filter((i) => i.name === show)).toEqual([
		expect.objectContaining({ kind: 'tv', name: show, season: 2, format: 'bluray' })
	]);

	await expectNoSourceRequests(request);
});
