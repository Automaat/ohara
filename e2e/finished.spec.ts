import type { APIRequestContext, Locator, Page } from '@playwright/test';
import { expect, expectNoSourceRequests, test } from './fixtures.js';

/** A title records whether it was watched, read or heard, and Kolekcja lists the rest (#243). */

type Seeded = { titleID: number; copyID: number };

async function created(res: Awaited<ReturnType<APIRequestContext['post']>>): Promise<number> {
	expect(res.ok(), await res.text()).toBe(true);
	return ((await res.json()) as { id: number }).id;
}

/**
 * Adds a title, one edition and one owned copy through the API, with a cover from the app itself,
 * so nothing is asked of a catalog.
 */
async function seed(
	request: APIRequestContext,
	title: { kind: string; name: string; finished?: boolean; finished_on?: string }
): Promise<Seeded> {
	const cover = new URL('/pwa-192x192.png', test.info().project.use.baseURL).href;
	const titleID = await created(
		await request.post('/api/titles', { data: { cover_url: cover, ...title } })
	);
	const editionID = await created(
		await request.post('/api/editions', {
			data: { title_id: titleID, ...(title.kind === 'movie' ? { format: 'dvd' } : {}) }
		})
	);
	const copyID = await created(
		await request.post('/api/copies', { data: { edition_id: editionID, status: 'owned' } })
	);
	return { titleID, copyID };
}

function tag(): string {
	return `T${Date.now()}`;
}

/** A day as the title page writes it, such as "10 maj 2026". */
function shown(day: string): string {
	return new Intl.DateTimeFormat('pl-PL', { dateStyle: 'medium', timeZone: 'UTC' }).format(
		new Date(day)
	);
}

/** Today in the browser's time zone, as the page's action records it. */
function today(): string {
	const d = new Date();
	const pad = (n: number) => String(n).padStart(2, '0');
	return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

async function finishedState(request: APIRequestContext, titleID: number) {
	const res = await request.get(`/api/titles/${titleID}`);
	expect(res.ok()).toBe(true);
	const t = (await res.json()) as { finished?: boolean | null; finished_on?: string | null };
	return { finished: t.finished ?? null, finished_on: t.finished_on ?? null };
}

function tile(page: Page, name: string): Locator {
	return page
		.getByRole('list', { name: 'Egzemplarze w kolekcji' })
		.getByRole('listitem')
		.filter({ has: page.getByRole('link', { name, exact: true }) });
}

/**
 * Copies are deleted at the end, since other tests count every copy the collection lists; the
 * titles stay, as the sold copy's test leaves its own.
 */
test('a title is marked watched today, its day changed, forgotten and the mark taken back', async ({
	page,
	request
}) => {
	const name = `Do obejrzenia ${tag()}`;
	const { titleID, copyID } = await seed(request, { kind: 'movie', name });
	try {
		await page.goto(`/titles/${titleID}`);
		/** The title page's line naming the finished state. */
		const line = page.locator('.finished-line');
		await page.getByRole('button', { name: 'Oznacz jako obejrzane' }).tap();
		await expect(line).toHaveText(`Obejrzane ${shown(today())}`);
		expect(await finishedState(request, titleID)).toEqual({
			finished: true,
			finished_on: today()
		});

		const sheet = page.getByRole('dialog', { name: 'Obejrzane' });
		const day = sheet.getByRole('textbox', { name: 'Data' });
		await page.getByRole('button', { name: 'Zmień datę' }).tap();
		await expect(day).toHaveValue(today());
		await day.fill('2026-05-10');
		await sheet.getByRole('button', { name: 'Zapisz' }).tap();
		await expect(sheet).toBeHidden();
		await expect(line).toHaveText(`Obejrzane ${shown('2026-05-10')}`);

		await page.getByRole('button', { name: 'Zmień datę' }).tap();
		await day.fill('2999-01-01');
		await sheet.getByRole('button', { name: 'Zapisz' }).tap();
		await expect(day).toHaveAccessibleDescription(/Data nie może być późniejsza niż dziś\./);
		await day.fill('');
		await sheet.getByRole('button', { name: 'Zapisz' }).tap();
		await expect(sheet).toBeHidden();
		await expect(line).toHaveText('Obejrzane');
		expect(await finishedState(request, titleID)).toEqual({ finished: true, finished_on: null });

		await page.reload();
		await expect(line).toHaveText('Obejrzane');
		await page.getByRole('button', { name: 'Zmień datę' }).tap();
		await sheet.getByRole('button', { name: 'Usuń oznaczenie' }).tap();
		await expect(sheet).toBeHidden();
		await expect(page.getByRole('button', { name: 'Oznacz jako obejrzane' })).toBeVisible();
		await expect(line).toHaveCount(0);
		expect(await finishedState(request, titleID)).toEqual({ finished: false, finished_on: null });
		await expectNoSourceRequests(request);
	} finally {
		await request.delete(`/api/copies/${copyID}`);
	}
});

test('Do nadrobienia lists owned copies of titles not finished', async ({ page, request }) => {
	const t = tag();
	const read = `Przeczytana ${t}`;
	const unread = `Nieprzeczytana ${t}`;
	const done = await seed(request, {
		kind: 'book',
		name: read,
		finished: true,
		finished_on: '2026-05-10'
	});
	const pending = await seed(request, { kind: 'book', name: unread });
	try {
		const view = (await (await request.get('/api/collection?unfinished=1')).json()) as {
			items: { copy_id: number; finished: boolean }[];
		};
		const ids = view.items.map((i) => i.copy_id);
		expect(ids).toContain(pending.copyID);
		expect(ids).not.toContain(done.copyID);

		await page.goto('/');
		await expect(tile(page, read)).toBeVisible();
		await expect(tile(page, unread)).toBeVisible();
		await page.getByRole('button', { name: /^Sortowanie i filtry/ }).tap();
		const sheet = page.getByRole('dialog', { name: 'Sortowanie i filtry' });
		await sheet.getByText('Do nadrobienia', { exact: true }).tap();
		await expect(sheet.getByRole('switch', { name: 'Do nadrobienia' })).toBeChecked();
		await expect(page).toHaveURL(/\/\?unfinished=1$/);
		await sheet.getByRole('button', { name: 'Gotowe' }).tap();
		await expect(tile(page, unread)).toBeVisible();
		await expect(tile(page, read)).toHaveCount(0);

		await page.reload();
		await expect(tile(page, unread)).toBeVisible();
		await expect(tile(page, read)).toHaveCount(0);
		await page.getByRole('button', { name: 'Usuń: Do nadrobienia' }).tap();
		await expect(page).toHaveURL(/\/$/);
		await expect(tile(page, read)).toBeVisible();
		await expectNoSourceRequests(request);
	} finally {
		await request.delete(`/api/copies/${done.copyID}`);
		await request.delete(`/api/copies/${pending.copyID}`);
	}
});
