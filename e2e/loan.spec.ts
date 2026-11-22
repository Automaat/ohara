import type { APIRequestContext, Locator, Page } from '@playwright/test';
import { expect, expectNoSourceRequests, test } from './fixtures.js';

/** Lending a copy from its page, the lent shelf line and the "Wypożyczone" filter (#239). */

async function created(res: Awaited<ReturnType<APIRequestContext['post']>>): Promise<number> {
	expect(res.ok(), await res.text()).toBe(true);
	return ((await res.json()) as { id: number }).id;
}

/** Adds a title with an app-hosted cover, one edition and one owned copy; returns the copy. */
async function seedCopy(request: APIRequestContext, name: string): Promise<number> {
	const cover = new URL('/pwa-192x192.png', test.info().project.use.baseURL).href;
	const titleID = await created(
		await request.post('/api/titles', { data: { kind: 'movie', name, cover_url: cover } })
	);
	const editionID = await created(
		await request.post('/api/editions', { data: { title_id: titleID, format: 'dvd' } })
	);
	return created(
		await request.post('/api/copies', { data: { edition_id: editionID, status: 'owned' } })
	);
}

function tile(page: Page, name: string): Locator {
	return page
		.getByRole('list', { name: 'Egzemplarze w kolekcji' })
		.getByRole('listitem')
		.filter({ has: page.getByRole('link', { name, exact: true }) });
}

test('Wypożycz and Zwrócono on a copy page, with the shelf line and the filter', async ({
	page,
	request
}) => {
	const t = `T${Date.now()}`;
	const lent = `Pożyczony ${t}`;
	const kept = `Na półce ${t}`;
	const lentCopy = await seedCopy(request, lent);
	await seedCopy(request, kept);

	await page.goto(`/copies/${lentCopy}`);
	// A copy edit not yet saved survives lending and the return.
	const notes = page.getByRole('textbox', { name: 'Notatki' });
	await notes.fill('Szkic notatki');
	await page.getByRole('button', { name: 'Wypożycz' }).tap();
	const sheet = page.getByRole('dialog', { name: 'Wypożycz egzemplarz' });
	await sheet.getByRole('textbox', { name: /^Komu/ }).fill('Ania');
	await sheet.getByRole('textbox', { name: 'Data wypożyczenia' }).fill('2026-01-01');
	await sheet.getByRole('textbox', { name: 'Termin zwrotu' }).fill('2025-12-31');
	await sheet.getByRole('button', { name: 'Wypożycz' }).tap();
	await expect(sheet.getByRole('textbox', { name: 'Termin zwrotu' })).toHaveAccessibleDescription(
		/Termin zwrotu nie może być przed wypożyczeniem\./
	);
	await sheet.getByRole('textbox', { name: 'Termin zwrotu' }).fill('2026-01-15');
	await sheet.getByRole('button', { name: 'Wypożycz' }).tap();
	await expect(sheet).toBeHidden();

	const loans = page.getByRole('region', { name: 'Wypożyczenia' });
	await expect(loans.getByRole('listitem')).toHaveText([/^Ania · od .* · po terminie$/]);
	await expect(page.getByRole('button', { name: 'Wypożycz' })).toHaveCount(0);
	await expect(notes).toHaveValue('Szkic notatki');

	await page.goto(`/?q=${t}`);
	await expect(tile(page, lent)).toContainText('Wypożyczony: Ania · po terminie');
	const line = tile(page, lent).getByText(/^Wypożyczony: Ania/);
	expect(await line.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true);
	await expect(tile(page, kept)).not.toContainText('Wypożyczony');

	await page.getByRole('button', { name: /^Sortowanie i filtry/ }).tap();
	const filters = page.getByRole('dialog', { name: 'Sortowanie i filtry' });
	// The switch's native input sits under its track; its label takes the tap.
	await filters.getByText('Wypożyczone', { exact: true }).tap();
	await expect(filters.getByRole('switch', { name: 'Wypożyczone' })).toBeChecked();
	await filters.getByRole('button', { name: 'Gotowe' }).tap();
	await expect(page).toHaveURL(new RegExp(`\\?loaned=1&q=${t}$`));
	await expect(tile(page, lent)).toBeVisible();
	await expect(tile(page, kept)).toHaveCount(0);

	await page.goto(`/copies/${lentCopy}`);
	await notes.fill('Szkic notatki');
	await page.getByRole('button', { name: 'Zwrócono' }).tap();
	await expect(page.getByRole('button', { name: 'Wypożycz' })).toBeVisible();
	await expect(loans.getByRole('listitem')).toHaveText([/^Ania · .+ – .+$/]);
	await expect(notes).toHaveValue('Szkic notatki');

	await page.goto(`/?loaned=1&q=${t}`);
	await expect(page.getByText('Nic nie pasuje', { exact: true })).toBeVisible();
	await page.goto(`/?q=${t}`);
	await expect(tile(page, lent)).not.toContainText('Wypożyczony');
	await expectNoSourceRequests(request);
});
