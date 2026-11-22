import { describe, expect, it } from 'vitest';
import { RatingSaver, type RatingView } from './title-rating';

type Call = {
	rating: number | null;
	resolve: (stored: number | null) => void;
	reject: (e: Error) => void;
};

/** A saver whose requests the test answers one by one, recording what the page was shown. */
function harness(stored: number | null) {
	const calls: Call[] = [];
	const views: RatingView[] = [];
	const saver = new RatingSaver(
		7,
		stored,
		(_, rating) =>
			new Promise<number | null>((resolve, reject) => calls.push({ rating, resolve, reject })),
		(view) => views.push(view)
	);
	const settled = () => new Promise((r) => setTimeout(r, 0));
	return { saver, calls, views, settled, last: () => views.at(-1) };
}

describe('RatingSaver', () => {
	it('shows a pick at once and the stored rating once saved', async () => {
		const h = harness(4);
		const done = h.saver.pick(8);
		expect(h.last()).toEqual({ rating: 8, failed: null });
		await h.settled();
		h.calls[0]?.resolve(8);
		await done;
		expect(h.last()).toEqual({ rating: 8, failed: null });
	});

	it('sends picks one after another, in the order picked', async () => {
		const h = harness(null);
		h.saver.pick(7);
		const done = h.saver.pick(8);
		await h.settled();
		expect(h.calls.map((c) => c.rating)).toEqual([7]);
		h.calls[0]?.resolve(7);
		await h.settled();
		expect(h.calls.map((c) => c.rating)).toEqual([7, 8]);
		expect(h.last()?.rating).toBe(8);
		h.calls[1]?.resolve(8);
		await done;
		expect(h.last()).toEqual({ rating: 8, failed: null });
	});

	it('keeps quiet about an earlier pick that failed once a later one is stored', async () => {
		const h = harness(4);
		h.saver.pick(7);
		const done = h.saver.pick(8);
		await h.settled();
		h.calls[0]?.reject(new Error('503'));
		await h.settled();
		h.calls[1]?.resolve(8);
		await done;
		expect(h.views.some((v) => v.failed !== null)).toBe(false);
		expect(h.last()).toEqual({ rating: 8, failed: null });
	});

	it('goes back to the confirmed rating when the latest pick fails', async () => {
		const h = harness(4);
		const done = h.saver.pick(8);
		await h.settled();
		const offline = new Error('offline');
		h.calls[0]?.reject(offline);
		await done;
		expect(h.last()).toEqual({ rating: 4, failed: offline });
	});

	it('goes back to an earlier stored pick when a later one fails', async () => {
		const h = harness(null);
		h.saver.pick(6);
		const done = h.saver.pick(9);
		await h.settled();
		h.calls[0]?.resolve(6);
		await h.settled();
		h.calls[1]?.reject(new Error('503'));
		await done;
		expect(h.last()?.rating).toBe(6);
	});

	it('ignores a read that started before a pick or while one was on its way', async () => {
		const h = harness(4);
		const before = h.saver.mark();
		const done = h.saver.pick(8);
		const during = h.saver.mark();
		await h.settled();
		h.calls[0]?.resolve(8);
		await done;
		expect(h.saver.loaded(4, before)).toBe(8);
		expect(h.saver.loaded(4, during)).toBe(8);
		expect(h.last()?.rating).toBe(8);
	});

	it('takes the rating of a read that started after the picks were stored', async () => {
		const h = harness(4);
		const done = h.saver.pick(8);
		await h.settled();
		h.calls[0]?.resolve(8);
		await done;
		expect(h.saver.loaded(3, h.saver.mark())).toBe(3);
		const offline = new Error('offline');
		const failing = h.saver.pick(9);
		await h.settled();
		h.calls[1]?.reject(offline);
		await failing;
		expect(h.last()).toEqual({ rating: 3, failed: offline });
	});
});
