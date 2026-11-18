/** How a scan ended, for the scanner's list of recent scans. */
export type ScanOutcome = 'added' | 'queued' | 'skipped';

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
	skipped: 'Pominięto'
};

export const outcomeTones = { added: 'success', queued: 'warning', skipped: 'neutral' } as const;

/**
 * The scans of this visit, newest first. It lives in the module, so it outlasts the trips
 * between the scanner and the add page, and resets on a reload.
 */
export const recentScans = $state<{ list: RecentScan[] }>({ list: [] });

let nextID = 0;

export function logScan(barcode: string, outcome: ScanOutcome, label: string, href?: string) {
	recentScans.list = [{ id: ++nextID, barcode, outcome, label, href }, ...recentScans.list].slice(
		0,
		20
	);
}
