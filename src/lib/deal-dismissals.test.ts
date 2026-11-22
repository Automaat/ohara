import type { ToastEnd, ToastOptions } from '@smykla-skalski/sui';
import { describe, expect, it, vi } from 'vitest';
import { ApiError } from './api';
import { DealDismissals, type DismissalHost } from './deal-dismissals.svelte';

type Raised = ToastOptions & { id: number; closed?: ToastEnd };

function host(send: DismissalHost['send'] = async () => undefined) {
	const toasts: Raised[] = [];
	const h = {
		toasts,
		send: vi.fn(send),
		forget: vi.fn(),
		reload: vi.fn(async () => undefined),
		toast: (options: ToastOptions) => {
			const raised = { ...options, id: toasts.length + 1 };
			toasts.push(raised);
			return raised.id;
		},
		dismiss: (id: number, reason: ToastEnd) => {
			const raised = toasts.find((t) => t.id === id);
			if (!raised || raised.closed) return;
			raised.closed = reason;
			raised.ondismiss?.(reason);
		},
		/** Presses the toast's action the way the toaster does: it closes, then runs. */
		act: async (raised: Raised | undefined) => {
			if (!raised?.action) throw new Error('no action');
			h.dismiss(raised.id, 'acted');
			raised.action.run();
			await vi.waitFor(() => expect(h.reload).toHaveBeenCalled());
		}
	};
	return h;
}

const deal = { id: 7, listing_title: 'Blade Runner Blu-ray' };
const unavailable = () => new ApiError('Usługa niedostępna', 503);

describe('DealDismissals', () => {
	it('hides the listing before the server answers, then offers "Cofnij"', async () => {
		const answer = Promise.withResolvers<undefined>();
		const h = host(() => answer.promise);
		const dismissals = new DealDismissals(h);
		const done = dismissals.dismiss(deal);
		expect(dismissals.hidden.has(7)).toBe(true);
		expect(h.forget).toHaveBeenCalledWith(7);
		answer.resolve(undefined);
		await done;
		expect(h.send).toHaveBeenCalledWith('POST', 7);
		expect(h.toasts.map((t) => [t.title, t.action?.label])).toEqual([
			['Zignorowano ofertę', 'Cofnij']
		]);
		expect(h.reload).toHaveBeenCalledTimes(1);
	});

	it('drops badge answers again once the dismissal is stored', async () => {
		const h = host();
		await new DealDismissals(h).dismiss(deal);
		expect(h.forget).toHaveBeenCalledTimes(2);
	});

	it('brings the listing back on "Cofnij"', async () => {
		const h = host();
		const dismissals = new DealDismissals(h);
		await dismissals.dismiss(deal);
		h.reload.mockClear();
		await h.act(h.toasts[0]);
		expect(h.send).toHaveBeenLastCalledWith('DELETE', 7);
		expect(dismissals.hidden.has(7)).toBe(false);
	});

	it('offers to try again when "Cofnij" fails, keeping the listing hidden', async () => {
		const h = host(async (method) => {
			if (method === 'DELETE' && h.send.mock.calls.length === 2) throw unavailable();
		});
		const dismissals = new DealDismissals(h);
		await dismissals.dismiss(deal);
		h.reload.mockClear();
		await h.act(h.toasts[0]);
		expect(dismissals.hidden.has(7)).toBe(true);
		const retry = h.toasts.at(-1);
		expect([retry?.title, retry?.tone, retry?.action?.label]).toEqual([
			'Nie udało się przywrócić oferty',
			'danger',
			'Spróbuj ponownie'
		]);
		h.reload.mockClear();
		await h.act(retry);
		expect(h.send).toHaveBeenCalledTimes(3);
		expect(dismissals.hidden.has(7)).toBe(false);
	});

	it('shows the listing again and offers to resend a dismissal whose answer was lost', async () => {
		const h = host(async () => {
			if (h.send.mock.calls.length === 1) throw new TypeError('connection reset');
		});
		const dismissals = new DealDismissals(h);
		await dismissals.dismiss(deal);
		expect(dismissals.hidden.has(7)).toBe(false);
		const retry = h.toasts[0];
		expect([retry?.title, retry?.tone, retry?.action?.label]).toEqual([
			'Nie udało się zignorować oferty',
			'danger',
			'Spróbuj ponownie'
		]);
		h.reload.mockClear();
		await h.act(retry);
		expect(h.send).toHaveBeenLastCalledWith('POST', 7);
		expect(dismissals.hidden.has(7)).toBe(true);
		expect(h.toasts.at(-1)?.action?.label).toBe('Cofnij');
	});

	it('takes back its toasts when the page goes away and raises none afterwards', async () => {
		const h = host();
		const dismissals = new DealDismissals(h);
		await dismissals.dismiss(deal);
		const answer = Promise.withResolvers<undefined>();
		h.send.mockImplementation(() => answer.promise);
		const late = dismissals.dismiss({ id: 8, listing_title: '' });
		dismissals.destroy();
		expect(h.toasts[0]?.closed).toBe('withdrawn');
		h.reload.mockClear();
		answer.resolve(undefined);
		await late;
		expect(h.toasts).toHaveLength(1);
		expect(h.reload).not.toHaveBeenCalled();
	});

	it('leaves a toast already closed alone when the page goes away', async () => {
		const h = host();
		const dismissals = new DealDismissals(h);
		await dismissals.dismiss(deal);
		h.dismiss(1, 'dismissed');
		dismissals.destroy();
		expect(h.toasts[0]?.closed).toBe('dismissed');
	});
});
