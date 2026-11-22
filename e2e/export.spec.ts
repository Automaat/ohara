import type { Page } from '@playwright/test';
import { expect, expectNoSourceRequests, test } from './fixtures.js';

/** Chooses item in Kolekcja's export menu and returns the downloaded file's name. */
async function exportAs(page: Page, item: string): Promise<string> {
	await page.getByRole('button', { name: 'Eksportuj kolekcję' }).click();
	const download = page.waitForEvent('download');
	await page.getByRole('menuitem', { name: item }).click();
	const file = await download;
	expect(await file.failure()).toBeNull();
	return file.suggestedFilename();
}

test('Kolekcja exports the collection as CSV and JSON files', async ({ page, request }) => {
	const title = await request.post('/api/titles', {
		data: { kind: 'movie', name: `Eksport ${Date.now()}` }
	});
	expect(title.ok()).toBe(true);

	await page.goto('/');
	expect(await exportAs(page, 'CSV (Excel)')).toMatch(/^ohara-\d{4}-\d{2}-\d{2}\.csv$/);
	expect(await exportAs(page, 'JSON')).toMatch(/^ohara-\d{4}-\d{2}-\d{2}\.json$/);
	await expectNoSourceRequests(request);
});
