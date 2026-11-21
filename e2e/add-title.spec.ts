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

test('Dodaj tytuł saves a movie typed in by hand to Kolekcja', async ({ page, request }) => {
	const name = `Smoke ${Date.now()}`;

	await page.goto('/');
	await page.getByRole('link', { name: 'Dodaj tytuł' }).click();
	await expect(page).toHaveURL(/\/titles\/new$/);

	const form = page.getByRole('form', { name: 'Potwierdź pozycję' });
	await form.getByRole('textbox', { name: 'Tytuł' }).fill(name);
	await expect(form.getByRole('radio', { name: 'Film' })).toBeChecked();
	await form.getByRole('radio', { name: 'DVD' }).check();
	await form.getByRole('button', { name: 'Dodaj do kolekcji' }).click();
	await expect(page).toHaveURL(/\/titles\/\d+$/);

	await page
		.getByRole('navigation', { name: 'Główne strony' })
		.getByRole('link', { name: 'Kolekcja' })
		.click();
	await expect(page).toHaveURL(/\/$/);
	const row = page
		.getByRole('list', { name: 'Egzemplarze w kolekcji' })
		.getByRole('listitem')
		.filter({ has: page.getByRole('link', { name }) });
	await expect(row).toContainText('DVD');

	const res = await request.get('/api/collection');
	expect(res.ok()).toBe(true);
	const { items } = (await res.json()) as { items: Item[] };
	const item = items.find((i) => i.name === name);
	expect(item).toMatchObject({ kind: 'movie', format: 'dvd', barcode: null });

	await expectNoSourceRequests(request);
});
