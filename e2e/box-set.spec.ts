import type { APIRequestContext } from '@playwright/test';
import { expect, expectNoSourceRequests, test } from './fixtures.js';

/** A box set's edition lists the titles it holds, and their pages name the box set (#251). */

async function created(res: Awaited<ReturnType<APIRequestContext['post']>>): Promise<number> {
	expect(res.ok(), await res.text()).toBe(true);
	return ((await res.json()) as { id: number }).id;
}

async function movie(request: APIRequestContext, name: string): Promise<number> {
	return created(await request.post('/api/titles', { data: { kind: 'movie', name } }));
}

test('the edition form lists the titles a box set holds and their pages name it', async ({
	page,
	request
}) => {
	const run = String(Date.now());
	const trilogy = `Władca Pierścieni: Trylogia ${run}`;
	const fellowship = `Drużyna Pierścienia ${run}`;
	const towers = `Dwie wieże ${run}`;
	const boxID = await created(
		await request.post('/api/editions', {
			data: { title_id: await movie(request, trilogy), format: 'bluray' }
		})
	);
	await created(
		await request.post('/api/copies', {
			data: { edition_id: boxID, status: 'owned', price_cents: 12900 }
		})
	);
	await movie(request, fellowship);
	const towersID = await movie(request, towers);

	await page.goto(`/editions/${boxID}`);
	await page.getByRole('button', { name: 'Edytuj wydanie' }).tap();
	const form = page
		.locator('form')
		.filter({ has: page.getByRole('button', { name: 'Zapisz zmiany' }) });
	const field = form.getByRole('combobox', { name: 'Zawartość zestawu' });
	const chips = form.getByRole('group', { name: 'Tytuły w zestawie' });
	const add = async (name: string) => {
		await field.tap();
		await field.pressSequentially(name);
		await form.getByRole('option', { name: new RegExp(name) }).tap();
		await expect(chips.getByText(name, { exact: true })).toBeVisible();
		await expect(field).toHaveValue('');
	};
	await add(fellowship);
	await add(towers);
	await form.getByRole('button', { name: 'Zapisz zmiany' }).tap();
	await expect(form).toBeHidden();

	await page.reload();
	const contents = page.getByRole('list', { name: 'Tytuły w zestawie' });
	await expect(contents.getByRole('link')).toHaveText([fellowship, towers]);

	await contents.getByRole('link', { name: towers }).tap();
	await expect(page).toHaveURL(`/titles/${towersID}`);
	const boxSets = page.getByRole('list', { name: 'Zestawy z tym tytułem' });
	await expect(boxSets.getByRole('listitem')).toHaveText(`W zestawie: ${trilogy} · na półce`);
	await boxSets.getByRole('link', { name: trilogy }).tap();
	await expect(page.getByRole('heading', { name: trilogy, level: 1 })).toBeVisible();
	await expectNoSourceRequests(request);
});
