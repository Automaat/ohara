import type { ToastEnd, ToastOptions } from '@smykla-skalski/sui';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { AddToasts, addToastMs } from './add-toast';
import { ApiError } from './api';
import type { SessionAdd } from './session.svelte';

/** A toast host that keeps its toasts in a list, as SUI's Toaster does. */
function host() {
	const shown = new Map<number, ToastOptions>();
	const ended: [string, ToastEnd][] = [];
	const held = new Set<number>();
	const screen = { single: false, listing: false };
	let last = 0;
	const dismiss = (id: number, reason: ToastEnd) => {
		const t = shown.get(id);
		if (!t) return;
		shown.delete(id);
		ended.push([t.title, reason]);
		t.ondismiss?.(reason);
	};
	return {
		shown,
		ended,
		held,
		screen,
		toast(options: ToastOptions) {
			shown.set(++last, options);
			return last;
		},
		dismiss,
		holding: (id: number) => held.has(id),
		single: () => screen.single,
		listing: () => screen.listing,
		/** Runs a toast's action the way the Toaster does: close it, then act. */
		act(id: number) {
			const t = shown.get(id);
			dismiss(id, 'acted');
			t?.action?.run();
		},
		titles: () => [...shown.values()].map((t) => t.title),
		descriptions: () => [...shown.values()].map((t) => t.description),
		idOf: (title: string) => [...shown].find(([, t]) => t.title === title)?.[0] ?? -1
	};
}

const dune: SessionAdd = {
	id: 1,
	copyID: 40,
	editionID: 21,
	titleID: 9,
	createdEdition: true,
	createdTitle: true,
	name: 'Diuna: Część druga',
	medium: '4K UHD',
	kind: 'movie',
	status: 'owned',
	priceCents: 5000,
	recentID: 1
};

beforeEach(() => {
	vi.useFakeTimers();
});

afterEach(() => {
	vi.useRealTimers();
});

describe('AddToasts', () => {
	it('names the add and offers Cofnij for six seconds', () => {
		const h = host();
		const toasts = new AddToasts(h, async () => {});
		toasts.show(dune);
		expect([...h.shown.values()]).toMatchObject([
			{
				title: 'Dodano do kolekcji',
				description: 'Diuna: Część druga · 4K UHD',
				action: { label: 'Cofnij' }
			}
		]);
		vi.advanceTimersByTime(addToastMs - 250);
		expect(h.shown.size).toBe(1);
		vi.advanceTimersByTime(250);
		expect(h.ended).toEqual([['Dodano do kolekcji', 'expired']]);
	});

	it('says a wish went to the wishlist', () => {
		const h = host();
		new AddToasts(h, async () => {}).show({ ...dune, status: 'wishlist', priceCents: null });
		expect(h.titles()).toEqual(['Dodano do listy życzeń']);
	});

	it('waits while the reader is on the toast and restarts in full after', () => {
		const h = host();
		new AddToasts(h, async () => {}).show(dune);
		vi.advanceTimersByTime(5000);
		h.held.add(1);
		vi.advanceTimersByTime(20_000);
		expect(h.shown.size).toBe(1);
		h.held.clear();
		vi.advanceTimersByTime(addToastMs - 250);
		expect(h.shown.size).toBe(1);
		vi.advanceTimersByTime(250);
		expect(h.shown.size).toBe(0);
	});

	it('keeps each add toast for its own six seconds', () => {
		const h = host();
		const toasts = new AddToasts(h, async () => {});
		toasts.show(dune);
		vi.advanceTimersByTime(1000);
		toasts.show({ ...dune, id: 2, name: 'Solaris', medium: '' });
		expect([...h.shown.values()].map((t) => t.description)).toEqual([
			'Diuna: Część druga · 4K UHD',
			'Solaris'
		]);
		vi.advanceTimersByTime(addToastMs - 1000);
		expect([...h.shown.values()].map((t) => t.description)).toEqual(['Solaris']);
		vi.advanceTimersByTime(1000);
		expect(h.shown.size).toBe(0);
		expect(h.ended).toEqual([
			['Dodano do kolekcji', 'expired'],
			['Dodano do kolekcji', 'expired']
		]);
	});

	it('undoes the add from Cofnij and says so', async () => {
		const h = host();
		const undo = vi.fn(async () => {});
		new AddToasts(h, undo).show(dune);
		h.act(1);
		await vi.runAllTimersAsync();
		expect(undo).toHaveBeenCalledWith(dune);
		expect(h.titles()).toEqual(['Cofnięto dodanie']);
	});

	it('reports a failed undo and tries again from the failure', async () => {
		const h = host();
		const undo = vi
			.fn<(add: SessionAdd) => Promise<void>>()
			.mockRejectedValueOnce(new ApiError('Serwer nie odpowiada.', 0))
			.mockResolvedValueOnce(undefined);
		new AddToasts(h, undo).show(dune);
		h.act(1);
		await vi.runAllTimersAsync();
		const failure = [...h.shown.values()][0];
		expect(failure).toMatchObject({
			title: 'Nie udało się cofnąć: Diuna: Część druga · 4K UHD',
			description: 'Serwer nie odpowiada.',
			tone: 'danger',
			action: { label: 'Spróbuj ponownie' }
		});
		h.act(h.idOf(failure.title));
		await vi.runAllTimersAsync();
		expect(undo).toHaveBeenCalledTimes(2);
		expect(h.titles()).toEqual(['Cofnięto dodanie']);
	});

	it('takes every toast back when the page goes, and raises none after', async () => {
		const h = host();
		let finish: () => void = () => {};
		const toasts = new AddToasts(h, () => new Promise<void>((resolve) => (finish = resolve)));
		toasts.show(dune);
		h.act(1);
		toasts.show({ ...dune, id: 2 });
		toasts.destroy();
		expect(h.shown.size).toBe(0);
		finish();
		await vi.runAllTimersAsync();
		toasts.show(dune);
		expect(h.shown.size).toBe(0);
	});
});

describe('AddToasts where one toast fits', () => {
	const solaris: SessionAdd = { ...dune, id: 2, copyID: 41, name: 'Solaris', medium: '' };

	function single() {
		const h = host();
		h.screen.single = true;
		return h;
	}

	it('shows only the newest add and lists the one before for the rest of its window', () => {
		const h = single();
		const toasts = new AddToasts(h, async () => {});
		toasts.show(dune);
		vi.advanceTimersByTime(1000);
		toasts.show(solaris);
		expect(h.descriptions()).toEqual(['Solaris']);
		expect(h.ended).toEqual([['Dodano do kolekcji', 'withdrawn']]);
		expect([...toasts.listed]).toEqual([1]);
		vi.advanceTimersByTime(addToastMs - 1000 - 250);
		expect([...toasts.listed]).toEqual([1]);
		vi.advanceTimersByTime(250);
		expect([...toasts.listed]).toEqual([]);
		expect(h.descriptions()).toEqual(['Solaris']);
		vi.advanceTimersByTime(1000);
		expect(h.shown.size).toBe(0);
	});

	it('undoes a listed add from the list and says so once the newest add toast leaves', async () => {
		const h = single();
		const undo = vi.fn(async () => {});
		const toasts = new AddToasts(h, undo);
		toasts.show(dune);
		toasts.show(solaris);
		toasts.undo(dune);
		await vi.advanceTimersByTimeAsync(0);
		expect(undo).toHaveBeenCalledWith(dune);
		expect([...toasts.listed]).toEqual([]);
		expect(h.descriptions()).toEqual(['Solaris']);
		await vi.advanceTimersByTimeAsync(addToastMs);
		expect(h.titles()).toEqual(['Cofnięto dodanie']);
	});

	it('keeps the newest add toast up when an earlier undo answers late', async () => {
		const h = single();
		let finish: () => void = () => {};
		const toasts = new AddToasts(h, () => new Promise<void>((resolve) => (finish = resolve)));
		toasts.show(dune);
		h.act(1);
		toasts.show(solaris);
		finish();
		await vi.advanceTimersByTimeAsync(0);
		expect(h.descriptions()).toEqual(['Solaris']);
		expect(h.shown.get(2)?.action?.label).toBe('Cofnij');
		h.dismiss(2, 'dismissed');
		expect(h.titles()).toEqual(['Cofnięto dodanie']);
	});

	it('keeps a listed add while the list is open, then restarts its window in full', () => {
		const h = single();
		const toasts = new AddToasts(h, async () => {});
		toasts.show(dune);
		toasts.show(solaris);
		h.screen.listing = true;
		vi.advanceTimersByTime(20_000);
		expect([...toasts.listed]).toEqual([1]);
		h.screen.listing = false;
		vi.advanceTimersByTime(addToastMs - 250);
		expect([...toasts.listed]).toEqual([1]);
		vi.advanceTimersByTime(250);
		expect([...toasts.listed]).toEqual([]);
	});

	it('takes no Cofnij from the list for an add that is not listed', async () => {
		const h = single();
		const undo = vi.fn(async () => {});
		const toasts = new AddToasts(h, undo);
		toasts.show(dune);
		toasts.undo(dune);
		toasts.show(solaris);
		vi.advanceTimersByTime(addToastMs);
		toasts.undo(dune);
		await vi.advanceTimersByTimeAsync(0);
		expect(undo).not.toHaveBeenCalled();
	});

	it('leaves out of the list an add whose toast the reader closed', () => {
		const h = single();
		const toasts = new AddToasts(h, async () => {});
		toasts.show(dune);
		h.dismiss(1, 'dismissed');
		toasts.show(solaris);
		expect([...toasts.listed]).toEqual([]);
	});

	it('puts a failed undo back in line behind the newest add toast', async () => {
		const h = single();
		const undo = vi
			.fn<(add: SessionAdd) => Promise<void>>()
			.mockRejectedValueOnce(new ApiError('Serwer nie odpowiada.', 0))
			.mockResolvedValueOnce(undefined);
		const toasts = new AddToasts(h, undo);
		toasts.show(dune);
		h.act(1);
		await vi.advanceTimersByTimeAsync(0);
		const failure = 'Nie udało się cofnąć: Diuna: Część druga · 4K UHD';
		expect(h.titles()).toEqual([failure]);
		toasts.show(solaris);
		expect(h.descriptions()).toEqual(['Solaris']);
		expect([...toasts.listed]).toEqual([]);
		await vi.advanceTimersByTimeAsync(addToastMs);
		expect(h.titles()).toEqual([failure]);
		h.act(h.idOf(failure));
		await vi.advanceTimersByTimeAsync(0);
		expect(undo).toHaveBeenCalledTimes(2);
		expect(h.titles()).toEqual(['Cofnięto dodanie']);
	});

	it('leaves only the newest add toast up when the screen turns too small', async () => {
		const h = host();
		const toasts = new AddToasts(h, async () => {});
		toasts.show(dune);
		h.act(1);
		await vi.advanceTimersByTimeAsync(0);
		toasts.show(solaris);
		toasts.show({ ...solaris, id: 3, name: 'Stalker' });
		h.screen.single = true;
		toasts.fit(true);
		expect(h.descriptions()).toEqual(['Stalker']);
		expect([...toasts.listed]).toEqual([2]);
		h.dismiss(h.idOf('Dodano do kolekcji'), 'dismissed');
		expect(h.titles()).toEqual(['Cofnięto dodanie']);
	});

	it('shows every waiting toast once the screen has room again', async () => {
		const h = single();
		let finish: () => void = () => {};
		const toasts = new AddToasts(h, () => new Promise<void>((resolve) => (finish = resolve)));
		toasts.show(dune);
		h.act(1);
		toasts.show(solaris);
		finish();
		await vi.advanceTimersByTimeAsync(0);
		h.screen.single = false;
		toasts.fit(false);
		expect(h.titles()).toEqual(['Dodano do kolekcji', 'Cofnięto dodanie']);
	});

	it('forgets the list when the page goes', () => {
		const h = single();
		const toasts = new AddToasts(h, async () => {});
		toasts.show(dune);
		toasts.show(solaris);
		toasts.destroy();
		expect(h.shown.size).toBe(0);
		expect([...toasts.listed]).toEqual([]);
		expect(vi.getTimerCount()).toBe(0);
	});
});
