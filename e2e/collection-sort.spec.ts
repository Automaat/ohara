import type { APIRequestContext } from '@playwright/test';
import { expect, expectNoSourceRequests, test } from './fixtures.js';

type Seed = { name: string; year: number | null; format: 'dvd' | 'bluray'; bought?: string };

async function add(request: APIRequestContext, seed: Seed) {
	const title = await request.post('/api/titles', {
		data: { kind: 'movie', name: seed.name, year: seed.year }
	});
	expect(title.ok()).toBe(true);
	const edition = await request.post('/api/editions', {
		data: { title_id: ((await title.json()) as { id: number }).id, format: seed.format }
	});
	expect(edition.ok()).toBe(true);
	const copy = await request.post('/api/copies', {
		data: {
			edition_id: ((await edition.json()) as { id: number }).id,
			status: 'owned',
			...(seed.bought ? { purchased_on: seed.bought } : {})
		}
	});
	expect(copy.ok()).toBe(true);
}

test('the sheet sorts copies by purchase date and year and keeps the filters', async ({
	page,
	request
}) => {
	const run = `Sort ${Date.now()}`;
	// Entered in this order, so "Ostatnio dodane" would list them the other way round. The Blu-ray
	// would lead every order if the format filter were dropped.
	await add(request, { name: `${run} Alfa`, year: 1999, format: 'dvd', bought: '2024-05-01' });
	await add(request, { name: `${run} Beta`, year: 2015, format: 'dvd', bought: '2023-06-01' });
	await add(request, { name: `${run} Gamma`, year: null, format: 'dvd' });
	await add(request, { name: `${run} Delta`, year: 2005, format: 'dvd', bought: '2023-01-01' });
	await add(request, { name: `${run} Blu`, year: 2030, format: 'bluray', bought: '2025-01-01' });

	await page.goto('/');
	const search = page.getByRole('searchbox', { name: 'Szukaj w kolekcji' });
	await search.fill(run);
	const names = page.getByRole('list', { name: 'Egzemplarze w kolekcji' }).getByRole('link');
	await expect(names).toHaveCount(5);

	const sheetButton = page.getByRole('button', { name: /^Sortowanie i filtry/ });
	const sheet = page.getByRole('dialog', { name: 'Sortowanie i filtry' });
	await sheetButton.click();
	await sheet.getByRole('combobox', { name: 'Nośnik' }).selectOption({ label: 'DVD' });
	await sheet.getByRole('button', { name: 'Gotowe' }).click();
	await expect(names).toHaveCount(4);

	const choices = page.getByRole('group', { name: 'Sortowanie i filtry' });
	async function sortBy(option: string, chip: string, order: string[]) {
		await sheetButton.click();
		await sheet.getByRole('combobox', { name: 'Sortowanie' }).selectOption({ label: option });
		await sheet.getByRole('button', { name: 'Gotowe' }).click();
		await expect(sheet).toBeHidden();

		await expect(names).toHaveText(order.map((suffix) => `${run} ${suffix}`));
		await expect(choices).toContainText(chip);
		await expect(choices).toContainText('DVD');
		await expect(search).toHaveValue(run);
	}

	await sortBy('Ostatnio kupione', 'Sortuj: ostatnio kupione', ['Alfa', 'Beta', 'Delta', 'Gamma']);
	await sortBy('Rok malejąco', 'Sortuj: rok malejąco', ['Beta', 'Delta', 'Alfa', 'Gamma']);
	await sortBy('Rok rosnąco', 'Sortuj: rok rosnąco', ['Alfa', 'Delta', 'Beta', 'Gamma']);

	await expectNoSourceRequests(request);
});
