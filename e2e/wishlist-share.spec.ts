import type { APIRequestContext, Browser, Page } from '@playwright/test';
import { expect, expectNoSourceRequests, test } from './fixtures.js';

const sharePath = '/api/wishlist/share';

/** The id a create call answered, failing the test on any other answer. */
async function created(res: Awaited<ReturnType<APIRequestContext['post']>>): Promise<number> {
	expect(res.status(), await res.text()).toBe(201);
	return ((await res.json()) as { id: number }).id;
}

/** Adds a wished copy of a new title and returns the title's name. */
async function wish(
	request: APIRequestContext,
	title: Record<string, unknown>,
	edition: Record<string, unknown>
): Promise<string> {
	const name = `${String(title.name)} ${Date.now()}`;
	const titleID = await created(await request.post('/api/titles', { data: { ...title, name } }));
	const editionID = await created(
		await request.post('/api/editions', { data: { title_id: titleID, ...edition } })
	);
	await created(
		await request.post('/api/copies', {
			data: { edition_id: editionID, status: 'wishlist', max_price_cents: 4321, notes: 'Tajne' }
		})
	);
	return name;
}

/**
 * Deletes every wished copy, so the shared list holds only what this test adds. The suite runs
 * one test at a time and every other test adds the copies it reads.
 */
async function clearWishes(request: APIRequestContext) {
	const res = await request.get('/api/collection?status=wishlist');
	expect(res.ok()).toBe(true);
	const { items } = (await res.json()) as { items: { copy_id: number }[] };
	for (const item of items) {
		expect((await request.delete(`/api/copies/${item.copy_id}`)).status()).toBe(204);
	}
}

async function createLink(request: APIRequestContext): Promise<string> {
	const res = await request.post(sharePath);
	expect(res.status()).toBe(201);
	return ((await res.json()) as { url: string }).url;
}

/**
 * Opens path in a new browser context without cookies, the way someone who got the link opens
 * it, and returns the page with every URL it requested.
 */
async function openAsGuest(
	browser: Browser,
	baseURL: string,
	path: string
): Promise<{ page: Page; requested: string[]; close: () => Promise<void> }> {
	const context = await browser.newContext({
		baseURL,
		viewport: { width: 390, height: 844 },
		deviceScaleFactor: 3,
		isMobile: true,
		hasTouch: true,
		locale: 'pl-PL'
	});
	expect(await context.cookies()).toEqual([]);
	const requested: string[] = [];
	context.on('request', (req) => requested.push(req.url()));
	const page = await context.newPage();
	const res = await page.goto(path);
	expect(res?.status()).toBe(200);
	return { page, requested, close: () => context.close() };
}

test.beforeEach(async ({ request }) => {
	await request.delete(sharePath);
});

test.afterEach(async ({ request }) => {
	await request.delete(sharePath);
});

test('Okazje creates, copies and turns off the wishlist link', async ({
	page,
	context,
	request
}) => {
	await context.grantPermissions(['clipboard-read', 'clipboard-write']);
	await page.goto('/deals');
	await page.getByText('Udostępnij listę życzeń').click();
	await page.getByRole('button', { name: 'Utwórz link' }).click();

	const field = page.getByRole('textbox', { name: 'Link do listy życzeń' });
	await expect(field).toHaveValue(/^http:\/\/localhost:8100\/share\/[A-Za-z0-9_-]{43}$/);
	const url = await field.inputValue();
	expect((await request.get(new URL(url).pathname)).status()).toBe(200);

	await page.getByRole('button', { name: 'Kopiuj' }).click();
	await expect(page.getByRole('button', { name: 'Skopiowano' })).toBeVisible();
	expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(url);

	await page.getByRole('button', { name: 'Wyłącz link' }).click();
	await expect(page.getByRole('button', { name: 'Utwórz link' })).toBeVisible();
	await expect(field).toBeHidden();
	expect((await request.get(new URL(url).pathname)).status()).toBe(404);
	await expectNoSourceRequests(request);
});

test('a failed read of the link offers a retry, never a new link', async ({ page, request }) => {
	const link = await createLink(request);
	let fail = true;
	await page.route('**/api/wishlist/share', async (route) => {
		if (fail && route.request().method() === 'GET') {
			await route.fulfill({ status: 503, json: { error: 'unavailable' } });
		} else {
			await route.fallback();
		}
	});
	await page.goto('/deals');
	await page.getByText('Udostępnij listę życzeń').click();
	await expect(page.getByText('Nie udało się wczytać linku')).toBeVisible();
	await expect(page.getByRole('button', { name: 'Utwórz link' })).toHaveCount(0);

	fail = false;
	await page.getByRole('button', { name: 'Spróbuj ponownie' }).click();
	await expect(page.getByRole('textbox', { name: 'Link do listy życzeń' })).toHaveValue(
		new URL(link, page.url()).href
	);
	expect((await request.get(link)).status()).toBe(200);
});

test('the shared list shows the wishes without the app or its data', async ({
	browser,
	baseURL,
	request
}) => {
	await clearWishes(request);
	const cover = new URL('/pwa-192x192.png', baseURL).href;
	const book = await wish(
		request,
		{ kind: 'book', name: 'Solaris', authors: ['Stanisław Lem'], year: 1961, cover_url: cover },
		{ binding: 'hardcover' }
	);
	const film = await wish(request, { kind: 'movie', name: 'Alien', year: 1979 }, { format: 'uhd' });
	const link = await createLink(request);

	const guest = await openAsGuest(browser, baseURL ?? '', link);
	const { page } = guest;
	await expect(page).toHaveTitle('Lista życzeń');
	await expect(page.getByRole('heading', { level: 1, name: 'Lista życzeń' })).toBeVisible();
	const wishes = page.getByRole('list', { name: 'Życzenia' }).getByRole('listitem');
	await expect(wishes).toHaveCount(2);
	await expect(wishes.filter({ hasText: book })).toContainText('Stanisław Lem');
	await expect(wishes.filter({ hasText: book })).toContainText('Książka · 1961 · Twarda oprawa');
	await expect(wishes.filter({ hasText: film })).toContainText('Film · 1979 · 4K UHD');
	for (const hidden of ['43,21', 'Tajne', 'Gdzie szukać']) {
		await expect(page.getByText(hidden)).toHaveCount(0);
	}
	await expect(page.getByRole('navigation')).toHaveCount(0);
	for (const name of ['Skanuj', 'Ustawienia', 'Kolekcja', 'Okazje']) {
		await expect(page.getByRole('link', { name })).toHaveCount(0);
		await expect(page.getByRole('button', { name })).toHaveCount(0);
	}
	expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);

	await expect(wishes.filter({ hasText: book }).locator('img')).toHaveCount(0);

	const origin = new URL(baseURL ?? '').origin;
	expect(guest.requested.length).toBeGreaterThan(0);
	expect(guest.requested).not.toContain(cover);
	for (const url of guest.requested) {
		const u = new URL(url);
		expect(`${u.origin}${u.pathname}`, 'a request outside the share paths').toMatch(
			new RegExp(`^${origin}/share/`)
		);
	}
	await guest.close();
	await expectNoSourceRequests(request);
});

test('the shared list says when it is empty and a turned-off link is gone', async ({
	browser,
	baseURL,
	request
}) => {
	await clearWishes(request);
	const link = await createLink(request);

	const guest = await openAsGuest(browser, baseURL ?? '', link);
	await expect(guest.page.getByText('Lista życzeń jest pusta.')).toBeVisible();
	await expect(guest.page.getByRole('list')).toHaveCount(0);
	for (const url of guest.requested) expect(new URL(url).pathname).toMatch(/^\/share\//);

	expect((await request.delete(sharePath)).status()).toBe(204);
	const gone = await guest.page.goto(link);
	expect(gone?.status()).toBe(404);
	const unknown = await guest.page.request.get('/api/no-such-path');
	expect(await gone?.text()).toBe(await unknown.text());
	await guest.close();
});
