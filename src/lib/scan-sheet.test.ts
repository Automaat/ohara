import { describe, expect, it } from 'vitest';
import { SheetHistory, type Sheet } from './scan-sheet';
import type { SessionAdd } from './session.svelte';

/** A dialog element as far as the late close cares: an event target that is open or not. */
class FakeDialog extends EventTarget {
	open = false;
}

/**
 * Hears a dialog's `close` event as the page does: its document listener first, in the capture
 * phase, then the Modal's own, which closes the sheet.
 */
function listen(sheets: SheetHistory, target: FakeDialog, dialog: FakeDialog, modal: () => void) {
	target.addEventListener('close', (e) => sheets.stopLateClose(e, dialog));
	target.addEventListener('close', modal);
}

/**
 * A browser history for the scanner's page, with the page around it: Back and Forward move only
 * once `settle` runs them, as `popstate` comes after `history.back()` returns, and every move
 * tells the sheet history which sheet the entry now holds, as the page's `$effect` does.
 */
function scanner() {
	const entries: (Sheet | null)[] = [null];
	let at = 0;
	const moves: number[] = [];
	const dialog = new FakeDialog();
	const log = {
		backs: 0,
		loads: [] as string[],
		closed: [] as string[][],
		pages: [] as string[],
		toasts: [] as string[]
	};
	const sheets = new SheetHistory({
		sheet: () => entries[at] ?? null,
		show(sheet, replace) {
			if (replace) {
				entries[at] = sheet;
			} else {
				entries.splice(at + 1, entries.length, sheet);
				at++;
			}
			follow();
		},
		back() {
			log.backs++;
			moves.push(-1);
		},
		load: (barcode) => log.loads.push(barcode),
		closed: (codes) => log.closed.push(codes),
		goto: (href) => log.pages.push(href),
		toast: (a) => log.toasts.push(a.name),
		tick: () => Promise.resolve()
	});

	/** The page follows the entry: the sheet's Modal opens or closes with it. */
	function follow() {
		const s = entries[at] ?? null;
		sheets.sync(s);
		dialog.open = s !== null;
	}

	listen(sheets, dialog, dialog, () => sheets.close());

	return {
		sheets,
		dialog,
		log,
		entries,
		get at() {
			return at;
		},
		current: () => entries[at] ?? null,
		/** The reader's Back or Forward button. */
		go(step: number) {
			moves.push(step);
		},
		/** Runs the history moves asked for so far, in order. */
		settle() {
			for (const step of moves.splice(0)) {
				const to = at + step;
				if (to < 0 || to >= entries.length) continue;
				at = to;
				follow();
			}
		},
		/** The dialog's `close` event, which comes a moment after the dialog closed. */
		closeEvent() {
			dialog.dispatchEvent(new Event('close'));
		}
	};
}

/** Lets every pending promise settle. */
function flush(): Promise<void> {
	return new Promise((resolve) => setTimeout(resolve, 0));
}

function add(name: string): SessionAdd {
	return {
		id: 1,
		copyID: 40,
		editionID: 21,
		titleID: 9,
		createdEdition: true,
		createdTitle: true,
		name,
		medium: 'DVD',
		kind: 'movie',
		status: 'owned',
		priceCents: 5000,
		recentID: 1
	};
}

describe('SheetHistory', () => {
	it('opens a code in a history entry of its own and looks it up', () => {
		const s = scanner();
		s.sheets.open('5901234123457', true);
		expect(s.entries).toHaveLength(2);
		expect(s.current()).toMatchObject({ barcode: '5901234123457', typed: true });
		expect(s.log.loads).toEqual(['5901234123457']);
	});

	it('gives the next code the open sheet entry, numbered afresh', () => {
		const s = scanner();
		s.sheets.open('5901234123457', false);
		const first = s.current();
		s.sheets.open('5901234123457', false);
		expect(s.entries).toHaveLength(2);
		expect(s.current()?.id).not.toBe(first?.id);
		expect(s.log.loads).toEqual(['5901234123457', '5901234123457']);
	});

	it('looks a sheet up once however often the page reports it', () => {
		const s = scanner();
		s.sheets.open('5901234123457', false);
		s.sheets.sync(s.current());
		s.sheets.sync(s.current());
		expect(s.log.loads).toEqual(['5901234123457']);
	});

	it('goes back once however many close events arrive', () => {
		const s = scanner();
		s.sheets.open('5901234123457', false);
		s.sheets.close();
		s.sheets.close();
		s.closeEvent();
		expect(s.log.backs).toBe(1);
		s.settle();
		expect(s.current()).toBeNull();
		s.sheets.close();
		s.closeEvent();
		expect(s.log.backs).toBe(1);
		expect(s.log.closed).toHaveLength(1);
	});

	it('hands the closed sheet every code it showed, once each', () => {
		const s = scanner();
		s.sheets.open('5901234123457', false);
		s.sheets.open('9780306406157', true);
		s.sheets.open('5901234123457', true);
		s.sheets.close();
		s.settle();
		expect(s.log.closed).toEqual([['5901234123457', '9780306406157']]);
		s.sheets.open('9780306406157', false);
		s.sheets.close();
		s.settle();
		expect(s.log.closed.at(-1)).toEqual(['9780306406157']);
	});

	it('ignores a close event that arrives after Back and then Forward (#128)', () => {
		const s = scanner();
		s.sheets.open('5901234123457', false);
		const opened = s.current();
		s.go(-1);
		s.go(1);
		s.settle();
		expect(s.current()).toEqual(opened);
		expect(s.dialog.open).toBe(true);
		s.closeEvent();
		expect(s.log.backs).toBe(0);
		expect(s.current()).toEqual(opened);
		expect(s.at).toBe(1);
	});

	it('lets the reopened sheet close again after Back and then Forward', () => {
		const s = scanner();
		s.sheets.open('5901234123457', false);
		s.sheets.close();
		s.settle();
		s.go(1);
		s.settle();
		expect(s.current()).not.toBeNull();
		s.dialog.open = false;
		s.closeEvent();
		expect(s.log.backs).toBe(2);
		s.settle();
		expect(s.current()).toBeNull();
	});

	it('ignores a close event that arrives after the next code opened the sheet', () => {
		const s = scanner();
		s.sheets.open('5901234123457', false);
		s.sheets.open('9780306406157', false);
		s.closeEvent();
		expect(s.log.backs).toBe(0);
		expect(s.current()?.barcode).toBe('9780306406157');
	});

	it('lets a close event from another dialog through', () => {
		const s = scanner();
		const other = new FakeDialog();
		other.open = true;
		let heard = 0;
		listen(s.sheets, other, s.dialog, () => heard++);
		other.dispatchEvent(new Event('close'));
		expect(heard).toBe(1);
	});

	it('opens the page left to only after the sheet entry is gone', () => {
		const s = scanner();
		s.sheets.open('5901234123457', true);
		s.sheets.leaveTo('/scan/5901234123457?manual=1');
		expect(s.log.backs).toBe(1);
		expect(s.log.pages).toEqual([]);
		s.settle();
		expect(s.log.pages).toEqual(['/scan/5901234123457?manual=1']);
		s.sheets.open('9780306406157', false);
		s.sheets.close();
		s.settle();
		expect(s.log.pages).toHaveLength(1);
		expect(s.log.closed).toHaveLength(2);
	});

	it('leaves for nowhere without an open sheet', () => {
		const s = scanner();
		s.sheets.leaveTo('/scan/5901234123457?manual=1');
		expect(s.log.backs).toBe(0);
		s.sheets.open('5901234123457', false);
		s.sheets.close();
		s.settle();
		expect(s.log.pages).toEqual([]);
	});

	it("shows an add's toast only after the sheet has closed", async () => {
		const s = scanner();
		s.sheets.open('5901234123457', false);
		s.sheets.added(add('Diuna'), null, () => {
			throw new Error('no next code');
		});
		expect(s.log.backs).toBe(1);
		expect(s.log.toasts).toEqual([]);
		await flush();
		expect(s.log.toasts).toEqual([]);
		s.settle();
		expect(s.log.toasts).toEqual([]);
		await flush();
		expect(s.log.toasts).toEqual(['Diuna']);
		s.sheets.open('9780306406157', false);
		s.sheets.close();
		s.settle();
		await flush();
		expect(s.log.toasts).toEqual(['Diuna']);
	});

	it("keeps an add's toast back when a sheet opens again before it shows", async () => {
		const s = scanner();
		s.sheets.open('5901234123457', false);
		s.sheets.added(add('Diuna'), null, () => {});
		s.settle();
		s.sheets.open('9780306406157', false);
		await flush();
		expect(s.log.toasts).toEqual([]);
	});

	it("shows an add's toast at once when a wedge scan's next code keeps the sheet", () => {
		const s = scanner();
		s.sheets.open('5901234123457', true);
		const reopened: string[] = [];
		s.sheets.added(add('Diuna'), '9780306406157', (code) => {
			reopened.push(code);
			s.sheets.open(code, true);
		});
		expect(reopened).toEqual(['9780306406157']);
		expect(s.log.toasts).toEqual(['Diuna']);
		expect(s.log.backs).toBe(0);
		expect(s.entries).toHaveLength(2);
		expect(s.current()?.barcode).toBe('9780306406157');
	});

	it('ignores an add when no sheet is open', async () => {
		const s = scanner();
		s.sheets.added(add('Diuna'), null, () => {});
		s.sheets.added(add('Diuna'), '9780306406157', () => {
			throw new Error('no sheet to reopen');
		});
		await flush();
		expect(s.log.backs).toBe(0);
		expect(s.log.toasts).toEqual([]);
	});
});
