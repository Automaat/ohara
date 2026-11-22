import { render } from 'svelte/server';
import { afterEach, describe, expect, it, vi } from 'vitest';
import ScanItem from './ScanItem.svelte';
import { ScanFlow, announceSettle } from '#lib/scan-flow.svelte.js';

const code = '5901234123457';

/** A fetch that answers a code whose only candidate waits for a pick. */
const fetchFn: typeof fetch = async (input) => {
	const url = String(input);
	const body = url.startsWith('/api/editions')
		? []
		: {
				barcode: code,
				candidates: [
					{
						type: 'book',
						title: { name: 'Lalka', authors: ['Bolesław Prus'] },
						edition: { barcode: code, binding: 'hardcover' },
						provider: 'bn',
						confidence: 0.5
					}
				],
				needs_confirmation: true,
				errors: []
			};
	return new Response(JSON.stringify(body), { status: 200 });
};

/** The text of the sheet's polite status, or null without one. */
function status(html: string): string | null {
	const bare = html.replaceAll(/<!--[^]*?-->/g, '');
	return /<span[^>]*role="status"[^>]*>([^<]*)<\/span>/.exec(bare)?.[1] ?? null;
}

afterEach(() => {
	vi.useRealTimers();
});

describe('ScanItem', () => {
	it('reads a resolved scan from the sheet status', async () => {
		vi.useFakeTimers();
		const flow = new ScanFlow(fetchFn, undefined);
		await flow.load(code);
		vi.advanceTimersByTime(announceSettle);
		const props = { flow, onadded: () => {}, onclose: () => {}, onmanual: () => {} };
		const { body } = render(ScanItem, { props });
		expect(status(body)).toBe('Znaleziono propozycję. Bolesław Prus – Lalka');
	});
});
