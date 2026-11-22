import { expect, expectNoSourceRequests, test, uniqueEAN, type Item } from './fixtures.js';

test('a wedge scan adds the stub film and Cofnij takes it all back', async ({ page, request }) => {
	const code = uniqueEAN();
	const name = `Film ${code}`;

	await page.goto('/scan');
	await page.getByRole('button', { name: 'Wpisz kod' }).click();
	const field = page.getByLabel('Kod kreskowy');
	await field.pressSequentially(code);
	await field.press('Enter');

	const sheet = page.getByRole('dialog', { name: 'Znaleziono' });
	await sheet.getByRole('button', { name }).click();
	await expect(sheet.getByRole('heading', { name })).toBeVisible();
	await expect(sheet.getByRole('radio', { name: 'DVD' })).toBeChecked();
	await sheet.getByRole('button', { name: 'Dodaj do kolekcji' }).click();

	const toasts = page.getByRole('region', { name: 'Powiadomienia' });
	await expect(toasts.getByText('Dodano do kolekcji', { exact: true })).toBeVisible();
	await expect(toasts.getByText(`${name} · DVD`)).toBeVisible();
	const item = await collectionItem(request, name);
	expect(item, 'the add is in the collection').toBeDefined();

	await toasts.getByRole('button', { name: 'Cofnij' }).click();
	await expect(toasts.getByText('Cofnięto dodanie', { exact: true })).toBeVisible();

	expect(await collectionItem(request, name), 'the copy is gone').toBeUndefined();
	const titles = await request.get('/api/titles');
	expect(titles.ok()).toBe(true);
	const names = ((await titles.json()) as { name: string }[]).map((t) => t.name);
	expect(names, 'the title is gone').not.toContain(name);
	if (item) {
		const paths = [
			`/api/copies/${item.copy_id}`,
			`/api/editions/${item.edition_id}`,
			`/api/titles/${item.title_id}`
		];
		const statuses = await Promise.all(paths.map(async (p) => (await request.get(p)).status()));
		expect(Object.fromEntries(paths.map((p, i) => [p, statuses[i]]))).toEqual(
			Object.fromEntries(paths.map((p) => [p, 404]))
		);
	}

	await expectNoSourceRequests(request);
});

/** The collection's item for the title named `name`, if any. */
async function collectionItem(
	request: Parameters<typeof expectNoSourceRequests>[0],
	name: string
): Promise<Item | undefined> {
	const res = await request.get('/api/collection');
	expect(res.ok()).toBe(true);
	const { items } = (await res.json()) as { items: Item[] };
	return items.find((i) => i.name === name);
}
