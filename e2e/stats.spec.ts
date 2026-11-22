import type { APIRequestContext, Page } from '@playwright/test';
import { expect, expectNoSourceRequests, test } from './fixtures.js';

/**
 * Statystyki: spending per month, collection growth and new against used (#246), and the value
 * by kind and by format (#247).
 */

type Owned = {
	status: string;
	price_cents: number | null;
	purchased_on: string | null;
};

const pln = new Intl.NumberFormat('pl-PL', { style: 'currency', currency: 'PLN' });

async function created(res: Awaited<ReturnType<APIRequestContext['post']>>): Promise<number> {
	expect(res.ok(), await res.text()).toBe(true);
	return ((await res.json()) as { id: number }).id;
}

/** Adds a DVD title, its edition and one owned copy with the given purchase fields. */
async function own(
	request: APIRequestContext,
	name: string,
	copy: { price_cents?: number; purchased_on?: string; acquired_as?: 'new' | 'used' }
): Promise<number> {
	const titleID = await created(
		await request.post('/api/titles', { data: { kind: 'movie', name } })
	);
	const editionID = await created(
		await request.post('/api/editions', { data: { title_id: titleID, format: 'dvd' } })
	);
	return created(
		await request.post('/api/copies', {
			data: { edition_id: editionID, status: 'owned', ...copy }
		})
	);
}

/** A day two days back, within this month or the last one whatever the time zones. */
function pastDay(): string {
	return new Date(Date.now() - 2 * 86_400_000).toISOString().slice(0, 10);
}

async function expectNoSideScroll(page: Page) {
	const [scroll, inner] = await page.evaluate(() => [
		document.documentElement.scrollWidth,
		window.innerWidth
	]);
	expect(scroll).toBeLessThanOrEqual(inner);
}

/** A `GET /api/collection` item for a routed answer: a DVD copy with the given fields. */
function item(id: number, over: Record<string, unknown>) {
	return {
		copy_id: id,
		created_at_us: Date.now() * 1000,
		status: 'owned',
		price_cents: null,
		max_price_cents: null,
		purchased_on: null,
		shop: '',
		acquired_as: null,
		condition: null,
		edition_id: id,
		barcode: null,
		label: '',
		format: 'dvd',
		binding: null,
		title_id: id,
		kind: 'movie',
		name: `Film ${id}`,
		original_title: null,
		polish_title: null,
		year: null,
		...over
	};
}

test('Statystyki without owned copies leads to Skanuj', async ({ page }) => {
	const empty = {
		items: [item(1, { status: 'wishlist', name: 'Życzenie' })],
		totals: { copies: 1, owned: 0, wishlist: 1, priced: 0, spent_cents: 0 }
	};
	await page.route('**/api/collection', (route) => route.fulfill({ json: empty }));
	await page.goto('/stats');
	await expect(page.getByText('Brak egzemplarzy na półce', { exact: true })).toBeVisible();
	await expect(page.getByRole('heading', { name: 'Wydatki w miesiącach' })).toHaveCount(0);
	await expectNoSideScroll(page);
	// The tab bar's scan button shares the name, so the link is looked up on the page itself.
	await expect(
		page
			.getByRole('region', { name: 'Wartość kolekcji' })
			.getByRole('link', { name: 'Wartość', exact: true })
	).toBeVisible();
	await page.locator('.content').getByRole('link', { name: 'Skanuj', exact: true }).tap();
	await expect(page).toHaveURL(/\/scan$/);
});

test('Statystyki draws spending, growth and the new and used split', async ({ page, request }) => {
	const tag = `S${Date.now()}`;
	const copies = [
		await own(request, `Nowy ${tag}`, {
			price_cents: 4999,
			purchased_on: pastDay(),
			acquired_as: 'new'
		}),
		await own(request, `Używany ${tag}`, { purchased_on: pastDay(), acquired_as: 'used' }),
		await own(request, `Bez daty ${tag}`, { price_cents: 1234 })
	];
	try {
		const res = await request.get('/api/collection');
		expect(res.ok()).toBe(true);
		const undated = ((await res.json()) as { items: Owned[] }).items.filter(
			(i) => i.status === 'owned' && !i.purchased_on
		);
		const undatedCents = undated.reduce((sum, i) => sum + (i.price_cents ?? 0), 0);

		await page.goto('/');
		await page.getByRole('button', { name: 'Ustawienia' }).tap();
		await page
			.getByRole('navigation', { name: 'Pozostałe strony' })
			.getByRole('link', { name: 'Statystyki' })
			.tap();
		await expect(page).toHaveURL(/\/stats$/);

		await expect(page.locator('.content').getByRole('heading', { level: 2 })).toHaveText([
			'Wydatki w miesiącach',
			'Przyrost kolekcji',
			'Nowe czy używane',
			'Wartość kolekcji'
		]);
		await expect(page.getByText(/^Bez daty: /)).toContainText(pln.format(undatedCents / 100));
		await expect(page.getByRole('img', { name: /^Nowe \d+%, Używane \d+%/ }).first()).toBeVisible();

		const chevrons = await page
			.locator('.content summary svg')
			.evaluateAll((icons) => icons.map((icon) => icon.getBoundingClientRect().height));
		expect(chevrons).toHaveLength(2);
		expect(Math.max(...chevrons), 'a "Pokaż dane" chevron sized like a chart').toBeLessThan(32);

		const spending = page.getByRole('region', { name: 'Wydatki w miesiącach' });
		await spending.locator('summary').tap();
		await expect(spending.getByRole('rowheader', { name: 'Bez daty' })).toBeVisible();
		await expect(spending.getByRole('row')).toHaveCount(14);
		await expectNoSideScroll(page);
		await expectNoSourceRequests(request);
	} finally {
		await Promise.all(copies.map((id) => request.delete(`/api/copies/${id}`)));
	}
});

test('Statystyki tells 0 zł purchases and later dates from missing ones', async ({ page }) => {
	const later = `${new Date().getFullYear() + 1}-06-01`;
	const view = {
		items: [
			item(1, { purchased_on: pastDay(), price_cents: 0, acquired_as: 'used' }),
			item(2, { purchased_on: later, price_cents: 0 })
		],
		totals: { copies: 2, owned: 2, wishlist: 0, priced: 2, spent_cents: 0 }
	};
	await page.route('**/api/collection', (route) => route.fulfill({ json: view }));
	await page.goto('/stats');
	const spending = page.getByRole('region', { name: 'Wydatki w miesiącach' });
	await expect(spending.getByText(/^Najwięcej|^Brak zakupów/)).toHaveCount(0);
	await expect(
		page.getByRole('region', { name: 'Przyrost kolekcji' }).getByText(/^1 egzemplarz z datą/)
	).toBeVisible();
	await expect(page.getByText('Posiadane egzemplarze kosztowały razem 0 zł.')).toBeVisible();
	await expectNoSideScroll(page);
});

type Estimate = { cents: number; source: string; count: number; trimmed: number };

/** A `GET /api/valuation` copy for a routed answer: an owned DVD without values. */
function valued(id: number, over: Record<string, unknown>) {
	return {
		copy_id: id,
		edition_id: id,
		title_id: id,
		title_name: `Film ${id}`,
		kind: 'movie',
		format: 'dvd',
		barcode: null,
		price_cents: null,
		floor: null,
		mid: null,
		ceiling: null,
		low_confidence: true,
		likely_sold: null,
		condition: null,
		authors: [],
		binding: null,
		...over
	};
}

const olx = (cents: number, count = 4): Estimate => ({ cents, source: 'olx', count, trimmed: 0 });

/** The owned copies the value sections read, one of each kind on their shelf. */
const shelf = {
	items: [
		item(1, {}),
		item(2, { kind: 'book', format: null }),
		item(3, { kind: 'music', format: 'vinyl' })
	],
	totals: { copies: 3, owned: 3, wishlist: 0, priced: 0, spent_cents: 0 }
};

/** Answers `GET /api/valuation` with the server's view, given copies and a valuation day. */
async function routeValuation(page: Page, over: Record<string, unknown>) {
	await page.route('**/api/valuation', async (route) => {
		const res = await route.fetch();
		const view = (await res.json()) as Record<string, unknown>;
		await route.fulfill({ response: res, json: { ...view, ...over } });
	});
}

/** Whether an element is wider inside than it is on the page. */
async function expectNoInnerScroll(region: ReturnType<Page['getByRole']>) {
	const [scroll, client] = await region.evaluate((el) => [el.scrollWidth, el.clientWidth]);
	expect(scroll).toBeLessThanOrEqual(client);
}

test('Statystyki says there is no valuation yet and leads to Wartość', async ({
	page,
	request
}) => {
	const res = await request.get('/api/valuation');
	expect(res.ok()).toBe(true);
	expect(((await res.json()) as { last_valuation: string | null }).last_valuation).toBeNull();

	await page.route('**/api/collection', (route) => route.fulfill({ json: shelf }));
	await page.goto('/stats');
	const value = page.getByRole('region', { name: 'Wartość kolekcji' });
	await expect(value.getByText(/^Nie ma jeszcze wyceny\./)).toBeVisible();
	await expect(page.getByRole('heading', { name: 'Wartość według rodzaju' })).toHaveCount(0);
	await expect(page.getByRole('heading', { name: 'Wartość według nośnika' })).toHaveCount(0);
	await expectNoSideScroll(page);
	await value.getByRole('link', { name: 'Wartość', exact: true }).tap();
	await expect(page).toHaveURL(/\/valuation$/);
	await expectNoSourceRequests(request);
});

test('Statystyki shows the value by kind and by format', async ({ page, request }) => {
	await page.route('**/api/collection', (route) => route.fulfill({ json: shelf }));
	await routeValuation(page, {
		last_valuation: pastDay(),
		window_days: 90,
		min_observations: 3,
		copies: [
			valued(1, { price_cents: 2_000_000, mid: olx(12_345_678), low_confidence: false }),
			valued(2, { format: 'bluray', price_cents: 4999, mid: olx(1500, 1) }),
			valued(3, { format: 'uhd', price_cents: null }),
			valued(4, { kind: 'book', format: null, price_cents: 3000, mid: olx(2500) }),
			valued(5, {
				kind: 'music',
				format: 'vinyl',
				price_cents: 8000,
				mid: { cents: 12000, source: 'discogs', count: 1, trimmed: 0 },
				low_confidence: false,
				discogs_release_id: 456
			}),
			valued(6, { kind: 'music', format: 'cassette', price_cents: 1000 })
		]
	});
	await page.goto('/stats');

	const kinds = page.getByRole('region', { name: 'Wartość według rodzaju' });
	await expect(kinds.getByRole('listitem')).toHaveCount(3);
	const films = kinds.getByRole('listitem').first();
	await expect(films).toContainText('Filmy');
	await expect(films).toContainText(pln.format(123_471.78));
	await expect(films).toContainText(
		`3 egzemplarze · zapłacono ${pln.format(20_049.99)} (bez ceny: 1)`
	);
	await expect(films).toContainText('bez wyceny: 1 · mało danych: 1');

	const formats = page.getByRole('region', { name: 'Wartość według nośnika' });
	await expect(formats.getByRole('listitem')).toHaveCount(6);
	await expect(formats.getByRole('listitem').first()).toContainText('DVD');
	await expect(formats.getByRole('listitem').filter({ hasText: 'Książki' })).toContainText(
		pln.format(25)
	);
	await expect(formats.getByRole('listitem').filter({ hasText: '4K UHD' })).toContainText(
		'brak wyceny'
	);
	await expect(formats.getByRole('listitem').filter({ hasText: 'Blu-ray' })).toContainText(
		'mało danych: 1'
	);
	await expect(
		formats
			.getByRole('listitem')
			.filter({ hasText: 'Winyl' })
			.getByRole('link', { name: /^Data provided by Discogs\./ })
	).toHaveAttribute('href', 'https://www.discogs.com/release/456');
	await expect(kinds.getByRole('link', { name: /^Data provided by Discogs\./ })).toHaveCount(1);
	await expect(
		formats.getByText(/^Wartość używanych egzemplarzy na dziś, z cen ofert z ostatnich 90 dni/)
	).toBeVisible();

	await expectNoSideScroll(page);
	await expectNoInnerScroll(kinds);
	await expectNoInnerScroll(formats);
	await expectNoSourceRequests(request);
});
