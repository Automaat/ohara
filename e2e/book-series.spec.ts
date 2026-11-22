import { expect, expectNoSourceRequests, samples, test, type Item } from './fixtures.js';

/**
 * A book scanned from BN joins the series its record names (#249). The double in
 * e2e/compose.yaml answers this ISBN with its recorded BN search, so the stack must be fresh.
 */
test('a scanned BN book lands in the series its record names', async ({ page, request }) => {
	const code = '9788375780635';
	const name = 'Ostatnie życzenie';

	await page.goto('/scan');
	await page.getByRole('button', { name: 'Wpisz kod' }).click();
	const field = page.getByLabel('Kod kreskowy');
	await field.pressSequentially(code);
	await field.press('Enter');

	const sheet = page.getByRole('dialog', { name: 'Znaleziono' });
	await expect(sheet.getByRole('heading', { name })).toBeVisible();
	await sheet.getByRole('button', { name: 'Dodaj do kolekcji' }).click();
	const toasts = page.getByRole('region', { name: 'Powiadomienia' });
	await expect(toasts.getByText('Dodano do kolekcji', { exact: true })).toBeVisible();

	const res = await request.get('/api/collection');
	expect(res.ok()).toBe(true);
	const { items } = (await res.json()) as { items: Item[] };
	const item = items.find((i) => i.barcode === code);
	expect(item, 'the scanned book is in the collection').toBeDefined();

	await page.goto(`/titles/${item?.title_id}`);
	await expect(page.getByRole('heading', { name, level: 1 })).toBeVisible();
	await expect(page.getByRole('link', { name: 'Wiedźmin', exact: true })).toBeVisible();

	const metrics = await (await request.get('/metrics')).text();
	const bn = samples(metrics, 'ohara_source_requests_total').get('{source="bn",status="ok"}');
	expect(bn, 'BN answered from the double').toBe(1);
	await expectNoSourceRequests(request);
});
