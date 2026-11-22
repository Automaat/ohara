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
