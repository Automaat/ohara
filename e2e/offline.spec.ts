import type { Locator } from '@playwright/test';
import { expect, expectNoSourceRequests, test } from './fixtures.js';

/** Whether `document.elementFromPoint` at the centre of a tab finds the tab's own link. */
function takesTapsAtCentre(tab: Locator): Promise<boolean> {
	return tab.evaluate((link) => {
		const box = link.getBoundingClientRect();
		const hit = document.elementFromPoint(box.x + box.width / 2, box.y + box.height / 2);
		return hit?.closest('a') === link;
	});
}

test('the tab bar stays usable under the offline notice (#303)', async ({
	page,
	context,
	request
}) => {
	await page.goto('/');
	await page.evaluate(async () => {
		await navigator.serviceWorker.ready;
		if (navigator.serviceWorker.controller) return;
		await new Promise((done) =>
			navigator.serviceWorker.addEventListener('controllerchange', done, { once: true })
		);
	});

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
