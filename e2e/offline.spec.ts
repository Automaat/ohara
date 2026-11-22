import type { Locator, Page } from '@playwright/test';
import { expect, expectNoSourceRequests, test, uniqueEAN } from './fixtures.js';

/** Whether `document.elementFromPoint` at the centre of a link or button finds that control. */
function takesTapsAtCentre(control: Locator): Promise<boolean> {
	return control.evaluate((el) => {
		const box = el.getBoundingClientRect();
		const hit = document.elementFromPoint(box.x + box.width / 2, box.y + box.height / 2);
		return hit?.closest('a, button') === el;
	});
}

/** Opens `path` once the service worker controls the page, so it also opens offline. */
async function openControlled(page: Page, path: string): Promise<void> {
	await page.goto(path);
	await page.evaluate(async () => {
		await navigator.serviceWorker.ready;
		if (navigator.serviceWorker.controller) return;
		await new Promise((done) =>
			navigator.serviceWorker.addEventListener('controllerchange', done, { once: true })
		);
	});
}

test('the tab bar stays usable under the offline notice (#303)', async ({
	page,
	context,
	request
}) => {
	await openControlled(page, '/');

	await context.setOffline(true);
	try {
		await page.goto('/');

		const notice = page.getByRole('complementary', { name: 'Brak połączenia' });
		await expect(notice).toBeVisible();
		await expect(notice.getByRole('button', { name: 'Zamknij' })).toBeVisible();
		await expect(page.getByRole('status').filter({ hasText: 'Brak połączenia' })).toHaveCount(1);

		const tabs = page.getByRole('navigation', { name: 'Główne strony' }).getByRole('link');
		await expect(tabs).toHaveCount(4);
		await Promise.all(
			(await tabs.all()).map(async (tab) => {
				const href = await tab.getAttribute('href');
				await expect
					.poll(() => takesTapsAtCentre(tab), `the ${href} tab takes its taps`)
					.toBe(true);
			})
		);

		await page.getByRole('link', { name: 'Skanuj' }).tap();
		await expect(page).toHaveURL(/\/scan$/);
		await expect(notice).toBeVisible();
	} finally {
		await context.setOffline(false);
	}

	await expectNoSourceRequests(request);
});

test('the back-online notice leaves the queue undo its taps', async ({
	page,
	context,
	request
}) => {
	const code = uniqueEAN();
	const added = await request.post('/api/scan-queue', {
		data: { barcode: code, reason: 'not_found', detail: '' }
	});
	expect(added.ok()).toBe(true);

	await openControlled(page, '/scan/queue');
	await page.getByRole('button', { name: `Więcej działań dla ${code}` }).click();
	await page.getByRole('menuitem', { name: 'Usuń' }).click();
	const undo = page
		.getByRole('region', { name: 'Powiadomienia' })
		.getByRole('button', { name: 'Cofnij' });
	await expect(undo).toBeVisible();

	await context.setOffline(true);
	try {
		await expect(page.getByRole('complementary', { name: 'Brak połączenia' })).toBeVisible();
	} finally {
		await context.setOffline(false);
	}
	await expect(page.getByRole('complementary', { name: 'Połączenie wróciło' })).toBeVisible();

	await expect.poll(() => takesTapsAtCentre(undo), 'Cofnij takes its taps').toBe(true);
	await undo.tap();
	await expect(page.getByRole('list', { name: 'Kody do uzupełnienia' })).toContainText(code);

	await expectNoSourceRequests(request);
});

test('the offline notice stays on a short screen', async ({ page, context, request }) => {
	await page.setViewportSize({ width: 390, height: 180 });
	await openControlled(page, '/');

	await context.setOffline(true);
	try {
		await page.goto('/');
		const title = page
			.getByRole('complementary', { name: 'Brak połączenia' })
			.getByText('Brak połączenia', { exact: true });
		await expect(title).toBeInViewport({ ratio: 1 });
	} finally {
		await context.setOffline(false);
	}

	await expectNoSourceRequests(request);
});
