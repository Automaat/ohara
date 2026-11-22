import type { APIRequestContext } from '@playwright/test';
import { expect, expectNoSourceRequests, test } from './fixtures.js';

/** An album copy's Goldmine grades of the record and its sleeve, kept apart from its state (#256). */

async function created(res: Awaited<ReturnType<APIRequestContext['post']>>): Promise<number> {
	expect(res.ok(), await res.text()).toBe(true);
	return ((await res.json()) as { id: number }).id;
}

/** Adds a title of kind with one edition in format and one owned copy; returns the title and copy. */
async function seedCopy(
	request: APIRequestContext,
	kind: 'movie' | 'music',
	name: string,
	format: string
): Promise<{ title: number; copy: number }> {
	const cover = new URL('/pwa-192x192.png', test.info().project.use.baseURL).href;
	const title = await created(
		await request.post('/api/titles', { data: { kind, name, cover_url: cover } })
	);
	const edition = await created(
		await request.post('/api/editions', { data: { title_id: title, format } })
	);
	const copy = await created(
		await request.post('/api/copies', {
			data: { edition_id: edition, status: 'owned', condition: 'good' }
		})
	);
	return { title, copy };
}

test('an album copy takes record and sleeve grades, a film copy does not', async ({
	page,
	request
}) => {
	const t = `T${Date.now()}`;
	const album = await seedCopy(request, 'music', `Płyta ${t}`, 'vinyl');
	const film = await seedCopy(request, 'movie', `Film ${t}`, 'dvd');

	await page.goto(`/titles/${album.title}`);
	await page.getByRole('button', { name: 'Edytuj' }).tap();
	const sheet = page.getByRole('dialog', { name: 'Edycja egzemplarza' });
	await expect(sheet.getByRole('combobox', { name: 'Stan', exact: true })).toHaveValue('good');
	await sheet
		.getByRole('combobox', { name: 'Stan nośnika' })
		.selectOption({ label: 'Very Good Plus (VG+)' });
	await sheet
		.getByRole('combobox', { name: 'Stan okładki' })
		.selectOption({ label: 'Near Mint (NM or M-)' });
	await sheet.getByRole('button', { name: 'Zapisz zmiany' }).tap();
	await expect(sheet).toBeHidden();

	const facts = page.getByRole('definition');
	await expect(facts.filter({ hasText: 'Nośnik VG+ · Okładka NM' })).toBeVisible();
	await expect(facts.filter({ hasText: 'Dobry' })).toBeVisible();
	const stored = await request.get(`/api/copies/${album.copy}`);
	expect(await stored.json()).toMatchObject({
		condition: 'good',
		media_grade: 'very_good_plus',
		sleeve_grade: 'near_mint'
	});

	await page.reload();
	await page.getByRole('button', { name: 'Edytuj' }).tap();
	await expect(sheet.getByRole('combobox', { name: 'Stan nośnika' })).toHaveValue('very_good_plus');
	await expect(sheet.getByRole('combobox', { name: 'Stan okładki' })).toHaveValue('near_mint');

	await page.goto(`/titles/${film.title}`);
	await page.getByRole('button', { name: 'Edytuj' }).tap();
	await expect(sheet.getByRole('combobox', { name: 'Stan', exact: true })).toBeVisible();
	await expect(sheet.getByRole('combobox', { name: 'Stan nośnika' })).toHaveCount(0);
	await expect(sheet.getByRole('combobox', { name: 'Stan okładki' })).toHaveCount(0);
	await expect(page.getByRole('term').filter({ hasText: 'Gradacja' })).toHaveCount(0);

	await expectNoSourceRequests(request);
});
