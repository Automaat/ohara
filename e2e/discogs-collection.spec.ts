import type { APIRequestContext, Page } from '@playwright/test';
import { expect, test } from './fixtures.js';

/**
 * An owned album copy pushed to the Discogs collection (#257), against app-discogs and its Discogs
 * double (e2e/compose.yaml), which already holds release 2000002.
 */
test.use({ baseURL: 'http://localhost:8101' });

async function created(res: Awaited<ReturnType<APIRequestContext['post']>>): Promise<number> {
	expect(res.ok(), await res.text()).toBe(true);
	return ((await res.json()) as { id: number }).id;
}

/** Adds a title of kind with one edition and one owned copy; returns the copy. */
async function seedCopy(
	request: APIRequestContext,
	kind: 'movie' | 'music',
	edition: Record<string, unknown>,
	copy: Record<string, unknown> = {}
): Promise<number> {
	const title = await created(
		await request.post('/api/titles', { data: { kind, name: `${kind} ${Date.now()}` } })
	);
	const e = await created(
		await request.post('/api/editions', { data: { title_id: title, ...edition } })
	);
	return created(
		await request.post('/api/copies', { data: { edition_id: e, status: 'owned', ...copy } })
	);
}

test('an owned album copy goes to the Discogs collection with its grades', async ({
	page,
	request
}) => {
	const release = 3_000_000 + (Date.now() % 1_000_000);
	const copy = await seedCopy(
		request,
		'music',
		{ format: 'vinyl', discogs_release_id: release },
		{ media_grade: 'near_mint', sleeve_grade: 'very_good_plus' }
	);

	await page.goto(`/copies/${copy}`);
	const discogs = page.getByRole('region', { name: 'Discogs' });
	await expect(discogs.getByRole('link', { name: 'W kolekcji Discogs' })).toHaveCount(0);
	await discogs.getByRole('button', { name: 'Dodaj do kolekcji Discogs' }).tap();
	await expect(discogs.getByRole('link', { name: 'W kolekcji Discogs' })).toHaveAttribute(
		'href',
		`https://www.discogs.com/release/${release}`
	);
	await expect(discogs.getByRole('button', { name: 'Zaktualizuj stan w Discogs' })).toBeVisible();

	const first = (await (await request.get(`/api/copies/${copy}/discogs`)).json()) as {
		instance_id: number;
	};
	await discogs.getByRole('button', { name: 'Zaktualizuj stan w Discogs' }).tap();
	await expect(discogs.getByRole('button', { name: 'Zaktualizuj stan w Discogs' })).toBeEnabled();
	const again = await request.get(`/api/copies/${copy}/discogs`);
	expect(await again.json()).toMatchObject({ instance_id: first.instance_id });

	await page.reload();
	await expect(discogs.getByRole('link', { name: 'W kolekcji Discogs' })).toBeVisible();
	expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
});

test('a release already in the Discogs collection is not added again', async ({
	page,
	request
}) => {
	const copy = await seedCopy(request, 'music', { format: 'cd', discogs_release_id: 2000002 });
	await page.goto(`/copies/${copy}`);
	const discogs = page.getByRole('region', { name: 'Discogs' });
	await discogs.getByRole('button', { name: 'Dodaj do kolekcji Discogs' }).tap();
	await expect(discogs.getByText('To wydanie jest już w Twojej kolekcji Discogs.')).toBeVisible();
	await expect(discogs.getByRole('link', { name: 'W kolekcji Discogs' })).toHaveCount(0);
});

/** Opens copy's page and checks that it has no Discogs part. */
async function expectNoDiscogs(page: Page, copy: number) {
	await page.goto(`/copies/${copy}`);
	await expect(page.getByRole('button', { name: 'Zapisz zmiany' })).toBeVisible();
	await expect(page.getByRole('region', { name: 'Discogs' })).toHaveCount(0);
}

test('a film copy and an album without a release offer no Discogs push', async ({
	page,
	request
}) => {
	await expectNoDiscogs(page, await seedCopy(request, 'movie', { format: 'dvd' }));
	await expectNoDiscogs(page, await seedCopy(request, 'music', { format: 'cd' }));
});
