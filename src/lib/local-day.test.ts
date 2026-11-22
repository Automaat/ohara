import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { LocalDay } from './local-day.svelte';

describe('LocalDay', () => {
	beforeEach(() => {
		vi.useFakeTimers();
		vi.setSystemTime(new Date(2026, 11, 14, 23, 59));
	});
	afterEach(() => {
		vi.useRealTimers();
	});

	function page() {
		return { win: new EventTarget(), doc: new EventTarget() };
	}

	it('moves on at local midnight while started', () => {
		const day = new LocalDay();
		const stop = day.start(page());
		expect(day.current).toBe('2026-12-14');
		vi.advanceTimersByTime(60_000);
		expect(day.current).toBe('2026-12-15');
		vi.advanceTimersByTime(24 * 60 * 60_000);
		expect(day.current).toBe('2026-12-16');
		stop();
		vi.advanceTimersByTime(24 * 60 * 60_000);
		expect(day.current).toBe('2026-12-16');
	});

	it.each(['pageshow', 'visibilitychange'])(
		'reads the day again on %s when the clock moved on while no timer ran',
		(event) => {
			const day = new LocalDay();
			const events = page();
			const stop = day.start(events);
			vi.setSystemTime(new Date(2026, 11, 16, 8, 0));
			expect(day.current).toBe('2026-12-14');
			(event === 'pageshow' ? events.win : events.doc).dispatchEvent(new Event(event));
			expect(day.current).toBe('2026-12-16');
			stop();
		}
	);

	it('shows the day it waits out when the clock passes midnight during a refresh', () => {
		const day = new LocalDay();
		const events = page();
		const stop = day.start(events);
		const readings = [new Date(2026, 11, 14, 23, 59, 59, 999), new Date(2026, 11, 15, 0, 0)];
		const RealDate = Date;
		class SteppingDate extends RealDate {
			constructor(...args: ConstructorParameters<typeof Date>) {
				if (args.length > 0) super(...args);
				else super((readings.length > 1 ? readings.shift() : readings[0]) ?? 0);
			}
		}
		vi.stubGlobal('Date', SteppingDate);
		try {
			events.doc.dispatchEvent(new Event('visibilitychange'));
			expect(day.current).toBe('2026-12-14');
			vi.advanceTimersByTime(1);
			expect(day.current).toBe('2026-12-15');
		} finally {
			vi.unstubAllGlobals();
			stop();
		}
	});

	it('stops listening once stopped', () => {
		const day = new LocalDay();
		const events = page();
		day.start(events)();
		vi.setSystemTime(new Date(2026, 11, 20, 8, 0));
		events.win.dispatchEvent(new Event('pageshow'));
		events.doc.dispatchEvent(new Event('visibilitychange'));
		expect(day.current).toBe('2026-12-14');
	});
});
