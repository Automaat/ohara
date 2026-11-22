import type { Page, Request } from '@playwright/test';
import { expect, expectNoSourceRequests, test, uniqueEAN } from './fixtures.js';

const badgePath = '/api/deals?under_limit=true&limit=500';

/** The app's API requests from now on, as "METHOD /path?query". */
function recordApi(page: Page): string[] {
	const seen: string[] = [];
	page.on('request', (req: Request) => {
		const url = new URL(req.url());
		if (url.pathname.startsWith('/api/')) seen.push(`${req.method()} ${url.pathname}${url.search}`);
	});
	return seen;
}

/** Opens the scanner once its first badge and queue reads answered, and returns the queue count. */
async function openScanner(page: Page): Promise<number> {
	const badge = page.waitForResponse((res) => res.url().endsWith(badgePath));
	await page.goto('/scan');
	await badge;
	const chip = page.getByRole('link', { name: /^Do uzupełnienia: \d+$/ });
	await expect(chip).toBeVisible();
	return queueCount(page);
}

async function queueCount(page: Page): Promise<number> {
	const label = await page
		.getByRole('link', { name: /^Do uzupełnienia: \d+$/ })
		.getAttribute('aria-label');
	return Number(label?.split(': ')[1]);
}

/** Types a code as a wedge scanner would, which opens the sheet over the camera. */
async function scan(page: Page, code: string) {
	await page.getByRole('button', { name: 'Wpisz kod' }).click();
	const field = page.getByLabel('Kod kreskowy');
	await field.pressSequentially(code);
	await field.press('Enter');
}

/** Scans a code the stub knows and leaves its sheet with "Pomiń". */
async function scanAndSkip(page: Page, code: string) {
	await scan(page, code);
	const sheet = page.getByRole('dialog', { name: 'Znaleziono' });
	await expect(sheet.getByRole('button', { name: `Film ${code}` })).toBeVisible();
	await sheet.getByRole('button', { name: 'Pomiń' }).click();
	await expect(sheet).toBeHidden();
}

test('the scan sheet sends no deals or queue read, and a page change refreshes the badge', async ({
	page,
	request
}) => {
	await openScanner(page);
	const seen = recordApi(page);

	await scanAndSkip(page, uniqueEAN(0));
	await scanAndSkip(page, uniqueEAN(1));
	await scanAndSkip(page, uniqueEAN(2));
	expect(seen.filter((r) => r.startsWith('GET /api/lookup/'))).toHaveLength(3);
	expect(seen.filter((r) => r.includes('/api/deals'))).toEqual([]);
	expect(seen.filter((r) => r.startsWith('GET /api/scan-queue'))).toEqual([]);

	const badge = page.waitForRequest((req) => req.url().endsWith(badgePath));
	await page
		.getByRole('navigation', { name: 'Główne strony' })
		.getByRole('link', { name: 'Kolekcja' })
		.click();
	await expect(page).toHaveURL(/\/$/);
	await badge;

	await expectNoSourceRequests(request);
});

test('the queue count follows a queued scan, the add that resolves it and its undo', async ({
	page,
	request
}) => {
	const code = uniqueEAN();
	const before = await openScanner(page);
	const seen = recordApi(page);

	await page.route('**/api/lookup/**', (route) =>
		route.fulfill({ json: { barcode: code, candidates: [], errors: [] } })
	);
	await scan(page, code);
	const missing = page.getByRole('dialog', { name: 'Nie znaleziono' });
	await expect(missing).toBeVisible();
	await missing.getByRole('button', { name: 'Skanuj dalej' }).click();
	await expect(missing).toBeHidden();
	await expect.poll(() => queueCount(page)).toBe(before + 1);
	await page.unroute('**/api/lookup/**');

	await scan(page, code);
	const sheet = page.getByRole('dialog', { name: 'Znaleziono' });
	await sheet.getByRole('button', { name: `Film ${code}` }).click();
	await sheet.getByRole('button', { name: 'Dodaj do kolekcji' }).click();
	const toasts = page.getByRole('region', { name: 'Powiadomienia' });
	await expect(toasts.getByText('Dodano do kolekcji', { exact: true })).toBeVisible();
	await expect.poll(() => queueCount(page)).toBe(before);

	await toasts.getByRole('button', { name: 'Cofnij' }).click();
	await expect(toasts.getByText('Cofnięto dodanie', { exact: true })).toBeVisible();
	await expect.poll(() => queueCount(page)).toBe(before + 1);

	const queue = await request.get('/api/scan-queue');
	expect(queue.ok()).toBe(true);
	const entries = (await queue.json()) as { barcode: string }[];
	expect(entries.map((e) => e.barcode)).toContain(code);
	expect(entries).toHaveLength(before + 1);
	expect(seen.filter((r) => r.startsWith('GET /api/scan-queue'))).toEqual([]);

	await expectNoSourceRequests(request);
});
