import type { APIRequestContext } from '@playwright/test';
import { expect, expectNoSourceRequests, test } from './fixtures.js';

/** Series of titles, their volumes and the numbers missing from the shelf (#248). */

async function created(res: Awaited<ReturnType<APIRequestContext['post']>>): Promise<number> {
	expect(res.ok(), await res.text()).toBe(true);
	return ((await res.json()) as { id: number }).id;
}

/** Adds a book with one copy of status, its cover from the app itself, in series at number. */
async function seed(
	request: APIRequestContext,
	name: string,
	series?: { name: string; number?: number },
	status?: 'owned' | 'wishlist'
): Promise<{ id: number; seriesID: number | null }> {
	const cover = new URL('/pwa-192x192.png', test.info().project.use.baseURL).href;
	const res = await request.post('/api/titles', {
		data: {
			kind: 'book',
			name,
			cover_url: cover,
			series_name: series?.name,
			series_number: series?.number
		}
	});
	expect(res.ok(), await res.text()).toBe(true);
	const title = (await res.json()) as { id: number; series_id?: number | null };
	if (status) {
		const editionID = await created(
			await request.post('/api/editions', { data: { title_id: title.id } })
		);
		await created(await request.post('/api/copies', { data: { edition_id: editionID, status } }));
	}
	return { id: title.id, seriesID: title.series_id ?? null };
}

test('the series page lists the volumes and the missing ones, reached from Kolekcja', async ({
	page,
	request
}) => {
	const run = String(Date.now());
	const saga = `Saga ${run}`;
	const first = await seed(request, `Tom pierwszy ${run}`, { name: saga, number: 1 }, 'owned');
	await seed(request, `Tom drugi ${run}`, { name: saga, number: 2 }, 'owned');
	await seed(request, `Tom czwarty ${run}`, { name: saga, number: 4 }, 'owned');
	await seed(request, `Tom piąty ${run}`, { name: saga, number: 5 }, 'wishlist');
	const total = await request.put(`/api/series/${first.seriesID}`, { data: { total_volumes: 6 } });
	expect(total.ok(), await total.text()).toBe(true);

	await page.goto('/');
	await page.getByRole('link', { name: 'Serie', exact: true }).tap();
	await expect(page).toHaveURL(/\/series$/);
	const entry = page.getByRole('list', { name: 'Serie' }).getByRole('link', { name: saga });
	await expect(entry).toContainText('Książka · 4 tytuły · brakuje 3');
	await entry.tap();

	await expect(page).toHaveURL(new RegExp(`/series/${first.seriesID}$`));
	await expect(page.getByRole('heading', { name: saga, level: 1 })).toBeVisible();
	const volumes = page.getByRole('list', { name: 'Tomy', exact: true }).getByRole('listitem');
	await expect(volumes).toHaveCount(4);
	await Promise.all(
		[
			[0, ['Tom 1', `Tom pierwszy ${run}`, 'Na półce']],
			[3, ['Tom 5', `Tom piąty ${run}`, 'Na liście życzeń']]
		].flatMap(([at, texts]) =>
			(texts as string[]).map((text) => expect(volumes.nth(at as number)).toContainText(text))
		)
	);

	const missing = page.getByRole('list', { name: 'Brakujące tomy' }).getByRole('listitem');
	await expect(missing).toHaveCount(3);
	await Promise.all(
		[
			[0, ['Tom 3', 'Brak w kolekcji']],
			[1, ['Tom 5', `Tom piąty ${run}`, 'Na liście życzeń']],
			[2, ['Tom 6', 'Brak w kolekcji']]
		].flatMap(([at, texts]) =>
			(texts as string[]).map((text) => expect(missing.nth(at as number)).toContainText(text))
		)
	);
	await expect(missing.nth(1).getByRole('link', { name: `Tom piąty ${run}` })).toBeVisible();

	const field = page.getByRole('textbox', { name: 'Liczba tomów' });
	await expect(field).toHaveValue('6');
	await field.fill('7');
	await page.getByRole('button', { name: 'Zapisz' }).tap();
	await expect(missing).toHaveCount(4);
	await page.reload();
	await expect(page.getByRole('textbox', { name: 'Liczba tomów' })).toHaveValue('7');

	await expectNoSourceRequests(request);
});

test('the title form joins a series it suggests and the title page links to it', async ({
	page,
	request
}) => {
	const run = String(Date.now());
	const witcher = `Wiedźmin ${run}`;
	await seed(request, `Ostatnie życzenie ${run}`, { name: witcher, number: 1 }, 'owned');
	const sword = await seed(request, `Miecz przeznaczenia ${run}`, undefined, 'owned');

	await page.goto(`/titles/${sword.id}`);
	await page.getByRole('button', { name: 'Więcej działań' }).tap();
	await page.getByRole('menuitem', { name: 'Edytuj tytuł' }).tap();
	const form = page.getByRole('dialog', { name: 'Edycja tytułu' });
	const series = form.getByRole('combobox', { name: 'Seria' });
	await series.tap();
	await series.pressSequentially(`wiedźmin ${run}`);
	await form.getByRole('option', { name: witcher, exact: true }).tap();
	await expect(series).toHaveValue(witcher);
	await form.getByRole('textbox', { name: 'Tom' }).fill('2');
	await form.getByRole('button', { name: 'Zapisz zmiany' }).tap();
	await expect(form).toBeHidden();

	const link = page.getByRole('link', { name: `${witcher} · tom 2` });
	await expect(link).toBeVisible();
	await link.tap();
	await expect(page.getByRole('heading', { name: witcher, level: 1 })).toBeVisible();
	await expect(
		page.getByRole('list', { name: 'Tomy', exact: true }).getByRole('listitem')
	).toHaveCount(2);
	await expect(page.getByText('Wszystkie tomy od 1 do 2 są na półce.')).toBeVisible();

	await expectNoSourceRequests(request);
});
