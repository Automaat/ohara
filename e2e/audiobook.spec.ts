import type { APIRequestContext } from '@playwright/test';
import { expect, expectNoSourceRequests, test, type Item } from './fixtures.js';

async function created(res: Awaited<ReturnType<APIRequestContext['post']>>): Promise<number> {
	expect(res.ok(), await res.text()).toBe(true);
	return ((await res.json()) as { id: number }).id;
}

/**
 * Adds an owned print book through the API. The title comes with a cover from the app itself, so
 * creating it asks no catalog for one.
 */
async function seedBook(request: APIRequestContext, name: string): Promise<number> {
	const cover = new URL('/pwa-192x192.png', test.info().project.use.baseURL).href;
	const titleID = await created(
		await request.post('/api/titles', { data: { kind: 'book', name, cover_url: cover } })
	);
	const editionID = await created(
		await request.post('/api/editions', { data: { title_id: titleID, binding: 'paperback' } })
	);
	await created(
		await request.post('/api/copies', { data: { edition_id: editionID, status: 'owned' } })
	);
	return editionID;
}

/** The collection item of the copy of name. */
async function itemNamed(request: APIRequestContext, name: string): Promise<Item | undefined> {
	const res = await request.get('/api/collection');
	expect(res.ok()).toBe(true);
	const { items } = (await res.json()) as { items: Item[] };
	return items.find((i) => i.name === name);
}

test('a book edition marked as an audiobook shows it on the shelf and in the Nośnik filter', async ({
	page,
	request
}) => {
	const run = String(Date.now());
	const audio = `Słuchowisko ${run}`;
	const paper = `Papier ${run}`;
	const audioEdition = await seedBook(request, audio);
	await seedBook(request, paper);

	await page.goto(`/editions/${audioEdition}`);
	await page.getByRole('button', { name: 'Edytuj wydanie' }).tap();
	const medium = page.getByRole('combobox', { name: 'Nośnik' });
	await expect(medium).toHaveValue('');
	await expect(page.getByRole('combobox', { name: 'Oprawa' })).toHaveValue('paperback');
	await medium.selectOption('audiobook');
	await expect(page.getByRole('combobox', { name: 'Oprawa' })).toHaveCount(0);
	await page.getByRole('button', { name: 'Zapisz zmiany' }).tap();
	await expect(page.getByRole('button', { name: 'Edytuj wydanie' })).toBeVisible();
	await expect(page.getByText('Audiobook', { exact: true }).first()).toBeVisible();
	await expect
		.poll(async () => {
			const item = await itemNamed(request, audio);
			return item && { format: item.format, kind: item.kind };
		})
		.toEqual({ format: 'audiobook', kind: 'book' });

	await page.goto('/');
	const shelf = page.getByRole('list', { name: 'Egzemplarze w kolekcji' });
	const row = (name: string) =>
		shelf.getByRole('listitem').filter({ has: page.getByRole('link', { name }) });
	await expect(row(audio)).toContainText('Audiobook');
	await expect(row(paper)).toContainText('Miękka oprawa');

	await page.getByRole('button', { name: /^Sortowanie i filtry/ }).tap();
	const sheet = page.getByRole('dialog', { name: 'Sortowanie i filtry' });
	await sheet.getByLabel('Nośnik').selectOption('audiobook');
	await sheet.getByRole('button', { name: 'Gotowe' }).tap();
	await expect(page).toHaveURL(/\/\?format=audiobook$/);
	await expect(row(audio)).toHaveCount(1);
	await expect(row(paper)).toHaveCount(0);

	await expectNoSourceRequests(request);
});
