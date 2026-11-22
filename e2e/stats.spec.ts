import type { APIRequestContext, Page } from '@playwright/test';
import { expect, expectNoSourceRequests, test } from './fixtures.js';

/** Statystyki: spending per month, collection growth and new against used (#246). */

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

test('Statystyki without owned copies leads to Skanuj', async ({ page }) => {
	const empty = {
		items: [
			{
				copy_id: 1,
				created_at_us: Date.now() * 1000,
				status: 'wishlist',
				price_cents: null,
				max_price_cents: null,
				purchased_on: null,
				shop: '',
				acquired_as: null,
				condition: null,
				edition_id: 1,
				barcode: null,
				label: '',
				format: 'dvd',
				binding: null,
				title_id: 1,
				kind: 'movie',
				name: 'Życzenie',
				original_title: null,
				polish_title: null,
				year: null
			}
		],
		totals: { copies: 1, owned: 0, wishlist: 1, priced: 0, spent_cents: 0 }
	};
	await page.route('**/api/collection', (route) => route.fulfill({ json: empty }));
	await page.goto('/stats');
	await expect(page.getByText('Brak egzemplarzy na półce', { exact: true })).toBeVisible();
	await expect(page.getByRole('heading', { name: 'Wydatki w miesiącach' })).toHaveCount(0);
	await expectNoSideScroll(page);
	// The tab bar's scan button shares the name, so the link is looked up on the page itself.
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
			'Nowe czy używane'
		]);
		await expect(page.getByText(/^Bez daty: /)).toContainText(pln.format(undatedCents / 100));
		await expect(page.getByRole('img', { name: /^Nowe \d+%, Używane \d+%/ }).first()).toBeVisible();

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
