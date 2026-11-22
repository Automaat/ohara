import type { APIRequestContext, Page, Route } from '@playwright/test';
import { expect, expectNoSourceRequests, test, uniqueEAN } from './fixtures.js';

/** The insurance report lists every owned copy with its values and prints on A4 (#245). */

type Estimate = { cents: number; source: string; count: number; trimmed: number };
type Bound = { cents: number; copies: number };
type CopyValue = {
	copy_id: number;
	edition_id: number;
	title_id: number;
	title_name: string;
	kind: string;
	format: string | null;
	barcode: string | null;
	price_cents: number | null;
	cover_url?: string;
	floor: Estimate | null;
	mid: Estimate | null;
	ceiling: Estimate | null;
	low_confidence: boolean;
	likely_sold: Estimate | null;
};
type View = {
	summary: { copies: number; spent_cents: number; mid: Bound; ceiling: Bound };
	copies: CopyValue[];
	last_valuation: string | null;
};
type Item = { copy_id: number; [key: string]: unknown };

const pln = new Intl.NumberFormat('pl-PL', { style: 'currency', currency: 'PLN' });
const amount = (cents: number) => pln.format(cents / 100);

const usedValue: Estimate = { cents: 6500, source: 'olx', count: 4, trimmed: 0 };
const newValue: Estimate = { cents: 9900, source: 'discogs', count: 1, trimmed: 0 };

/** A day two days back, on or before the server's today whatever the time zones. */
function pastDay(): string {
	return new Date(Date.now() - 2 * 86_400_000).toISOString().slice(0, 10);
}

async function created(res: Awaited<ReturnType<APIRequestContext['post']>>): Promise<number> {
	expect(res.ok(), await res.text()).toBe(true);
	return ((await res.json()) as { id: number }).id;
}

/**
 * Adds an owned copy of a new title and returns the copy's ID. The title comes with a cover, so
 * the server looks none up outside.
 */
async function own(
	request: APIRequestContext,
	title: Record<string, unknown>,
	edition: Record<string, unknown>,
	copy: Record<string, unknown>
): Promise<number> {
	const cover = new URL('/pwa-192x192.png', test.info().project.use.baseURL).href;
	const titleID = await created(
		await request.post('/api/titles', { data: { cover_url: cover, ...title } })
	);
	const editionID = await created(
		await request.post('/api/editions', { data: { title_id: titleID, ...edition } })
	);
	return created(
		await request.post('/api/copies', {
			data: { edition_id: editionID, status: 'owned', ...copy }
		})
	);
}

/** Answers `GET /api/valuation` with the server's view changed by patch. */
async function patchValuation(page: Page, patch: (v: View) => View) {
	await page.route('**/api/valuation', async (route) => {
		const res = await route.fetch();
		await route.fulfill({ response: res, json: patch((await res.json()) as View) });
	});
}

/**
 * The server's view as a valuation would leave it, without running one, which would ask outside
 * sources: valued, with copyID given a used and a new value that the summary sums as the server
 * sums them.
 */
function valuedWith(v: View, copyID: number): View {
	return {
		...v,
		last_valuation: v.last_valuation ?? pastDay(),
		copies: v.copies.map((c) =>
			c.copy_id === copyID ? { ...c, mid: usedValue, ceiling: newValue } : c
		),
		summary: {
			...v.summary,
			mid: { cents: v.summary.mid.cents + usedValue.cents, copies: v.summary.mid.copies + 1 },
			ceiling: {
				cents: v.summary.ceiling.cents + newValue.cents,
				copies: v.summary.ceiling.copies + 1
			}
		}
	};
}

const reportTable = (page: Page) => page.getByRole('table', { name: 'Egzemplarze w raporcie' });

test('Raport dla ubezpieczyciela lists every owned copy with totals of the valuation', async ({
	page,
	request
}) => {
	const tag = `R${Date.now()}`;
	const barcode = uniqueEAN();
	const valued = await own(
		request,
		{ kind: 'music', name: `Album ${tag}`, authors: ['Wykonawca Testowy'] },
		{ format: 'vinyl', barcode },
		{ price_cents: 4999, condition: 'very_good' }
	);
	const unvalued = await own(
		request,
		{ kind: 'book', name: `Książka ${tag}`, authors: ['Autor Pierwszy', 'Autor Drugi'] },
		{ binding: 'hardcover' },
		{}
	);
	try {
		let view: View | null = null;
		await patchValuation(page, (v) => {
			view = valuedWith(v, valued);
			return view;
		});

		await page.goto('/valuation');
		await page.getByRole('link', { name: 'Raport dla ubezpieczyciela' }).click();
		await expect(page).toHaveURL(/\/valuation\/report$/);
		await expect(page.getByRole('heading', { name: 'Raport dla ubezpieczyciela' })).toBeVisible();
		const table = reportTable(page);
		await expect(table).toBeVisible();
		expect(view).not.toBeNull();
		const v = view as unknown as View;

		const body = table.locator('tbody tr');
		await expect(body).toHaveCount(v.copies.length);
		expect(v.copies.length).toBe(v.summary.copies);

		const first = body.filter({ hasText: `Album ${tag}` });
		await expect(first).toContainText('Wykonawca Testowy');
		await expect(first).toContainText('Muzyka · Winyl');
		await expect(first).toContainText(barcode);
		await expect(first).toContainText('Bardzo dobry');
		await expect(first).toContainText(amount(4999));
		await expect(first).toContainText(amount(usedValue.cents));
		await expect(first).toContainText(amount(newValue.cents));

		const second = body.filter({ hasText: `Książka ${tag}` });
		await expect(second).toContainText('Autor Pierwszy, Autor Drugi');
		await expect(second).toContainText('Książka · Twarda oprawa');
		await expect(second.getByText('brak wyceny')).toHaveCount(2);

		const totals = page.getByRole('region', { name: 'Podsumowanie' });
		const all = v.summary.copies;
		const total = (label: string) =>
			totals.locator('dl > div').filter({ has: page.getByText(label, { exact: true }) });
		await expect(total('Egzemplarze')).toContainText(String(all));
		await expect(total('Zapłacono')).toContainText(amount(v.summary.spent_cents));
		await expect(total('Wartość (używane)')).toContainText(amount(v.summary.mid.cents));
		await expect(total('Wartość (używane)')).toContainText(
			`${v.summary.mid.copies} z ${all} egzemplarz`
		);
		await expect(total('Wartość (nowe)')).toContainText(amount(v.summary.ceiling.cents));
		await expect(total('Wartość (nowe)')).toContainText(
			`${v.summary.ceiling.copies} z ${all} egzemplarz`
		);
		await expect(total('Data wyceny')).toBeVisible();
		await expectNoSourceRequests(request);
	} finally {
		await Promise.all([valued, unvalued].map((id) => request.delete(`/api/copies/${id}`)));
	}
});

test('Raport dla ubezpieczyciela says no valuation exists before the first one', async ({
	page,
	request
}) => {
	const copy = await own(
		request,
		{ kind: 'movie', name: `Bez wyceny ${Date.now()}` },
		{ format: 'dvd' },
		{}
	);
	try {
		await patchValuation(page, (v) => ({ ...v, last_valuation: null }));
		await page.goto('/valuation/report');
		await expect(page.getByText('Kolekcja nie ma jeszcze wyceny')).toBeVisible();
		await expect(reportTable(page)).toHaveCount(0);
		await page.getByRole('link', { name: 'Przejdź do wyceny' }).click();
		await expect(page).toHaveURL(/\/valuation$/);
	} finally {
		await request.delete(`/api/copies/${copy}`);
	}
});

test('Raport dla ubezpieczyciela shows an empty state without an owned copy', async ({ page }) => {
	await patchValuation(page, (v) => ({
		...v,
		last_valuation: pastDay(),
		copies: [],
		summary: { ...v.summary, copies: 0, spent_cents: 0 }
	}));
	await page.route(
		(url) => url.pathname === '/api/collection',
		(route) => route.fulfill({ json: { items: [], totals: {} } })
	);
	await page.goto('/valuation/report');
	await expect(page.getByText('Brak posiadanych egzemplarzy')).toBeVisible();
	await expect(reportTable(page)).toHaveCount(0);
	await expect(page.getByRole('link', { name: 'Przejdź do kolekcji' })).toHaveAttribute(
		'href',
		'/'
	);
});

/** Sixty owned copies of every kind, valued in parts, each with a cover from the app's origin. */
function sixty(): { copies: CopyValue[]; items: Item[] } {
	const kinds = ['movie', 'book', 'music'] as const;
	const formats = { movie: 'bluray', book: null, music: 'vinyl' } as const;
	const est = (cents: number): Estimate => ({ cents, source: 'olx', count: 3, trimmed: 0 });
	const copies: CopyValue[] = [];
	const items: Item[] = [];
	for (let i = 1; i <= 60; i++) {
		const kind = kinds[i % 3];
		const id = 900_000 + i;
		copies.push({
			copy_id: id,
			edition_id: id,
			title_id: id,
			title_name: `Pozycja ${String(i).padStart(2, '0')} o dość długim tytule do zawinięcia`,
			kind,
			format: formats[kind],
			barcode: `590000000${String(i).padStart(4, '0')}`,
			price_cents: i % 4 === 0 ? null : 1000 + i * 100,
			cover_url: i % 2 === 0 ? '/pwa-192x192.png' : '/pwa-512x512.png',
			floor: null,
			mid: i % 3 === 0 ? null : est(2000 + i * 50),
			ceiling: i % 5 === 0 ? est(5000 + i * 10) : null,
			low_confidence: false,
			likely_sold: null
		});
		items.push({
			copy_id: id,
			authors: kind === 'movie' ? [] : [`Twórca ${i}`, 'Drugi Twórca'],
			binding: kind === 'book' ? 'paperback' : null,
			condition: 'good'
		});
	}
	return { copies, items };
}

async function fulfillSixty(page: Page) {
	const { copies, items } = sixty();
	await page.route('**/api/valuation', async (route: Route) => {
		const res = await route.fetch();
		const v = (await res.json()) as View;
		await route.fulfill({
			response: res,
			json: { ...v, last_valuation: pastDay(), copies, summary: { ...v.summary, copies: 60 } }
		});
	});
	await page.route(
		(url) => url.pathname === '/api/collection',
		(route) => route.fulfill({ json: { items, totals: {} } })
	);
}

/** Whether every image the locator finds has loaded. */
function loaded(imgs: Element[]): boolean {
	return imgs.every((i) => i instanceof HTMLImageElement && i.complete && i.naturalWidth > 0);
}

/** Whether every image the locator finds takes room on the page and is not faded out. */
function drawn(imgs: Element[]): boolean {
	return imgs.every((i) => {
		const box = i.getBoundingClientRect();
		return box.width > 0 && box.height > 0 && getComputedStyle(i).opacity === '1';
	});
}

test('Raport dla ubezpieczyciela prints on A4 with every cover and no controls', async ({
	page
}) => {
	const failed: string[] = [];
	page.on('requestfailed', (req) => failed.push(req.url()));
	page.on('response', (res) => {
		if (res.request().resourceType() === 'image' && !res.ok()) failed.push(res.url());
	});
	await fulfillSixty(page);
	await page.goto('/valuation/report');
	const table = reportTable(page);
	await expect(table.locator('tbody tr')).toHaveCount(60);

	const covers = table.locator('tbody img');
	await expect(covers).toHaveCount(60);
	await expect.poll(() => covers.evaluateAll(loaded)).toBe(true);

	await page.emulateMedia({ media: 'print' });
	await expect(page.getByRole('navigation', { name: 'Główne strony' })).toBeHidden();
	await expect(page.getByRole('button')).toHaveCount(0);
	await expect(page.getByRole('link')).toHaveCount(0);
	expect(await covers.evaluateAll(drawn)).toBe(true);
	const layout = await table.evaluate((t) => ({
		head: getComputedStyle(t.querySelector('thead') as Element).display,
		rows: [...t.querySelectorAll('tbody tr')].map((r) => getComputedStyle(r).breakInside)
	}));
	expect(layout.head).toBe('table-header-group');
	expect(new Set(layout.rows)).toEqual(new Set(['avoid']));

	const saved = test.info().outputPath('insurance-report.pdf');
	const pdf = await page.pdf({ path: saved, format: 'A4', printBackground: true });
	const pages = (pdf.toString('latin1').match(/\/Type\s*\/Page[^s]/g) ?? []).length;
	expect(pages).toBeGreaterThan(1);
	await test.info().attach('insurance-report.pdf', { path: saved, contentType: 'application/pdf' });
	expect(failed, 'failed requests').toEqual([]);
});
