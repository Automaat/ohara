import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { monthCheckEvery, StatsMonth } from './stats-month.svelte';
import { lastMonths } from './stats';

const lastMinuteOfFebruaryInPoland = new Date('2026-02-28T22:59:00Z');

describe('StatsMonth', () => {
	beforeEach(() => {
		vi.useFakeTimers();
		vi.setSystemTime(lastMinuteOfFebruaryInPoland);
	});
	afterEach(() => {
		vi.useRealTimers();
	});

	function page() {
		return { win: new EventTarget(), doc: new EventTarget() };
	}

	it('moves on to the next month after midnight in Poland while started', () => {
		const month = new StatsMonth();
		const stop = month.start(page());
		expect(lastMonths(month.now).at(-1)).toBe('2026-02');
		vi.advanceTimersByTime(monthCheckEvery);
		expect(lastMonths(month.now).at(-1)).toBe('2026-03');
		stop();
		vi.setSystemTime(new Date('2026-04-15T12:00:00Z'));
		vi.advanceTimersByTime(monthCheckEvery);
		expect(lastMonths(month.now).at(-1)).toBe('2026-03');
	});

	it('keeps its instant within a month', () => {
		vi.setSystemTime(new Date('2026-02-10T12:00:00Z'));
		const month = new StatsMonth();
		const first = month.now;
		const stop = month.start(page());
		vi.advanceTimersByTime(monthCheckEvery * 10);
		expect(month.now).toBe(first);
		stop();
	});

	it.each(['pageshow', 'visibilitychange'])(
		'reads the month again on %s when no timer ran',
		(event) => {
			const month = new StatsMonth();
			const events = page();
			const stop = month.start(events);
			vi.setSystemTime(new Date('2026-05-02T08:00:00Z'));
			expect(lastMonths(month.now).at(-1)).toBe('2026-02');
			(event === 'pageshow' ? events.win : events.doc).dispatchEvent(new Event(event));
			expect(lastMonths(month.now).at(-1)).toBe('2026-05');
			stop();
		}
	);
});
