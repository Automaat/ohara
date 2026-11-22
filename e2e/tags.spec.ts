import type { APIRequestContext, Page } from '@playwright/test';
import { expect, expectNoSourceRequests, test } from './fixtures.js';

/** Personal tags on titles: the title form, the title page's links and Kolekcja's filter (#241). */

async function created(res: Awaited<ReturnType<APIRequestContext['post']>>): Promise<number> {
	expect(res.ok(), await res.text()).toBe(true);
	return ((await res.json()) as { id: number }).id;
}

/** Adds a title with one DVD copy through the API, its cover from the app itself. */
async function seed(request: APIRequestContext, name: string, tags?: string[]): Promise<number> {
	const cover = new URL('/pwa-192x192.png', test.info().project.use.baseURL).href;
	const titleID = await created(
		await request.post('/api/titles', { data: { kind: 'movie', name, cover_url: cover, tags } })
	);
	const editionID = await created(
		await request.post('/api/editions', { data: { title_id: titleID, format: 'dvd' } })
	);
	await created(
		await request.post('/api/copies', { data: { edition_id: editionID, status: 'owned' } })
	);
	return titleID;
}

function tile(page: Page, name: string) {
	return page
		.getByRole('list', { name: 'Egzemplarze w kolekcji' })
		.getByRole('listitem')
		.filter({ has: page.getByRole('link', { name, exact: true }) });
}

test('the title form suggests tags in use, Enter adds one and saving adds the one typed', async ({
	page,
	request
}) => {
	const run = String(Date.now());
	const horror = `Horror ${run}`;
	const kids = `Dla dzieci ${run}`;
	await seed(request, `Obcy ${run}`, [horror]);
	const thingID = await seed(request, `Coś ${run}`);

	await page.goto(`/titles/${thingID}`);
	await page.getByRole('button', { name: 'Więcej działań' }).tap();
	await page.getByRole('menuitem', { name: 'Edytuj tytuł' }).tap();
	const form = page.getByRole('dialog', { name: 'Edycja tytułu' });
	const field = form.getByRole('combobox', { name: 'Tagi' });
	await field.tap();
	await field.pressSequentially(`horror ${run}`);
	await expect(form.getByRole('option', { name: horror })).toBeVisible();
	await field.press('Enter');
	const chips = form.getByRole('group', { name: 'Tagi tytułu' });
	await expect(chips.getByText(horror, { exact: true })).toBeVisible();
	await expect(field).toHaveValue('');
	await expect(form).toBeVisible();
	await field.pressSequentially(kids);
	await field.press('Escape');
	await expect(form).toBeVisible();
	await form.getByRole('button', { name: 'Zapisz zmiany' }).tap();
	await expect(form).toBeHidden();

	const tags = page.getByRole('list', { name: 'Tagi' });
	await expect(tags.getByRole('listitem')).toHaveText([horror, kids]);
	await expectNoSourceRequests(request);
});

test('a tag on the title page and the sheet filter Kolekcja without asking the server', async ({
	page,
	request
}) => {
	const run = String(Date.now());
	const horror = `Horror ${run}`;
	const kids = `Dla dzieci ${run}`;
	const alien = `Obcy ${run}`;
	const thing = `Coś ${run}`;
	const cruise = `Rejs ${run}`;
	await seed(request, alien, [horror]);
	const thingID = await seed(request, thing, [`horror ${run}`, kids]);
	await seed(request, cruise);

	await page.goto(`/titles/${thingID}`);
	const tags = page.getByRole('list', { name: 'Tagi' });
	await expect(tags.getByRole('listitem')).toHaveText([horror, kids]);
	await tags.getByRole('link', { name: `Kolekcja z tagiem ${horror}` }).tap();
	await expect(page).toHaveURL(new RegExp(`/\\?tag=Horror\\+${run}$`));
	await expect(tile(page, alien)).toHaveCount(1);
	await expect(tile(page, thing)).toHaveCount(1);
	await expect(tile(page, cruise)).toHaveCount(0);
	await expect(page.getByRole('button', { name: `Usuń: Tag: ${horror}` })).toBeVisible();

	const sent: string[] = [];
	page.on('request', (req) => {
		if (new URL(req.url()).pathname === '/api/collection') sent.push(req.url());
	});
	await page.getByRole('button', { name: /^Sortowanie i filtry/ }).tap();
	const sheet = page.getByRole('dialog', { name: 'Sortowanie i filtry' });
	const select = sheet.getByRole('combobox', { name: 'Tag' });
	await expect(select).toHaveValue(horror);
	await expect(select.locator('option').first()).toHaveText('Każdy');
	await select.selectOption(kids);
	await sheet.getByRole('button', { name: 'Gotowe' }).tap();
	await expect(page).toHaveURL(new RegExp(`/\\?tag=Dla\\+dzieci\\+${run}$`));
	await expect(tile(page, thing)).toHaveCount(1);
	await expect(tile(page, alien)).toHaveCount(0);

	await page.getByRole('button', { name: /^Sortowanie i filtry/ }).tap();
	await sheet.getByRole('combobox', { name: 'Tag' }).selectOption({ label: 'Każdy' });
	await sheet.getByRole('button', { name: 'Gotowe' }).tap();
	await expect(page).toHaveURL(/\/$/);
	await expect(tile(page, cruise)).toHaveCount(1);
	expect(sent, 'requests to /api/collection while filtering').toEqual([]);

	await expectNoSourceRequests(request);
});
