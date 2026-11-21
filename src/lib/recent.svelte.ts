import { keepVisit, storedVisit } from './visit';

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
export const recentScans = $state<{ list: RecentScan[] }>({ list: storedVisit().scans });

/**
 * The last scan's id; a reload goes on from the ids the visit kept, its adds' links to their scans
 * included, so no id comes back.
 */
let nextID = Math.max(
	0,
	...recentScans.list.map((r) => r.id),
	...storedVisit().adds.map((a) => a.recentID ?? 0)
);

/** Keeps the visit's scans for the next load; a scan restarts the visit's lifetime. */
function keepScans(activity: boolean) {
	keepVisit({ scans: $state.snapshot(recentScans.list) }, activity);
}

/** Logs a scan at the top of the list and returns its id. */
export function logScan(barcode: string, outcome: ScanOutcome, label: string, href?: string) {
	const id = ++nextID;
	recentScans.list = [{ id, barcode, outcome, label, href }, ...recentScans.list].slice(0, 20);
	keepScans(true);
	return id;
}

/** Marks a logged add as undone; its link led to a copy that is gone. */
export function retractScan(id: number) {
	const scan = recentScans.list.find((r) => r.id === id);
	if (!scan) return;
	scan.outcome = 'undone';
	scan.href = undefined;
	keepScans(false);
}
