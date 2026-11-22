/** The part of a toast's element the hold check reads. */
export type ToastElement = {
	matches(selector: string): boolean;
	contains(other: Element | null): boolean;
};

/** The part of the document the hold check reads: the toast and where focus is. */
export type ToastDocument = {
	querySelector(selectors: string): ToastElement | null;
	readonly activeElement: Element | null;
};

/**
 * Whether the reader is on the toast with this id: the pointer over it or focus inside it. A toast
 * that is not shown is not held.
 */
export function toastHeld(doc: ToastDocument, id: number): boolean {
	const el = doc.querySelector(`[data-toast="${id}"]`);
	return el !== null && (el.matches(':hover') || el.contains(doc.activeElement));
}
