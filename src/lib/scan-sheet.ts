import type { SessionAdd } from './session.svelte.js';

/** The sheet over the scanner: the code it shows, its own number and whether the code was typed. */
export type Sheet = NonNullable<App.PageState['scanSheet']>;

/** Numbers each sheet, so a barcode read again opens a fresh one. */
let sheetSeq = 0;

/** What the sheet's history needs from the page. */
export type SheetHost = {
	/** The sheet the current history entry holds, or null. */
	sheet(): Sheet | null;
	/** Puts a sheet in history: in an entry of its own, or in the current one when `replace`. */
	show(sheet: Sheet, replace: boolean): void;
	/** Goes back one history entry. */
	back(): void;
	/** Looks up the code a sheet has just opened on. */
	load(barcode: string): void;
	/** Clears what a closed sheet leaves behind, given every code it showed. */
	closed(codes: string[]): void;
	/** Opens another page. */
	goto(href: string): void;
	/** Shows an add's toast. */
	toast(add: SessionAdd): void;
	/** Resolves once the page has drawn its latest change. */
	tick(): Promise<void>;
};

/**
 * The scanner's sheet in history: each code opens the sheet in an entry of its own, so going back
 * closes it, and the next code takes the same entry. Closing goes back once, and what waits for
 * the sheet to close (another page, an add's toast) happens only once its entry is gone.
 */
export class SheetHistory {
	readonly #host: SheetHost;
	/** The sheet last opened, until it closes. */
	#shown: Sheet | null = null;
	/** Every code the open sheet has shown, a wedge scan's next codes included. */
	#handled: string[] = [];
	/** The sheet whose close has gone back already, or 0. */
	#closing = 0;
	/** Where to go once the sheet's history entry is gone, as the add page for a code by hand. */
	#afterClose: string | null = null;
	/** An add whose toast waits for the sheet to close, so the page's toasts announce it. */
	#addedOnClose: SessionAdd | null = null;

	constructor(host: SheetHost) {
		this.#host = host;
	}

	/**
	 * Opens the sheet for a code, in a history entry of its own so going back closes it. A sheet
	 * already open gives way to the next code in the same entry.
	 */
	open(barcode: string, typed: boolean): void {
		const replace = this.#host.sheet() !== null;
		this.#host.show({ barcode, id: ++sheetSeq, typed }, replace);
	}

	/** Closes the open sheet by going back from its entry, once however often it is asked. */
	close(): void {
		const s = this.#host.sheet();
		if (!s || this.#closing === s.id) return;
		this.#closing = s.id;
		this.#host.back();
	}

	/**
	 * A dialog's `close` event comes a moment after it closed. When Back and then Forward, or the
	 * next code, has opened the sheet again by then, the event belongs to the earlier close, so it
	 * never reaches the sheet's Modal, which would close the reopened sheet and go back from it.
	 */
	stopLateClose(event: Event, dialog: (EventTarget & { readonly open: boolean }) | null): void {
		if (event.target === dialog && dialog?.open) event.stopImmediatePropagation();
	}

	/**
	 * After an add the sheet closes and the toast names what went in once it has. A wedge scan's
	 * next code keeps the sheet open through `reopen`, so its toast shows in the sheet.
	 */
	added(add: SessionAdd, next: string | null, reopen: (code: string) => void): void {
		if (!this.#host.sheet()) return;
		if (next) {
			reopen(next);
			this.#host.toast(add);
		} else {
			this.#addedOnClose = add;
			this.close();
		}
	}

	/**
	 * Closes the sheet and opens `href` once its entry is gone, so going back from that page lands
	 * on the scanner rather than reopening the sheet.
	 */
	leaveTo(href: string): void {
		if (!this.#host.sheet()) return;
		this.#afterClose = href;
		this.close();
	}

	/** Follows the sheet the current history entry holds, as the page's history moves. */
	sync(sheet: Sheet | null): void {
		if (sheet) this.#opened(sheet);
		else if (this.#shown) this.#closed();
	}

	#opened(s: Sheet): void {
		if (this.#shown?.id === s.id) return;
		this.#shown = s;
		if (!this.#handled.includes(s.barcode)) this.#handled.push(s.barcode);
		this.#host.load(s.barcode);
	}

	#closed(): void {
		const codes = this.#handled;
		this.#handled = [];
		this.#shown = null;
		this.#closing = 0;
		this.#host.closed(codes);
		const next = this.#afterClose;
		this.#afterClose = null;
		const add = this.#addedOnClose;
		this.#addedOnClose = null;
		if (add) void this.#toastAfterClose(add);
		if (next) this.#host.goto(next);
	}

	/** Shows an add's toast once the closed sheet no longer hosts the page's toasts. */
	async #toastAfterClose(add: SessionAdd): Promise<void> {
		await this.#host.tick();
		if (!this.#host.sheet()) this.#host.toast(add);
	}
}
