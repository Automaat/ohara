import { expect, test as base, type APIRequestContext } from '@playwright/test';

/**
 * The smoke tests, each failing when the browser asked anything of another origin than the app.
 * Their API client follows no redirect, so it never reaches another origin either. The server side
 * is covered by e2e/compose.yaml, which leaves the app no way out, and by `expectNoSourceRequests`.
 */
export const test = base.extend<{ sameOrigin: void }>({
	request: async ({ playwright, baseURL }, use) => {
		const request = await playwright.request.newContext({ baseURL, maxRedirects: 0 });
		await use(request);
		await request.dispose();
	},
	sameOrigin: [
		async ({ context, baseURL }, use) => {
			const app = new URL(baseURL ?? '').origin;
			const foreign: string[] = [];
			context.on('request', (req) => {
				const url = new URL(req.url());
				if (/^(https?|wss?):$/.test(url.protocol) && url.origin !== app) foreign.push(req.url());
			});
			await use();
			expect(foreign, 'requests to another origin than the app').toEqual([]);
		},
		{ auto: true }
	]
});

export { expect };

/** The fields of a `GET /api/collection` item the tests read. */
export type Item = {
	copy_id: number;
	edition_id: number;
	title_id: number;
	name: string;
	kind: string;
	format: string | null;
	barcode: string | null;
};

/** Sums every sample of one metric in a Prometheus text exposition, per label set. */
export function samples(text: string, metric: string): Map<string, number> {
	const out = new Map<string, number>();
	for (const line of text.split('\n')) {
		const m = line.match(/^([a-z_]+)(\{[^}]*\})? (\S+)$/);
		if (m && m[1] === metric) out.set(m[2] ?? '', Number(m[3]));
	}
	return out;
}

/**
 * Checks on /metrics that the server sent no request to an outside source (book, movie, music,
 * deal or valuation) and asked no lookup provider but the stub.
 */
export async function expectNoSourceRequests(request: APIRequestContext) {
	const res = await request.get('/metrics');
	expect(res.ok()).toBe(true);
	const text = await res.text();
	const sources = samples(text, 'ohara_source_requests_total');
	expect(sources.size, 'source counters exist').toBeGreaterThan(0);
	for (const [labels, n] of sources) expect(n, `ohara_source_requests_total${labels}`).toBe(0);
	for (const [labels, n] of samples(text, 'ohara_lookup_provider_requests_total')) {
		if (!labels.includes('provider="stub"')) {
			expect(n, `ohara_lookup_provider_requests_total${labels}`).toBe(0);
		}
	}
}

/** A valid EAN-13 that is no ISBN, unique to this run, so the stub answers it as a DVD. */
export function uniqueEAN(): string {
	const body = `590${String(Date.now()).slice(-9)}`;
	let sum = 0;
	for (let i = 0; i < 12; i++) sum += Number(body[i]) * (i % 2 === 0 ? 1 : 3);
	return body + String((10 - (sum % 10)) % 10);
}
