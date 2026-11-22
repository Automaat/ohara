import type { APIRequestContext, Page } from '@playwright/test';
import { expect, expectNoSourceRequests, test } from './fixtures.js';

/** Wartość states what sold copies made over what they cost (#237). */

type Realized = { copies: number; gain_cents: number; unpriced: number };
type View = { summary: { realized: Realized }; last_valuation: string | null };

const pln = new Intl.NumberFormat('pl-PL', { style: 'currency', currency: 'PLN' });

/** A day two days back, on or before the server's today whatever the time zones. */
function pastDay(): string {
	return new Date(Date.now() - 2 * 86_400_000).toISOString().slice(0, 10);
}

async function created(res: Awaited<ReturnType<APIRequestContext['post']>>): Promise<number> {
	expect(res.ok(), await res.text()).toBe(true);
	return ((await res.json()) as { id: number }).id;
}

/** Adds a DVD title, its edition and one copy sold for saleCents after it cost priceCents. */
async function sell(
	request: APIRequestContext,
	name: string,
	priceCents: number,
	saleCents: number
): Promise<number> {
	const cover = new URL('/pwa-192x192.png', test.info().project.use.baseURL).href;
	const titleID = await created(
		await request.post('/api/titles', { data: { kind: 'movie', name, cover_url: cover } })
	);
	const editionID = await created(
		await request.post('/api/editions', { data: { title_id: titleID, format: 'dvd' } })
	);
	return created(
		await request.post('/api/copies', {
			data: {
				edition_id: editionID,
				status: 'sold',
				price_cents: priceCents,
				sold_on: pastDay(),
				sale_price_cents: saleCents
			}
		})
	);
}

async function realized(request: APIRequestContext): Promise<Realized> {
	const res = await request.get('/api/valuation');
	expect(res.ok()).toBe(true);
	return ((await res.json()) as View).summary.realized;
}

/**
 * Opens Wartość on the server's answer, marked as valued, so the hero shows without a valuation
 * run, which would ask outside sources. patch changes the answer before the page reads it.
 */
async function openValued(page: Page, patch: (v: View) => View = (v) => v) {
	await page.route('**/api/valuation', async (route) => {
		const res = await route.fetch();
		const view = (await res.json()) as View;
		await route.fulfill({
			response: res,
			json: patch({ ...view, last_valuation: view.last_valuation ?? pastDay() })
		});
	});
	await page.goto('/valuation');
	await expect(page.getByRole('heading', { name: 'Szacowana wartość' })).toBeVisible();
}

test('Wartość shows the gain sold copies realized', async ({ page, request }) => {
	const before = await realized(request);
	const tag = `T${Date.now()}`;
	const copies = [
		await sell(request, `Zyskowny ${tag}`, 4000, 7000),
		await sell(request, `Stratny ${tag}`, 5000, 3500)
	];
	try {
		const after = await realized(request);
		expect(after).toEqual({
			copies: before.copies + 2,
			gain_cents: before.gain_cents + 1500,
			unpriced: before.unpriced
		});

		await openValued(page);
		const sign = after.gain_cents > 0 ? '+' : after.gain_cents < 0 ? '−' : '';
		const amount = `${sign}${pln.format(Math.abs(after.gain_cents) / 100)}`;
		const line = page.getByText(/^Zysk ze sprzedaży: /);
		await expect(line).toBeVisible();
		await expect(line).toContainText(amount);
		await expect(line).toContainText(new RegExp(`· ${after.copies} egzemplarz`));
		await expectNoSourceRequests(request);
	} finally {
		await Promise.all(copies.map((id) => request.delete(`/api/copies/${id}`)));
	}
});

test('Wartość shows a loss from sales with its sign', async ({ page }) => {
	await openValued(page, (v) => ({
		...v,
		summary: { ...v.summary, realized: { copies: 1, gain_cents: -2000, unpriced: 0 } }
	}));
	await expect(page.getByText(/^Zysk ze sprzedaży: /)).toHaveText(
		/^Zysk ze sprzedaży: −20,00\szł · 1 egzemplarz$/
	);
});

test('Wartość shows the gain from sales before the first valuation', async ({ page }) => {
	await page.route('**/api/valuation', async (route) => {
		const res = await route.fetch();
		const view = (await res.json()) as View;
		await route.fulfill({
			response: res,
			json: {
				...view,
				last_valuation: null,
				summary: { ...view.summary, realized: { copies: 1, gain_cents: 3000, unpriced: 0 } }
			}
		});
	});
	await page.goto('/valuation');
	await expect(page.getByText('Kolekcja nie ma jeszcze wyceny')).toBeVisible();
	await expect(page.getByText(/^Zysk ze sprzedaży: /)).toHaveText(
		/^Zysk ze sprzedaży: \+30,00\szł · 1 egzemplarz$/
	);
});

test('Wartość shows no realized gain without a sold copy', async ({ page, request }) => {
	await openValued(page, (v) => ({
		...v,
		summary: { ...v.summary, realized: { copies: 0, gain_cents: 0, unpriced: 0 } }
	}));
	await expect(page.getByText('Wydano', { exact: false })).toBeVisible();
	await expect(page.getByText(/Zysk ze sprzedaży/)).toHaveCount(0);
	await expectNoSourceRequests(request);
});
