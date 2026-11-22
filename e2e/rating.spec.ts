import { expect, expectNoSourceRequests, test } from './fixtures.js';

/** Rating a title from its page, without the edit form (#242). */

test('Ocena on a title page is stored, kept across a reload and cleared', async ({
	page,
	request
}) => {
	const name = `Ocena ${Date.now()}`;
	// An app-hosted cover keeps the create from asking a catalog for one.
	const cover = new URL('/pwa-192x192.png', test.info().project.use.baseURL).href;
	const title = await request.post('/api/titles', {
		data: {
			kind: 'book',
			name,
			authors: ['Bolesław Prus'],
			notes: 'Pierwsze wydanie',
			cover_url: cover
		}
	});
	expect(title.ok(), await title.text()).toBe(true);
	const { id } = (await title.json()) as { id: number };

	await page.goto(`/titles/${id}`);
	const rating = page.getByRole('combobox', { name: 'Ocena' });
	await expect(rating).toHaveValue('0');

	const edited = await request.put(`/api/titles/${id}`, {
		data: { kind: 'book', name, authors: ['Bolesław Prus'], notes: 'Poprawione' }
	});
	expect(edited.ok(), await edited.text()).toBe(true);

	const rated = page.waitForResponse(
		(res) => res.request().method() === 'PUT' && res.url().endsWith(`/api/titles/${id}/rating`)
	);
	await rating.selectOption({ label: '8 / 10' });
	expect((await rated).status()).toBe(200);
	await expect(page.getByRole('dialog', { name: 'Edycja tytułu' })).toBeHidden();

	await page.reload();
	await expect(rating).toHaveValue('8');
	await expect(page.getByText('Poprawione')).toBeVisible();

	const cleared = page.waitForResponse(
		(res) => res.request().method() === 'PUT' && res.url().endsWith(`/api/titles/${id}/rating`)
	);
	await rating.selectOption({ label: 'Brak oceny' });
	expect((await cleared).status()).toBe(200);

	await page.reload();
	await expect(rating).toHaveValue('0');

	const stored = (await (await request.get(`/api/titles/${id}`)).json()) as {
		rating?: number | null;
		authors: string[];
		notes: string;
		cover_url?: string;
	};
	expect(stored.rating ?? null).toBeNull();
	expect(stored.authors).toEqual(['Bolesław Prus']);
	expect(stored.notes).toBe('Poprawione');
	expect(stored.cover_url).toBe(cover);

	await expectNoSourceRequests(request);
});
