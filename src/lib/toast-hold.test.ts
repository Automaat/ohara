import { describe, expect, it } from 'vitest';
import { toastHeld, type ToastDocument, type ToastElement } from './toast-hold';

/** A page with toast 7 shown, the pointer over it or not, and focus inside it or elsewhere. */
function page(opts: { hovered?: boolean; focusInside?: boolean; shown?: boolean }): {
	doc: ToastDocument;
	selectors: string[];
} {
	const inside = {} as Element;
	const outside = {} as Element;
	const toast: ToastElement = {
		matches: (selector) => selector === ':hover' && opts.hovered === true,
		contains: (other) => other === inside
	};
	const selectors: string[] = [];
	const doc: ToastDocument = {
		querySelector: (selector) => {
			selectors.push(selector);
			return opts.shown === false || selector !== '[data-toast="7"]' ? null : toast;
		},
		activeElement: opts.focusInside ? inside : outside
	};
	return { doc, selectors };
}

describe('toastHeld', () => {
	it('holds a toast under the pointer', () => {
		expect(toastHeld(page({ hovered: true }).doc, 7)).toBe(true);
	});

	it('holds a toast with focus inside it', () => {
		expect(toastHeld(page({ focusInside: true }).doc, 7)).toBe(true);
	});

	it('lets a toast go when neither pointer nor focus is on it', () => {
		expect(toastHeld(page({}).doc, 7)).toBe(false);
	});

	it('lets go of a toast that is not shown', () => {
		const { doc, selectors } = page({ hovered: true, focusInside: true, shown: false });
		expect(toastHeld(doc, 7)).toBe(false);
		expect(selectors).toEqual(['[data-toast="7"]']);
	});

	it('looks the toast up by its id', () => {
		expect(toastHeld(page({ hovered: true }).doc, 8)).toBe(false);
	});
});
