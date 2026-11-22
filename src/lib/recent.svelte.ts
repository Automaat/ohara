import { canonicalBarcode } from './scan';
import { bindVisit, changeVisit, type StoredVisit } from './visit';

/**
 * How a scan ended, for the scanner's list of recent scans; waiting is kept on the device until
 * the scan queue takes it.
 */
export type ScanOutcome = 'added' | 'queued' | 'waiting' | 'skipped' | 'undone';

export type RecentScan = {
	id: number;
	barcode: string;
	outcome: ScanOutcome;
	label: string;
	href?: string;
};

export const outcomeLabels: Record<ScanOutcome, string> = {
	added: 'Dodano',
	queued: 'W kolejce',
	waiting: 'Czeka',
	skipped: 'Pominięto',
	undone: 'Cofnięto'
};

export const outcomeTones = {
	added: 'success',
	queued: 'warning',
	waiting: 'warning',
	skipped: 'neutral',
	undone: 'neutral'
} as const;

/**
 * The scans of this visit, newest first. It lives in the module, so it outlasts the trips
 * between the scanner and the add page, and the browser's storage keeps it across reloads until
 * the visit ends.
 */
export const recentScans = $state<{ list: RecentScan[] }>({ list: [] });

recentScans.list = bindVisit('scans', {
	get: () => $state.snapshot(recentScans.list),
	set: (list) => {
		recentScans.list = list;
	}
});

/** The last id this page gave a scan, so a scan dropped from the list never gives its id back. */
let lastID = 0;

/** The next scan id, after every scan the visit holds and every add's link to its scan. */
function nextID(v: StoredVisit): number {
	lastID = Math.max(lastID, ...v.scans.map((r) => r.id), ...v.adds.map((a) => a.recentID ?? 0)) + 1;
	return lastID;
}

/** Logs a scan at the top of the list and returns its id. */
export function logScan(barcode: string, outcome: ScanOutcome, label: string, href?: string) {
	let id = 0;
	changeVisit((v) => {
		id = nextID(v);
		return { ...v, scans: [{ id, barcode, outcome, label, href }, ...v.scans].slice(0, 20) };
	}, true);
	return id;
}

/** The scans with a logged add marked undone; its link led to a copy that is gone. */
export function retracted(scans: readonly RecentScan[], id: number): RecentScan[] {
	return scans.map((r) => (r.id === id ? { ...r, outcome: 'undone', href: undefined } : r));
}

/** Marks a logged add as undone; its link led to a copy that is gone. */
export function retractScan(id: number) {
	changeVisit((v) => ({ ...v, scans: retracted(v.scans, id) }), false);
}

/**
 * Marks the scans of a code that waited on the device as queued once the scan queue took it; the
 * code is in the server's normalized form.
 */
export function queueWaiting(barcode: string) {
	changeVisit(
		(v) => ({
			...v,
			scans: v.scans.map((r) =>
				r.outcome === 'waiting' && canonicalBarcode(r.barcode) === barcode
					? { ...r, outcome: 'queued', href: '/scan/queue' }
					: r
			)
		}),
		false
	);
}
