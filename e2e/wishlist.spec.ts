import type { APIRequestContext, Locator, Page } from '@playwright/test';
import { expect, expectNoSourceRequests, test } from './fixtures.js';

/** Wishes live in Okazje: Kolekcja lists none, Okazje lists every one (#473). */

type Seeded = { titleID: number; copyID: number };

async function created(res: Awaited<ReturnType<APIRequestContext['post']>>): Promise<number> {
	expect(res.ok(), await res.text()).toBe(true);
	return ((await res.json()) as { id: number }).id;
}

/** A title with one edition and one copy, its cover from the app so no catalog is asked. */
async function seed(
	request: APIRequestContext,
	title: { kind: string; name: string; authors?: string[]; tags?: string[] },
	edition: { format?: string },
	copy: { status: string; max_price_cents?: number }
): Promise<Seeded> {
	const cover = new URL('/pwa-192x192.png', test.info().project.use.baseURL).href;
	const titleID = await created(
		await request.post('/api/titles', { data: { cover_url: cover, ...title } })
	);
	const editionID = await created(
		await request.post('/api/editions', { data: { title_id: titleID, ...edition } })
	);
	const copyID = await created(
		await request.post('/api/copies', { data: { edition_id: editionID, ...copy } })
	);
	return { titleID, copyID };
}

function wishList(page: Page): Locator {
	return page.getByRole('list', { name: 'Lista życzeń' });
}

const viewports = [
	{ name: 'phone', use: {}, touch: true },
	{
		name: 'desktop',
		use: {
			viewport: { width: 1440, height: 900 },
			deviceScaleFactor: 1,
			isMobile: false,
			hasTouch: false
		},
		touch: false
	}
] as const;

for (const viewport of viewports) {
	test.describe(viewport.name, () => {
		test.use(viewport.use);

		async function press(target: Locator) {
			if (viewport.touch) await target.tap();
			else await target.click();
		}

		test('Okazje lists every wish with its facts, title link and edit link', async ({
			page,
			request
		}) => {
			const run = `${viewport.name}-${Date.now()}`;
			const film = `Wymarzony film ${run}`;
			const book = `Wymarzona książka ${run}`;
			const owned = `Posiadany ${run}`;
			const wish = await seed(
				request,
				{ kind: 'movie', name: film },
				{ format: 'bluray' },
				{ status: 'wishlist', max_price_cents: 8000 }
			);
			await seed(
				request,
				{ kind: 'book', name: book, authors: ['Stanisław Lem'] },
				{},
				{ status: 'wishlist' }
			);
			await seed(request, { kind: 'movie', name: owned }, { format: 'dvd' }, { status: 'owned' });

			await page.goto('/deals');
			const list = wishList(page);
			await expect(list).toBeVisible();
			const filmCard = list.getByRole('listitem').filter({ hasText: film });
			await expect(filmCard).toContainText(/Film · Blu-ray · limit 80\szł/);
			await expect(list.getByRole('listitem').filter({ hasText: book })).toContainText(
				'Książka · Stanisław Lem'
			);
			await expect(list.getByRole('listitem').filter({ hasText: owned })).toHaveCount(0);

			await expect(filmCard.getByRole('link', { name: `Edytuj: ${film}` })).toHaveAttribute(
				'href',
				`/copies/${wish.copyID}`
			);
			await press(filmCard.getByRole('link', { name: film, exact: true }));
			await expect(page).toHaveURL(new RegExp(`/titles/${wish.titleID}$`));
			await expectNoSourceRequests(request);
		});

		test('a tag only wishes carry says so in Kolekcja', async ({ page, request }) => {
			const tag = `Marzenie ${viewport.name} ${Date.now()}`;
			await seed(
				request,
				{ kind: 'movie', name: `Życzenie z tagiem ${tag}`, tags: [tag] },
				{ format: 'dvd' },
				{ status: 'wishlist' }
			);

			await page.goto(`/?tag=${encodeURIComponent(tag)}`);
			const message = `Tag „${tag}” mają tylko pozycje z listy życzeń, a nie egzemplarze w kolekcji.`;
			await expect(page.getByText(message, { exact: true })).toBeVisible();

			await press(page.getByRole('button', { name: /^Sortowanie i filtry/ }));
			const sheet = page.getByRole('dialog', { name: 'Sortowanie i filtry' });
			await expect(
				sheet.getByText(
					'Ten tag mają tylko pozycje z listy życzeń, a nie egzemplarze w kolekcji.',
					{ exact: true }
				)
			).toBeVisible();
			await press(sheet.getByRole('button', { name: 'Gotowe' }));

			await press(page.locator('.results').getByRole('link', { name: 'Okazje', exact: true }));
			await expect(page).toHaveURL(/\/deals$/);
			await expect(
				wishList(page)
					.getByRole('listitem')
					.filter({ hasText: `Życzenie z tagiem ${tag}` })
			).toHaveCount(1);
		});
	});
}
