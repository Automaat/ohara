import { expect, expectNoSourceRequests, test, type Item } from './fixtures.js';

/** The copy of a book typed in through "Dodaj tytuł", read back from the collection. */
async function itemNamed(request: Parameters<typeof expectNoSourceRequests>[0], name: string) {
	const res = await request.get('/api/collection');
	expect(res.ok()).toBe(true);
	const { items } = (await res.json()) as { items: Item[] };
	return items.find((i) => i.name === name);
}

test('a book added as an audiobook shows it on the shelf, in the Nośnik filter and the edition form', async ({
	page,
	request
}) => {
	const run = String(Date.now());
	const audio = `Słuchowisko ${run}`;
	const paper = `Papier ${run}`;

	const title = await request.post('/api/titles', { data: { kind: 'book', name: paper } });
	expect(title.ok()).toBe(true);
	const edition = await request.post('/api/editions', {
		data: { title_id: ((await title.json()) as { id: number }).id, binding: 'paperback' }
	});
	expect(edition.ok()).toBe(true);
	const copy = await request.post('/api/copies', {
		data: { edition_id: ((await edition.json()) as { id: number }).id, status: 'owned' }
	});
	expect(copy.ok()).toBe(true);

	await page.goto('/titles/new');
	const form = page.getByRole('form', { name: 'Potwierdź pozycję' });
	await form.getByRole('textbox', { name: 'Tytuł' }).fill(audio);
	await form.getByRole('radio', { name: 'Książka' }).check();
	await form.getByRole('radio', { name: 'Audiobook' }).check();
	await form.getByRole('button', { name: 'Dodaj do kolekcji' }).click();
	await expect(page).toHaveURL(/\/titles\/\d+$/);

	const item = await itemNamed(request, audio);
	expect(item).toMatchObject({ kind: 'book', format: 'audiobook', barcode: null });

	await page.goto('/');
	const shelf = page.getByRole('list', { name: 'Egzemplarze w kolekcji' });
	const row = (name: string) =>
		shelf.getByRole('listitem').filter({ has: page.getByRole('link', { name }) });
	await expect(row(audio)).toContainText('Audiobook');
	await expect(row(paper)).toContainText('Miękka oprawa');

	await page.getByRole('button', { name: /^Sortowanie i filtry/ }).tap();
	const sheet = page.getByRole('dialog', { name: 'Sortowanie i filtry' });
	await sheet.getByLabel('Nośnik').selectOption('audiobook');
	await sheet.getByRole('button', { name: 'Gotowe' }).tap();
	await expect(page).toHaveURL(/\/\?format=audiobook$/);
	await expect(row(audio)).toHaveCount(1);
	await expect(row(paper)).toHaveCount(0);

	await page.goto(`/editions/${item?.edition_id}`);
	await expect(page.getByText('Audiobook', { exact: true }).first()).toBeVisible();
	await page.getByRole('button', { name: 'Edytuj wydanie' }).tap();
	const medium = page.getByRole('combobox', { name: 'Nośnik' });
	await expect(medium).toHaveValue('audiobook');
	await expect(page.getByRole('combobox', { name: 'Oprawa' })).toHaveCount(0);
	await medium.selectOption('');
	await page.getByRole('combobox', { name: 'Oprawa' }).selectOption('hardcover');
	await page.getByRole('button', { name: 'Zapisz zmiany' }).tap();
	await expect(page.getByRole('button', { name: 'Edytuj wydanie' })).toBeVisible();
	await expect.poll(async () => (await itemNamed(request, audio))?.format ?? null).toBeNull();

	await expectNoSourceRequests(request);
});
