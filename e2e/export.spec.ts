import type { Download, Page } from '@playwright/test';
import { expect, expectNoSourceRequests, test } from './fixtures.js';

/** Waits for the download that clicking target starts and returns its file name. */
async function download(page: Page, click: () => Promise<void>): Promise<string> {
	const started: Promise<Download> = page.waitForEvent('download');
	await click();
	const file = await started;
	expect(await file.failure()).toBeNull();
	return file.suggestedFilename();
}

const csvName = /^ohara-\d{4}-\d{2}-\d{2}\.csv$/;
const jsonName = /^ohara-\d{4}-\d{2}-\d{2}\.json$/;

test.beforeEach(async ({ request }) => {
	const title = await request.post('/api/titles', {
		data: { kind: 'movie', name: `Eksport ${Date.now()}` }
	});
	expect(title.ok()).toBe(true);
});

test('on a phone the settings sheet downloads the collection as CSV and JSON', async ({
	page,
	request
}) => {
	await page.goto('/');
	await page.getByRole('button', { name: 'Ustawienia' }).click();
	const sheet = page.getByRole('dialog', { name: 'Ustawienia' });
	const choices = sheet.getByRole('region', { name: 'Eksportuj kolekcję' });
	expect(
		await download(page, () => choices.getByRole('link', { name: 'CSV (Excel)' }).click())
	).toMatch(csvName);
	expect(await download(page, () => choices.getByRole('link', { name: 'JSON' }).click())).toMatch(
		jsonName
	);
	await expectNoSourceRequests(request);
});

test.describe('desktop', () => {
	test.use({
		viewport: { width: 1440, height: 900 },
		deviceScaleFactor: 1,
		isMobile: false,
		hasTouch: false
	});

	test('the header menu downloads the collection as CSV and JSON', async ({ page, request }) => {
		await page.goto('/');
		const menu = page.getByRole('button', { name: 'Eksportuj kolekcję' });
		await menu.click();
		expect(
			await download(page, () => page.getByRole('menuitem', { name: 'CSV (Excel)' }).click())
		).toMatch(csvName);
		await menu.click();
		expect(
			await download(page, () => page.getByRole('menuitem', { name: 'JSON' }).click())
		).toMatch(jsonName);
		await expectNoSourceRequests(request);
	});
});
