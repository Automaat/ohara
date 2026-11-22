import { describe, expect, it, vi } from 'vitest';
import type { MarketPrice } from './deals';
import { TitleMarkets } from './title-market.svelte';

const dvd: MarketPrice = {
	title_id: 2,
	format: 'dvd',
	cents: 4250,
	source: 'olx',
	count: 4,
	trimmed: 1
};

const answer = (...markets: MarketPrice[]) =>
	new Response(JSON.stringify({ window_days: 90, markets }), { status: 200 });

/** A market read the test answers when it chooses, to hold a load in flight. */
function held() {
	const answers: ((res: Response) => void)[] = [];
	const paths: string[] = [];
	const fetchFn: typeof fetch = (input) => {
		paths.push(String(input));
		return new Promise<Response>((resolve) => answers.push(resolve));
	};
	return {
		fetchFn,
		paths,
		asked: (n: number) => vi.waitFor(() => expect(answers).toHaveLength(n)),
		answer: (i: number, res: Response) => answers[i]?.(res)
	};
}

describe('TitleMarkets', () => {
	it("gives a wish its title and format's market price", async () => {
		const markets = new TitleMarkets(async () => answer(dvd));
		expect(markets.for(2, 'dvd')).toBeNull();
		await markets.load(2);
		expect(markets.for(2, 'dvd')).toEqual(dvd);
		expect(markets.for(2, 'bluray')).toBeNull();
		expect(markets.for(3, 'dvd')).toBeNull();
	});

	it('asks for one title once', async () => {
		const fetchFn = vi.fn<typeof fetch>(async () => answer(dvd));
		const markets = new TitleMarkets(fetchFn);
		await markets.load(2);
		await markets.load(2);
		expect(fetchFn).toHaveBeenCalledOnce();
		expect(String(fetchFn.mock.calls[0]?.[0])).toBe('/api/valuation/market?title_id=2');
	});

	it('shows nothing after a failed request and asks again next time', async () => {
		const fetchFn = vi
			.fn<typeof fetch>()
			.mockResolvedValueOnce(new Response('{"error":"boom"}', { status: 500 }))
			.mockResolvedValueOnce(answer(dvd));
		const markets = new TitleMarkets(fetchFn);
		await markets.load(2);
		expect(markets.for(2, 'dvd')).toBeNull();
		await markets.load(2);
		expect(markets.for(2, 'dvd')).toEqual(dvd);
	});

	it("drops the first answer for a title asked again after another's", async () => {
		const reads = held();
		const markets = new TitleMarkets(reads.fetchFn);
		const first = markets.load(2);
		const other = markets.load(3);
		const again = markets.load(2);
		await reads.asked(3);
		reads.answer(1, answer({ ...dvd, title_id: 3 }));
		reads.answer(2, answer({ ...dvd, cents: 6000 }));
		await Promise.all([other, again]);
		reads.answer(0, answer(dvd));
		await first;
		expect(markets.for(2, 'dvd')).toEqual({ ...dvd, cents: 6000 });
	});

	it("drops an earlier title's late answer", async () => {
		const reads = held();
		const markets = new TitleMarkets(reads.fetchFn);
		const first = markets.load(2);
		await reads.asked(1);
		const second = markets.load(3);
		await reads.asked(2);
		reads.answer(1, answer({ ...dvd, title_id: 3 }));
		await second;
		reads.answer(0, answer(dvd));
		await first;
		expect(markets.for(2, 'dvd')).toBeNull();
		expect(markets.for(3, 'dvd')).toEqual({ ...dvd, title_id: 3 });
		expect(reads.paths).toEqual([
			'/api/valuation/market?title_id=2',
			'/api/valuation/market?title_id=3'
		]);
	});
});
