import type { ToastEnd, ToastOptions } from '@smykla-skalski/sui';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from './api';
import {
	QueueRemovals,
	swipeAxis,
	swipeOffset,
	swipeOpens,
	swipeWidth,
	undoWindowMs
} from './queue.svelte';
import type { QueueEntry } from './scan';

function entry(id: number, barcode = `590123412345${id}`): QueueEntry {
	return {
		id,
		barcode,
		reason: 'not_found',
		detail: '',
		scans: 2,
		created_at: '2026-11-18T10:00:00Z',
		last_scanned_at: '2026-11-19T08:00:00Z'
	};
}

/** A page with SUI's toast rules: a toast's action runs right after it closes with `acted`. */
function page(send: (entry: QueueEntry) => Promise<void> = async () => {}) {
	const toasts = new Map<number, ToastOptions>();
	const sent: number[] = [];
	const removed: number[] = [];
	const ended: ToastEnd[] = [];
	let last = 0;
	let held = false;
	const dismiss = (id: number, reason: ToastEnd) => {
		const shown = toasts.get(id);
		if (!shown) return;
		toasts.delete(id);
		ended.push(reason);
		shown.ondismiss?.(reason);
	};
	const removals = new QueueRemovals({
		toast: (options) => {
			toasts.set(++last, options);
			return last;
		},
		dismiss,
		holding: () => held,
		send: (e) => {
			sent.push(e.id);
			return send(e);
		},
		removed: (e) => removed.push(e.id)
	});
	return {
		removals,
		toasts,
		sent,
		removed,
		ended,
		hold: (on: boolean) => (held = on),
		/** Presses the newest toast's action, as SUI's Toaster does. */
		undo: () => {
			const [id, shown] = [...toasts].at(-1) ?? [];
			if (id === undefined || !shown?.action) throw new Error('no toast with an action');
			dismiss(id, 'acted');
			shown.action.run();
		},
		/** Presses the newest toast's Dismiss. */
		close: () => {
			const id = [...toasts.keys()].at(-1);
			if (id !== undefined) dismiss(id, 'dismissed');
		}
	};
}

describe('swipe', () => {
	it('waits for the touch to pass the slop before it picks a direction', () => {
		expect(swipeAxis(5, -7)).toBeNull();
		expect(swipeAxis(-12, 4)).toBe('x');
		expect(swipeAxis(6, 15)).toBe('y');
		expect(swipeAxis(-10, -10)).toBe('y');
	});

	it('keeps the face between shut and open', () => {
		expect(swipeOffset(0, -40)).toBe(-40);
		expect(swipeOffset(0, -300)).toBe(-swipeWidth);
		expect(swipeOffset(0, 50)).toBe(0);
		expect(swipeOffset(-swipeWidth, 30)).toBe(-swipeWidth + 30);
	});

	it('opens a row dragged past half of "Usuń"', () => {
		expect(swipeOpens(-swipeWidth)).toBe(true);
		expect(swipeOpens(-swipeWidth / 2)).toBe(true);
		expect(swipeOpens(-swipeWidth / 2 + 1)).toBe(false);
		expect(swipeOpens(0)).toBe(false);
	});
});

describe('QueueRemovals', () => {
	beforeEach(() => {
		vi.useFakeTimers();
	});

	afterEach(() => {
		vi.useRealTimers();
	});

	it('hides a removed entry at once and offers Cofnij without asking the server', () => {
		const p = page();
		p.removals.remove(entry(1, '5901234123457'));
		expect(p.removals.hidden.has(1)).toBe(true);
		const [shown] = [...p.toasts.values()];
		expect(shown?.title).toBe('Usunięto kod 5901234123457');
		expect(shown?.action?.label).toBe('Cofnij');
		expect(p.sent).toEqual([]);
	});

	it('brings the entry back on Cofnij and never sends the removal', async () => {
		const p = page();
		p.removals.remove(entry(1));
		p.undo();
		await vi.runAllTimersAsync();
		expect(p.removals.hidden.has(1)).toBe(false);
		expect(p.sent).toEqual([]);
		expect(p.removed).toEqual([]);
		expect(p.toasts.size).toBe(0);
	});

	it('sends the removal when the undo window runs out', async () => {
		const p = page();
		p.removals.remove(entry(1));
		await vi.advanceTimersByTimeAsync(undoWindowMs - 1);
		expect(p.sent).toEqual([]);
		await vi.advanceTimersByTimeAsync(1);
		expect(p.ended).toEqual(['expired']);
		expect(p.sent).toEqual([1]);
		expect(p.removed).toEqual([1]);
		expect(p.removals.hidden.has(1)).toBe(false);
	});

	it('sends the removal when the toast is dismissed', async () => {
		const p = page();
		p.removals.remove(entry(1));
		p.close();
		await vi.advanceTimersByTimeAsync(0);
		expect(p.sent).toEqual([1]);
		expect(p.removed).toEqual([1]);
		await vi.runAllTimersAsync();
		expect(p.sent).toEqual([1]);
	});

	it('keeps the toast while the reader holds it', async () => {
		const p = page();
		p.removals.remove(entry(1));
		p.hold(true);
		await vi.advanceTimersByTimeAsync(undoWindowMs * 3);
		expect(p.sent).toEqual([]);
		expect(p.toasts.size).toBe(1);
		p.hold(false);
		await vi.advanceTimersByTimeAsync(undoWindowMs);
		expect(p.sent).toEqual([1]);
	});

	it('ignores a second removal of an entry that is still waiting', () => {
		const p = page();
		p.removals.remove(entry(1));
		p.removals.remove(entry(1));
		expect(p.toasts.size).toBe(1);
	});

	it('undoes only the entry whose toast offered Cofnij', async () => {
		const p = page();
		p.removals.remove(entry(1));
		p.removals.remove(entry(2));
		p.undo();
		expect([...p.removals.hidden]).toEqual([1]);
		await vi.advanceTimersByTimeAsync(undoWindowMs);
		expect(p.sent).toEqual([1]);
	});

	it('sends every waiting removal when the page goes away', async () => {
		const p = page();
		p.removals.remove(entry(1));
		p.removals.remove(entry(2));
		p.removals.flush();
		expect(p.sent).toEqual([1, 2]);
		expect(p.ended).toEqual(['withdrawn', 'withdrawn']);
		await vi.advanceTimersByTimeAsync(0);
		expect(p.removed).toEqual([1, 2]);
		await vi.runAllTimersAsync();
		expect(p.sent).toEqual([1, 2]);
	});

	it('treats an entry the server no longer has as removed', async () => {
		const p = page(async () => {
			throw new ApiError('Nie znaleziono.', 404);
		});
		p.removals.remove(entry(1));
		p.close();
		await vi.advanceTimersByTimeAsync(0);
		expect(p.removed).toEqual([1]);
		expect(p.toasts.size).toBe(0);
	});

	it('shows the entry again with the failure when the server refuses', async () => {
		const p = page(async () => {
			throw new ApiError('Serwer zwrócił błąd 500.', 500);
		});
		p.removals.remove(entry(1, '5901234123457'));
		p.close();
		await vi.advanceTimersByTimeAsync(0);
		expect(p.removed).toEqual([]);
		expect(p.removals.hidden.has(1)).toBe(false);
		const [failure] = [...p.toasts.values()];
		expect(failure).toMatchObject({
			title: 'Nie udało się usunąć kodu 5901234123457',
			description: 'Serwer zwrócił błąd 500.',
			tone: 'danger'
		});
	});

	it('sends waiting removals on destroy and stays quiet about later failures', async () => {
		const p = page(async () => {
			throw new ApiError('Serwer nie odpowiada.', 0);
		});
		p.removals.remove(entry(1));
		p.removals.destroy();
		await vi.advanceTimersByTimeAsync(0);
		expect(p.sent).toEqual([1]);
		expect(p.toasts.size).toBe(0);
		expect(p.removals.hidden.has(1)).toBe(false);
	});
});
