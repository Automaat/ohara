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
	mid_grade?: string;
	discogs_release_id?: number | null;
	condition: string | null;
	authors: string[];
	binding: string | null;
};
type View = {
	summary: { copies: number; spent_cents: number; mid: Bound; ceiling: Bound };
	copies: CopyValue[];
	last_valuation: string | null;
};

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
		await expect(total('Ostatnia wycena')).toBeVisible();
		await expect(total('Kwoty według cen na dzień')).toBeVisible();
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
		await page.goto('/valuation');
		await expect(page.getByText('Kolekcja nie ma jeszcze wyceny')).toBeVisible();
		await page.getByRole('link', { name: 'Raport dla ubezpieczyciela' }).click();
		await expect(page).toHaveURL(/\/valuation\/report$/);
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
	await page.goto('/valuation/report');
	await expect(page.getByText('Brak posiadanych egzemplarzy')).toBeVisible();
	await expect(reportTable(page)).toHaveCount(0);
	await expect(page.getByRole('link', { name: 'Przejdź do kolekcji' })).toHaveAttribute(
		'href',
		'/'
	);
});

/** One creator name of 500 letters without a space, as long as the API takes. */
const unbroken = 'W'.repeat(500);

/** Twenty creators of 495 letters each, far more than one printed row holds. */
const crowd = Array.from(
	{ length: 20 },
	(_, i) => `${'Ł'.repeat(490)} ${String(i).padStart(3, '0')}`
);

const discogsRelease = 4242;

/**
 * Sixty owned copies of every kind, valued in parts, each with a cover from the app's origin.
 * Copy 2 has an unbroken 500-letter creator, copy 3 twenty long ones, and copy 5 a Discogs
 * release priced at Very Good Plus.
 */
function sixty(): CopyValue[] {
	const kinds = ['movie', 'book', 'music'] as const;
	const formats = { movie: 'bluray', book: null, music: 'vinyl' } as const;
	const est = (cents: number, source = 'olx'): Estimate => ({
		cents,
		source,
		count: 3,
		trimmed: 0
	});
	return Array.from({ length: 60 }, (_, n) => {
		const i = n + 1;
		const kind = kinds[i % 3];
		const id = 900_000 + i;
		const authors =
			i === 2 ? [unbroken] : i === 3 ? crowd : kind === 'movie' ? [] : [`Twórca ${i}`, 'Drugi'];
		return {
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
			mid: i === 5 ? est(12000, 'discogs') : i % 3 === 0 ? null : est(2000 + i * 50),
			ceiling: i === 5 ? est(20000, 'discogs') : i % 5 === 0 ? est(5000 + i * 10) : null,
			mid_grade: i === 5 ? 'VG+' : undefined,
			discogs_release_id: i === 5 ? discogsRelease : null,
			low_confidence: false,
			likely_sold: null,
			condition: i === 5 ? 'poor' : 'good',
			authors,
			binding: kind === 'book' ? 'paperback' : null
		};
	});
}

async function fulfillSixty(page: Page) {
	const copies = sixty();
	await page.route('**/api/valuation', async (route: Route) => {
		const res = await route.fetch();
		const v = (await res.json()) as View;
		await route.fulfill({
			response: res,
			json: { ...v, last_valuation: pastDay(), copies, summary: { ...v.summary, copies: 60 } }
		});
	});
	return copies;
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

/** The width and height in points of every page of a PDF, from its media boxes. */
function pageSizes(pdf: Awaited<ReturnType<Page['pdf']>>): string[] {
	const boxes = pdf.toString('latin1').matchAll(/\/MediaBox\s*\[\s*0 0 ([\d.]+) ([\d.]+)\s*\]/g);
	return [...boxes].map((m) => `${Math.round(Number(m[1]))}x${Math.round(Number(m[2]))}`);
}

/**
 * The amount cells of every row that end past the table or the page: none when the columns fit
 * the printable width.
 */
function clipped(table: Element): number {
	const right = Math.min(table.getBoundingClientRect().right, document.documentElement.clientWidth);
	return [...table.querySelectorAll('tbody td.num')].filter(
		(cell) => cell.getBoundingClientRect().right > right + 0.5
	).length;
}

test('Raport dla ubezpieczyciela prints on A4 with every cover and no controls', async ({
	page
}) => {
	const failed: string[] = [];
	page.on('requestfailed', (req) => failed.push(req.url()));
	page.on('response', (res) => {
		if (res.request().resourceType() === 'image' && !res.ok()) failed.push(res.url());
	});
	const copies = await fulfillSixty(page);
	await page.goto('/valuation/report');
	await expect(
		page.getByText(/^Ostatnia wycena: .+ Kwoty według cen z ostatnich \d+ dni/)
	).toBeVisible();
	const table = reportTable(page);
	await expect(table.locator('tbody tr')).toHaveCount(60);

	const covers = table.locator('tbody img');
	await expect(covers).toHaveCount(60);
	await expect.poll(() => covers.evaluateAll(loaded)).toBe(true);
	expect(await covers.evaluateAll((imgs) => imgs.map((i) => i.getAttribute('src')))).toEqual(
		copies.map((c) => c.cover_url)
	);
	await expect(page.getByRole('button', { name: 'Drukuj' })).toBeEnabled();

	const row = (n: string) => table.locator('tbody tr').filter({ hasText: `Pozycja ${n}` });
	const discogs = row('05');
	await expect(discogs).toContainText('Słaby');
	await expect(discogs).toContainText('ocena VG+');
	await expect(discogs.getByRole('link', { name: /Data provided by Discogs\./ })).toHaveAttribute(
		'href',
		new RegExp(`/release/${discogsRelease}$`)
	);
	await expect(row('02')).toContainText('(pełna lista: przypis 1)');
	await expect(row('03')).toContainText('(pełna lista: przypis 2)');
	const appendix = page.getByRole('region', { name: 'Przypisy: pełne listy twórców' });
	await expect(appendix.getByRole('listitem')).toHaveCount(2);
	await expect(appendix.getByRole('listitem').first()).toContainText(unbroken);
	await expect(appendix.getByRole('listitem').last()).toContainText(crowd[19]);

	await page.setViewportSize({ width: 718, height: 1047 });
	await page.emulateMedia({ media: 'print' });
	await expect(page.getByRole('navigation', { name: 'Główne strony' })).toBeHidden();
	await expect(page.getByRole('button')).toHaveCount(0);
	await expect(page.getByRole('link')).toHaveCount(1);
	await expect(page.getByRole('link')).toHaveText(/Data provided by Discogs\./);
	expect(await covers.evaluateAll(drawn)).toBe(true);
	expect(await table.evaluate(clipped)).toBe(0);
	const layout = await table.evaluate((t) => ({
		head: getComputedStyle(t.querySelector('thead') as Element).display,
		rows: [...t.querySelectorAll('tbody tr')].map((r) => getComputedStyle(r).breakInside),
		page: getComputedStyle(t.closest('.report') as Element).page
	}));
	expect(layout.head).toBe('table-header-group');
	expect(new Set(layout.rows)).toEqual(new Set(['avoid']));
	expect(layout.page).toBe('insurance-report');

	const saved = test.info().outputPath('insurance-report.pdf');
	const pdf = await page.pdf({ path: saved, preferCSSPageSize: true, printBackground: true });
	const sizes = pageSizes(pdf);
	expect(sizes.length).toBeGreaterThan(1);
	expect(new Set(sizes)).toEqual(new Set(['595x842']));
	await test.info().attach('insurance-report.pdf', { path: saved, contentType: 'application/pdf' });
	expect(failed, 'failed requests').toEqual([]);
});

test('Raport dla ubezpieczyciela sets A4 only for itself', async ({ page }) => {
	await fulfillSixty(page);
	await page.goto('/valuation');
	await page.getByRole('link', { name: 'Raport dla ubezpieczyciela' }).click();
	await expect(reportTable(page).locator('tbody tr')).toHaveCount(60);
	await page.getByRole('link', { name: 'Wartość' }).first().click();
	await expect(page).toHaveURL(/\/valuation$/);
	await expect(reportTable(page)).toHaveCount(0);
	const pdf = await page.pdf({ preferCSSPageSize: true });
	expect(pageSizes(pdf).length).toBeGreaterThan(0);
	expect(new Set(pageSizes(pdf))).not.toContain('595x842');
});

test('Raport dla ubezpieczyciela offers printing only once every cover has loaded', async ({
	page
}) => {
	await fulfillSixty(page);
	let release: () => void = () => {};
	const held = new Promise<void>((resolve) => {
		release = resolve;
	});
	await page.route('**/pwa-512x512.png', async (route) => {
		await held;
		await route.continue();
	});
	await page.goto('/valuation/report');
	await expect(reportTable(page).locator('tbody tr')).toHaveCount(60);
	await expect(page.getByRole('button', { name: 'Wczytywanie okładek…' })).toBeVisible();
	await expect(page.getByRole('button', { name: 'Drukuj' })).toHaveCount(0);
	release();
	await expect(page.getByRole('button', { name: 'Drukuj' })).toBeEnabled();
	await expect.poll(() => reportTable(page).locator('tbody img').evaluateAll(loaded)).toBe(true);
});
