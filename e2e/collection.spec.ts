import type { APIRequestContext, Locator, Page } from '@playwright/test';
import { expect, expectNoSourceRequests, test, type Item } from './fixtures.js';

/** Kolekcja loads the whole collection once and filters it in the browser (#203). */

type Seeded = { titleID: number; copyID: number };

async function created(res: Awaited<ReturnType<APIRequestContext['post']>>): Promise<number> {
	expect(res.ok(), await res.text()).toBe(true);
	return ((await res.json()) as { id: number }).id;
}

/**
 * Adds a title, one edition and one copy through the API. The title comes with a cover from the
 * app itself, so creating it asks no catalog for one and the page loads nothing from elsewhere.
 */
async function seed(
	request: APIRequestContext,
	title: { kind: string; name: string; authors?: string[] },
	edition: { format?: string; barcode?: string } = {},
	copy: { status?: string; shop?: string } = {}
): Promise<Seeded> {
	const cover = new URL('/pwa-192x192.png', test.info().project.use.baseURL).href;
	const titleID = await created(
		await request.post('/api/titles', { data: { cover_url: cover, ...title } })
	);
	const editionID = await created(
		await request.post('/api/editions', { data: { title_id: titleID, ...edition } })
	);
	const copyID = await created(
		await request.post('/api/copies', {
			data: { edition_id: editionID, status: 'owned', ...copy }
		})
	);
	return { titleID, copyID };
}

/** A tag unique to this test, so its rows stand apart from what other tests added. */
function tag(): string {
	return `T${Date.now()}`;
}

/** An ISBN unique to this run, as the ISBN-13 the server stores and its ISBN-10. */
function uniqueISBN(): { isbn13: string; isbn10: string } {
	const core = String(Date.now()).slice(-9);
	let sum10 = 0;
	for (let i = 0; i < 9; i++) sum10 += Number(core[i]) * (10 - i);
	const check10 = (11 - (sum10 % 11)) % 11;
	const body = `978${core}`;
	let sum13 = 0;
	for (let i = 0; i < 12; i++) sum13 += Number(body[i]) * (i % 2 === 0 ? 1 : 3);
	return {
		isbn13: body + String((10 - (sum13 % 10)) % 10),
		isbn10: core + (check10 === 10 ? 'X' : String(check10))
	};
}

/** Records every `GET /api/collection` the page sends, filtered or not. */
function collectionRequests(page: Page): string[] {
	const sent: string[] = [];
	page.on('request', (req) => {
		const url = new URL(req.url());
		if (req.method() === 'GET' && url.pathname === '/api/collection') sent.push(url.search);
	});
	return sent;
}

/**
 * Notes the `data-cover-key` of each collection tile whose cover a View Transition names as
 * the landing place, read back with `landedKeys`.
 */
async function watchLanding(page: Page): Promise<void> {
	await page.evaluate(() => {
		const landed: string[] = [];
		(window as unknown as { landed: string[] }).landed = landed;
		new MutationObserver((records) => {
			for (const r of records) {
				const el = r.target as HTMLElement;
				const key = el.closest('[data-cover-key]')?.getAttribute('data-cover-key');
				if (el.style.viewTransitionName === 'ohara-cover' && key) landed.push(key);
			}
		}).observe(document.body, { attributes: true, attributeFilter: ['style'], subtree: true });
	});
}

/** The tiles `watchLanding` saw a cover land on. */
function landedKeys(page: Page): Promise<string[]> {
	return page.evaluate(() => (window as unknown as { landed: string[] }).landed);
}

function shelf(page: Page): Locator {
	return page.getByRole('list', { name: 'Egzemplarze w kolekcji' });
}

function tile(page: Page, name: string): Locator {
	return shelf(page)
		.getByRole('listitem')
		.filter({ has: page.getByRole('link', { name, exact: true }) });
}

/**
 * Picks a status in the filter sheet: a phone's narrow sheet shows the segmented control as a
 * select, a wide screen as radios.
 */
async function pickStatus(sheet: Locator, narrow: boolean, value: string, label: string) {
	if (narrow) await sheet.getByRole('combobox', { name: 'Status' }).selectOption(value);
	else await sheet.getByRole('radio', { name: label }).check();
}

function chip(page: Page, name: RegExp): Locator {
	return page.getByRole('group', { name: 'Typ' }).getByRole('button', { name });
}

/** The count a chip shows, as a number. */
async function chipCount(page: Page, name: RegExp): Promise<number> {
	return Number(await chip(page, name).locator('.chip-count').textContent());
}

const viewports = [
	{ name: 'phone', use: {}, touch: true },
	{
		name: 'desktop',
		use: {
			viewport: { width: 1440, height: 900 },
			deviceScaleFactor: 1,
			isMobile: false,
			hasTouch: false
		},
		touch: false
	}
] as const;

for (const viewport of viewports) {
	test.describe(viewport.name, () => {
		test.use(viewport.use);

		/** A tap on a touch screen, a click with a mouse. */
		async function press(target: Locator) {
			if (viewport.touch) await target.tap();
			else await target.click();
		}

		test('chips, the sheet and the search filter without asking the server', async ({
			page,
			request
		}) => {
			const t = tag();
			const film = `Film ${t}`;
			const wish = `Życzenie ${t}`;
			const book = `Książka ${t}`;
			const cd = `Album ${t}`;
			await seed(request, { kind: 'movie', name: film }, { format: 'bluray' }, { shop: 'Empik' });
			await seed(request, { kind: 'movie', name: wish }, { format: 'dvd' }, { status: 'wishlist' });
			await seed(request, { kind: 'book', name: book, authors: [`Autor ${t}`] });
			await seed(request, { kind: 'music', name: cd, authors: ['Miles Davis'] }, { format: 'cd' });

			const sent = collectionRequests(page);
			await page.goto('/');
			await expect(tile(page, film)).toBeVisible();
			await expect.poll(() => sent.length).toBe(1);
			sent.length = 0;

			await press(chip(page, /^Filmy/));
			await expect(page).toHaveURL(/\/\?kind=movie$/);
			await expect(tile(page, film)).toHaveCount(1);
			await expect(tile(page, book)).toHaveCount(0);

			await press(chip(page, /^Książki/));
			await expect(page).toHaveURL(/\/\?kind=book$/);
			await expect(tile(page, book)).toHaveCount(1);
			await expect(tile(page, film)).toHaveCount(0);

			await press(chip(page, /^Wszystko/));
			await expect(page).toHaveURL(/\/$/);
			await expect(tile(page, cd)).toHaveCount(1);

			await press(chip(page, /^Lista życzeń/));
			await expect(page).toHaveURL(/\/\?status=wishlist$/);
			await expect(tile(page, wish)).toHaveCount(1);
			await expect(tile(page, film)).toHaveCount(0);
			await press(chip(page, /^Lista życzeń/));
			await expect(page).toHaveURL(/\/$/);

			await press(page.getByRole('button', { name: /^Sortowanie i filtry/ }));
			const sheet = page.getByRole('dialog', { name: 'Sortowanie i filtry' });
			await sheet.getByLabel('Nośnik').selectOption('cd');
			await press(sheet.getByRole('button', { name: 'Gotowe' }));
			await expect(page).toHaveURL(/\/\?format=cd$/);
			await expect(tile(page, cd)).toHaveCount(1);
			await expect(tile(page, film)).toHaveCount(0);
			await press(page.getByRole('button', { name: 'Usuń: CD' }));
			await expect(page).toHaveURL(/\/$/);

			const search = page.getByRole('searchbox', { name: 'Szukaj w kolekcji' });
			await press(search);
			await search.pressSequentially(t);
			await expect(page).toHaveURL(new RegExp(`\\?q=${t}$`));
			await expect(shelf(page).getByRole('listitem')).toHaveCount(4);
			await search.fill('');
			await search.pressSequentially('empik');
			await expect(tile(page, film)).toHaveCount(1);
			await expect(tile(page, book)).toHaveCount(0);

			await search.fill(`${t} brak`);
			await expect(page.getByText('Nic nie pasuje', { exact: true })).toBeVisible();

			expect(sent, 'requests to /api/collection after the first load').toEqual([]);
			await expectNoSourceRequests(request);
		});

		test('a filtered address loads the collection once and counts all of it', async ({
			page,
			request
		}) => {
			await seed(request, { kind: 'book', name: `Książka ${tag()}` });
			await seed(request, { kind: 'movie', name: `Film ${tag()}` }, { format: 'dvd' });

			const sent = collectionRequests(page);
			await page.goto('/?kind=book');
			await expect(shelf(page)).toBeVisible();
			await expect(chip(page, /^Książki/)).toHaveAttribute('aria-pressed', 'true');
			await page.waitForLoadState('networkidle');
			expect(sent).toEqual(['']);

			const res = await request.get('/api/collection');
			expect(res.ok()).toBe(true);
			const all = (await res.json()) as { items: Item[]; totals: { wishlist: number } };
			const kinds = (kind: string) => all.items.filter((i) => i.kind === kind).length;
			expect(await chipCount(page, /^Wszystko/)).toBe(all.items.length);
			expect(await chipCount(page, /^Filmy/)).toBe(kinds('movie'));
			expect(await chipCount(page, /^Książki/)).toBe(kinds('book'));
			expect(await chipCount(page, /^Muzyka/)).toBe(kinds('music'));
			expect(await chipCount(page, /^Lista życzeń/)).toBe(all.totals.wishlist);
			await expect(shelf(page).getByRole('listitem')).toHaveCount(kinds('book'));
		});

		test('an ISBN-10 finds the book stored under its ISBN-13', async ({ page, request }) => {
			const { isbn13, isbn10 } = uniqueISBN();
			const name = `ISBN ${tag()}`;
			await seed(request, { kind: 'book', name }, { barcode: isbn13 });

			await page.goto('/');
			await expect(tile(page, name)).toBeVisible();
			const search = page.getByRole('searchbox', { name: 'Szukaj w kolekcji' });
			await press(search);
			await search.pressSequentially(isbn10);
			await expect(page).toHaveURL(new RegExp(`\\?q=${isbn10}$`));
			await expect(shelf(page).getByRole('listitem')).toHaveCount(1);
			await expect(tile(page, name)).toBeVisible();

			await search.fill('');
			await expect(page).toHaveURL(/\/$/);
			await search.fill(`\u00a0${isbn10}\u00a0`);
			await expect(page).toHaveURL(new RegExp(`\\?q=${isbn10}$`));
			await expect(shelf(page).getByRole('listitem')).toHaveCount(1);
			await expect(tile(page, name)).toBeVisible();
		});

		test('a failed refresh keeps filtering what is drawn', async ({ page, request }) => {
			const t = tag();
			const book = `Książka ${t}`;
			const film = `Film ${t}`;
			const opened = await seed(request, { kind: 'book', name: book });
			await seed(request, { kind: 'movie', name: film }, { format: 'dvd' });
			await page.goto(`/?q=${t}`);
			await press(tile(page, book).getByRole('link'));
			await expect(page).toHaveURL(new RegExp(`/titles/${opened.titleID}$`));

			await page.route('**/api/collection', (route) =>
				route.fulfill({ status: 500, json: { error: 'awaria' } })
			);
			const sent = collectionRequests(page);
			await page.goBack();
			await expect(page.getByRole('button', { name: 'Spróbuj ponownie' }).first()).toBeVisible();
			await expect(tile(page, film)).toBeVisible();
			expect(sent).toHaveLength(1);
			sent.length = 0;

			await press(chip(page, /^Filmy/));
			await expect(page).toHaveURL(new RegExp(`\\?kind=movie&q=${t}$`));
			await expect(tile(page, film)).toBeVisible();
			await expect(tile(page, book)).toHaveCount(0);
			expect(sent, 'requests to /api/collection after the chip').toEqual([]);
		});

		test('Back from a title restores the filter, the scroll and the cover', async ({
			page,
			request
		}) => {
			const t = tag();
			const seeded = await Promise.all(
				Array.from({ length: 30 }, (_, i) =>
					seed(request, { kind: 'book', name: `Tom ${String(i).padStart(2, '0')} ${t}` })
				)
			);
			const address = `/?kind=book&q=${t}`;
			await page.goto(address);
			await expect(shelf(page).getByRole('listitem')).toHaveCount(30);

			const opened = tile(page, `Tom 20 ${t}`);
			await opened.scrollIntoViewIfNeeded();
			await page.evaluate(() => window.scrollBy(0, 40));
			const scrolled = await page.evaluate(() => window.scrollY);
			expect(scrolled).toBeGreaterThan(0);

			await press(opened.getByRole('link'));
			await expect(page).toHaveURL(new RegExp(`/titles/${seeded[20].titleID}$`));
			await expect(page.getByRole('heading', { name: `Tom 20 ${t}` })).toBeVisible();

			await watchLanding(page);
			const sent = collectionRequests(page);
			await page.goBack();

			await expect(page).toHaveURL(address);
			await expect(chip(page, /^Książki/)).toHaveAttribute('aria-pressed', 'true');
			await expect(page.getByRole('searchbox', { name: 'Szukaj w kolekcji' })).toHaveValue(t);
			await expect(shelf(page).getByRole('listitem')).toHaveCount(30);
			await expect.poll(() => landedKeys(page)).toContain(String(seeded[20].copyID));
			expect(Math.abs((await page.evaluate(() => window.scrollY)) - scrolled)).toBeLessThan(2);
			await page.waitForLoadState('networkidle');
			expect(sent.length, 'one refresh of the collection').toBeLessThanOrEqual(1);
		});

		test('Back to a tile drawn in a later part restores the scroll and the cover', async ({
			page,
			request
		}) => {
			const t = tag();
			const total = 100;
			const seeded = await Promise.all(
				Array.from({ length: total }, (_, i) =>
					seed(request, { kind: 'book', name: `Tom ${String(i).padStart(3, '0')} ${t}` })
				)
			);
			const address = `/?q=${t}`;
			await page.goto(address);
			const tiles = shelf(page).getByRole('listitem');
			await expect(tiles).toHaveCount(total);
			await expect(tiles.first()).toHaveAttribute('aria-setsize', String(total));
			await expect(tiles.last()).toHaveAttribute('aria-posinset', String(total));

			const far = 95;
			const opened = tile(page, `Tom 0${far} ${t}`);
			await opened.scrollIntoViewIfNeeded();
			await page.evaluate(() => window.scrollBy(0, 40));
			const scrolled = await page.evaluate(() => window.scrollY);

			await press(opened.getByRole('link'));
			await expect(page).toHaveURL(new RegExp(`/titles/${seeded[far].titleID}$`));
			await expect(page.getByRole('heading', { name: `Tom 0${far} ${t}` })).toBeVisible();

			await watchLanding(page);
			await page.goBack();

			await expect(page).toHaveURL(address);
			await expect.poll(() => landedKeys(page)).toContain(String(seeded[far].copyID));
			expect(Math.abs((await page.evaluate(() => window.scrollY)) - scrolled)).toBeLessThan(2);
			await expect(tiles).toHaveCount(total);
		});

		test('the table layout mounts only what the screen shows', async ({ page, request }) => {
			const t = tag();
			const name = `Tabela ${t}`;
			await seed(request, { kind: 'book', name });
			await page.addInitScript(() => localStorage.setItem('ohara.collection.layout', 'table'));
			await page.goto(`/?q=${t}`);
			if (viewport.touch) {
				await expect(tile(page, name)).toBeVisible();
				await expect(page.locator('table')).toHaveCount(0);
			} else {
				await expect(page.getByRole('table').getByRole('link', { name })).toBeVisible();
				await expect(page.locator('ul[aria-label="Egzemplarze w kolekcji"]')).toHaveCount(0);
			}
		});

		test('a refresh outlives a switch to Bez egzemplarzy and back', async ({ page, request }) => {
			const t = tag();
			const first = `Pierwszy ${t}`;
			const second = `Drugi ${t}`;
			const opened = await seed(request, { kind: 'book', name: first });
			await page.goto(`/?q=${t}`);
			await press(tile(page, first).getByRole('link'));
			await expect(page).toHaveURL(new RegExp(`/titles/${opened.titleID}$`));
			await seed(request, { kind: 'book', name: second });

			let release: () => void = () => undefined;
			const held = new Promise<void>((resolve) => (release = resolve));
			await page.route('**/api/collection', async (route) => {
				await held;
				await route.continue();
			});
			await page.goBack();
			await expect(tile(page, first)).toBeVisible();
			await expect(tile(page, second)).toHaveCount(0);

			await press(page.getByRole('button', { name: /^Sortowanie i filtry/ }));
			const sheet = page.getByRole('dialog', { name: 'Sortowanie i filtry' });
			await pickStatus(sheet, viewport.touch, 'none', 'Bez egzemplarzy');
			await expect(page).toHaveURL(/status=none/);
			await pickStatus(sheet, viewport.touch, 'all', 'Każdy');
			await expect(page).not.toHaveURL(/status=/);
			await press(sheet.getByRole('button', { name: 'Gotowe' }));
			release();
			await expect(tile(page, second)).toBeVisible();
		});

		test('empty and failed loads keep their states', async ({ page }) => {
			const empty = {
				items: [],
				totals: { copies: 0, owned: 0, wishlist: 0, priced: 0, spent_cents: 0 }
			};
			await page.route('**/api/collection', (route) => route.fulfill({ json: empty }));
			await page.route('**/api/titles', (route) => route.fulfill({ json: [] }));
			await page.goto('/?kind=book');
			await expect(page.getByText('Kolekcja jest pusta', { exact: true })).toBeVisible();
			await page.unrouteAll();

			await page.route('**/api/collection', (route) =>
				route.fulfill({ status: 500, json: { error: 'awaria' } })
			);
			await page.goto('/');
			const retry = page.getByRole('button', { name: 'Spróbuj ponownie' }).first();
			await expect(retry).toBeVisible();
			await page.unrouteAll();
			await press(retry);
			await expect(shelf(page)).toBeVisible();
		});
	});
}

/**
 * Answers `GET /api/collection` with `total` copies made from one seeded through the API, so a
 * test meets a collection of thousands without adding them.
 */
async function routeCollection(page: Page, request: APIRequestContext, total: number) {
	const t = tag();
	await seed(request, { kind: 'book', name: `Wzór ${t}` });
	const res = await request.get(`/api/collection?q=${t}`);
	const [item] = ((await res.json()) as { items: Record<string, unknown>[] }).items;
	const items = Array.from({ length: total }, (_, i) => ({
		...item,
		copy_id: 1_000_000 + i,
		edition_id: 1_000_000 + i,
		title_id: 1_000_000 + i,
		name: `Tom ${String(i).padStart(4, '0')} ${t}`,
		cover_url: null
	}));
	const totals = { copies: total, owned: total, wishlist: 0, priced: 0, spent_cents: 0 };
	await page.route('**/api/collection', (route) => route.fulfill({ json: { items, totals } }));
}

test('the end of a shelf still being drawn is the end of the collection', async ({
	page,
	request
}) => {
	await routeCollection(page, request, 2000);
	const cdp = await page.context().newCDPSession(page);
	await cdp.send('Emulation.setCPUThrottlingRate', { rate: 4 });
	await page.goto('/');
	const tiles = shelf(page).getByRole('listitem');
	await expect(tiles.first()).toBeVisible();
	const drawnAtEnd = await page.evaluate(() => {
		window.scrollTo(0, document.documentElement.scrollHeight);
		return document.querySelectorAll('ul[aria-label="Egzemplarze w kolekcji"] > li').length;
	});
	expect(drawnAtEnd, 'tiles drawn when the reader went to the end').toBeLessThan(2000);
	await expect(tiles).toHaveCount(2000, { timeout: 15_000 });
	await expect(tiles.last()).toBeInViewport();
});

test('turning a phone sideways and back keeps the place on the shelf', async ({
	page,
	request
}) => {
	await routeCollection(page, request, 2000);
	await page.addInitScript(() => localStorage.setItem('ohara.collection.layout', 'table'));
	await page.goto('/');
	const tiles = shelf(page).getByRole('listitem');
	await expect(tiles).toHaveCount(2000);
	await tiles.nth(1500).evaluate((el) => el.scrollIntoView({ block: 'start' }));

	await page.setViewportSize({ width: 844, height: 390 });
	await expect(page.getByRole('table')).toBeVisible();
	await expect(shelf(page)).toHaveCount(0);
	await page.setViewportSize({ width: 390, height: 844 });

	await expect(shelf(page)).toBeVisible();
	await expect(tiles.nth(1500)).toBeInViewport();
});
