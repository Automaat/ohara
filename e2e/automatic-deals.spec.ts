import type { MatchedDealsView, Observation } from '../src/lib/api.gen.js';
import { expect, test } from './fixtures.js';

test('Okazje shows automatic deals and explains offers with too few comparisons', async ({
	page
}) => {
	const listing: Observation = {
		id: 1,
		source: 'olx',
		external_id: 'used-bluray',
		observed_at: new Date().toISOString(),
		price_cents: 8000,
		acquired_as: 'used',
		url: 'https://example.test/offer',
		listing_title: 'Blade Runner Blu-ray',
		title_id: null,
		title_name: 'Blade Runner',
		edition_id: null,
		format: 'bluray',
		listing_photo_url: null,
		cover_url: null,
		max_price_cents: null,
		alert: 'none',
		alert_error: '',
		alert_attempts: 0
	};
	await page.route('**/api/deals/matched?*', async (route) => {
		const res = await route.fetch();
		const view: MatchedDealsView = await res.json();
		expect(res.ok(), JSON.stringify(view)).toBe(true);
		await route.fulfill({
			response: res,
			json: {
				...view,
				wishes: 2,
				searched_wishes: 2,
				observations: [
					listing,
					{
						...listing,
						id: 2,
						external_id: 'new-dvd',
						listing_title: 'Alien DVD',
						title_name: 'Alien',
						format: 'dvd',
						acquired_as: 'new'
					}
				],
				automatic: [
					{
						observation_id: 1,
						sample_count: 5,
						typical_cents: 10000,
						discount_percent: 20,
						qualifies: true
					},
					{
						observation_id: 2,
						sample_count: 4,
						typical_cents: null,
						discount_percent: null,
						qualifies: false
					}
				]
			} satisfies MatchedDealsView
		});
	});

	await page.goto('/deals');
	const deal = page.getByRole('article').filter({ hasText: 'Blade Runner Blu-ray' });
	const insufficient = page.getByRole('article').filter({ hasText: 'Alien DVD' });
	await expect(deal).toContainText('20% poniżej typowej ceny ofertowej');
	await expect(deal).toContainText(/Typowa cena ofertowa 100\s*zł · stan używany · 5 ofert/);
	await expect(insufficient).toHaveCount(0);

	await page.getByRole('radio', { name: 'Wszystkie · 2' }).press('Space');
	await expect(insufficient).toContainText('Brak typowej ceny ofertowej');
	await expect(insufficient).toContainText('Stan nowy · 4 oferty do porównania');
});
