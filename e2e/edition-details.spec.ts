import type { APIRequestContext, Page } from '@playwright/test';
import { expect, expectNoSourceRequests, test } from './fixtures.js';

/** An edition records its discs, its limited run and a vinyl's color (#259). */

async function created(res: Awaited<ReturnType<APIRequestContext['post']>>): Promise<number> {
	expect(res.ok(), await res.text()).toBe(true);
	return ((await res.json()) as { id: number }).id;
}

/**
 * A title of kind with one edition, returning the edition's ID. The title has a cover of the app's
 * own, so the server looks up none in a catalog.
 */
async function edition(
	request: APIRequestContext,
	kind: string,
	name: string,
	format: string | null
): Promise<number> {
	const cover = new URL('/pwa-192x192.png', test.info().project.use.baseURL).href;
	const titleID = await created(
		await request.post('/api/titles', { data: { kind, name, cover_url: cover } })
	);
	return created(await request.post('/api/editions', { data: { title_id: titleID, format } }));
}

/** Opens the edition page's edit form. */
async function editForm(page: Page, id: number) {
	await page.goto(`/editions/${id}`);
	await page.getByRole('button', { name: 'Edytuj wydanie' }).tap();
	return page.locator('form').filter({ has: page.getByRole('button', { name: 'Zapisz zmiany' }) });
}

/** Whether the page scrolls sideways: wider content than the viewport. */
async function overflows(page: Page): Promise<boolean> {
	return page.evaluate(
		() => document.documentElement.scrollWidth > document.documentElement.clientWidth
	);
}

test('a vinyl edition takes its discs, color and run, and its summary names them', async ({
	page,
	request
}) => {
	const name = `Kind of Blue ${Date.now()}`;
	const id = await edition(request, 'music', name, 'cd');
	await created(await request.post('/api/copies', { data: { edition_id: id, status: 'owned' } }));

	const form = await editForm(page, id);
	await expect(form.getByRole('textbox', { name: 'Liczba płyt' })).toBeVisible();
	await expect(form.getByRole('textbox', { name: 'Nakład' })).toBeVisible();
	await expect(form.getByRole('textbox', { name: 'Kolor winylu' })).toHaveCount(0);
	await form.getByLabel('Nośnik').selectOption('vinyl');
	await form.getByRole('textbox', { name: 'Kolor winylu' }).fill('czerwony');
	await form.getByRole('textbox', { name: 'Liczba płyt' }).fill('2');
	await form.getByRole('textbox', { name: 'Nakład' }).fill('500');
	await form.getByRole('button', { name: 'Zapisz zmiany' }).tap();
	await expect(form).toBeHidden();

	const summary = 'Winyl · 2 płyty · czerwony · nakład 500 egz.';
	await page.reload();
	await expect(page.getByText(summary, { exact: true })).toBeVisible();
	expect(await overflows(page)).toBe(false);
	const stored = (await (await request.get(`/api/editions/${id}`)).json()) as Record<
		string,
		unknown
	>;
	expect(stored).toMatchObject({ disc_count: 2, limited_to: 500, color: 'czerwony' });

	await page.goto(`/?q=${encodeURIComponent(name)}`);
	await expect(page.getByText(summary, { exact: true })).toBeVisible();
	expect(await overflows(page)).toBe(false);
	await expectNoSourceRequests(request);
});

test('a book edition takes a limited run but no discs or color', async ({ page, request }) => {
	const id = await edition(request, 'book', `Lalka ${Date.now()}`, null);

	const form = await editForm(page, id);
	await expect(form.getByRole('textbox', { name: 'Liczba płyt' })).toHaveCount(0);
	await expect(form.getByRole('textbox', { name: 'Kolor winylu' })).toHaveCount(0);
	await form.getByRole('textbox', { name: 'Nakład' }).fill('0');
	await form.getByRole('button', { name: 'Zapisz zmiany' }).tap();
	await expect(form.getByRole('textbox', { name: 'Nakład' })).toHaveAccessibleDescription(
		/Nakład to liczba od 1 do 1 000 000\./
	);
	await form.getByRole('textbox', { name: 'Nakład' }).fill('300');
	await form.getByRole('button', { name: 'Zapisz zmiany' }).tap();
	await expect(form).toBeHidden();

	await page.reload();
	await expect(page.getByText('nakład 300 egz.', { exact: true })).toBeVisible();
	expect(await overflows(page)).toBe(false);
	await expectNoSourceRequests(request);
});
