import type { APIRequestContext } from '@playwright/test';
import { expect, expectNoSourceRequests, test } from './fixtures.js';

/** A copy's collector marks: an autograph and its number in the edition's run (#260). */

async function created(res: Awaited<ReturnType<APIRequestContext['post']>>): Promise<number> {
	expect(res.ok(), await res.text()).toBe(true);
	return ((await res.json()) as { id: number }).id;
}

/** Adds a book with one edition of run limitedTo (none when null) and one owned copy. */
async function seedCopy(
	request: APIRequestContext,
	name: string,
	limitedTo: number | null
): Promise<{ title: number; copy: number; edition: number }> {
	const cover = new URL('/pwa-192x192.png', test.info().project.use.baseURL).href;
	const title = await created(
		await request.post('/api/titles', { data: { kind: 'book', name, cover_url: cover } })
	);
	const edition = await created(
		await request.post('/api/editions', {
			data: { title_id: title, ...(limitedTo === null ? {} : { limited_to: limitedTo }) }
		})
	);
	const copy = await created(
		await request.post('/api/copies', { data: { edition_id: edition, status: 'owned' } })
	);
	return { title, copy, edition };
}

test('a copy records its autograph and its number in the run', async ({ page, request }) => {
	const t = `T${Date.now()}`;
	const limited = await seedCopy(request, `Nakład ${t}`, 500);
	const open = await seedCopy(request, `Bez nakładu ${t}`, null);

	await page.goto(`/titles/${limited.title}`);
	await page.getByRole('button', { name: 'Edytuj' }).tap();
	const sheet = page.getByRole('dialog', { name: 'Edycja egzemplarza' });
	await sheet.getByText('Z autografem', { exact: true }).tap();
	await expect(sheet.getByRole('checkbox', { name: 'Z autografem' })).toBeChecked();
	const number = sheet.getByRole('textbox', { name: 'Numer egzemplarza' });
	await number.fill('501');
	await sheet.getByRole('button', { name: 'Zapisz zmiany' }).tap();
	await expect(number).toHaveAccessibleDescription(/nie może przekraczać nakładu wydania/);

	await number.fill('123');
	await sheet.getByRole('button', { name: 'Zapisz zmiany' }).tap();
	await expect(sheet).toBeHidden();
	const facts = page.getByRole('definition');
	await expect(facts.filter({ hasText: 'Z autografem · Egzemplarz nr 123/500' })).toBeVisible();
	const stored = await request.get(`/api/copies/${limited.copy}`);
	expect(await stored.json()).toMatchObject({ signed: true, copy_number: 123 });

	await page.reload();
	await page.getByRole('button', { name: 'Edytuj' }).tap();
	await expect(sheet.getByRole('checkbox', { name: 'Z autografem' })).toBeChecked();
	await expect(number).toHaveValue('123');

	const twin = await created(
		await request.post('/api/copies', { data: { edition_id: limited.edition, status: 'owned' } })
	);
	const clash = await request.put(`/api/copies/${twin}`, {
		data: { edition_id: limited.edition, status: 'owned', copy_number: 123 }
	});
	expect(clash.status()).toBe(409);
	expect(await clash.json()).toMatchObject({ field: 'copy_number' });

	await page.goto(`/titles/${open.title}`);
	await page.getByRole('button', { name: 'Edytuj' }).tap();
	await number.fill('123');
	await sheet.getByRole('button', { name: 'Zapisz zmiany' }).tap();
	await expect(sheet).toBeHidden();
	await expect(facts.filter({ hasText: 'Egzemplarz nr 123' })).toHaveText('Egzemplarz nr 123');
	await expect(page.getByRole('term').filter({ hasText: 'Cechy' })).toHaveCount(1);

	await expectNoSourceRequests(request);
});
