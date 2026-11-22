import { ApiError, request } from './api';
import type { CopyStatus, Kind } from './collection';
import { retracted } from './recent.svelte';
import { bindVisit, changeVisit, type StoredVisit } from './visit';

/** One copy this visit added through the quick confirm, with what undoing it has to remove. */
export type SessionAdd = {
	id: number;
	copyID: number;
	editionID: number;
	titleID: number;
	/** The add created the edition, not only the copy, so undoing it removes the edition too. */
	createdEdition: boolean;
	/** The add created the title as well. */
	createdTitle: boolean;
	name: string;
	/** The medium, such as "4K UHD" or "Twarda oprawa"; empty when unknown. */
	medium: string;
	kind: Kind;
	cover?: string;
	status: CopyStatus;
	/** What an owned copy cost; null for a wish or a price left blank. */
	priceCents: number | null;
	/** The add's line in the recent scans; null for an add without a barcode, which is no scan. */
	recentID: number | null;
};

/**
 * The copies this visit added, newest first. It lives in the module, so it outlasts the trips
 * between the scanner and the add page, and the browser's storage keeps it across reloads until
 * the visit ends.
 */
export const session = $state<{ adds: SessionAdd[] }>({ adds: [] });

session.adds = bindVisit('adds', {
	get: () => $state.snapshot(session.adds),
	set: (adds) => {
		session.adds = adds;
	}
});

/** The last id this page gave an add, so an undone add's id never comes back. */
let lastID = 0;

/**
 * Records an add at the top of the visit's adds. Its id follows every add the visit holds, those
 * another tab or an earlier load made included.
 */
export function recordAdd(add: Omit<SessionAdd, 'id'>): SessionAdd {
	let recorded: SessionAdd = { ...add, id: 0 };
	changeVisit((v) => {
		lastID = Math.max(lastID, ...v.adds.map((a) => a.id)) + 1;
		recorded = { ...add, id: lastID };
		return { ...v, adds: [recorded, ...v.adds] };
	}, true);
	return recorded;
}

/** Forgets every add, as the end of a visit does. */
export function resetSession() {
	changeVisit((v) => ({ ...v, adds: [] }), false);
}

/** What the visit's owned copies cost, in grosze; wishes and blank prices add nothing. */
export function sessionSpent(adds: readonly SessionAdd[]): number {
	return adds.reduce((sum, a) => sum + (a.status === 'owned' ? (a.priceCents ?? 0) : 0), 0);
}

/** The add as the toast and the list name it: the title and its medium. */
export function addLine(add: Pick<SessionAdd, 'name' | 'medium'>): string {
	return add.medium ? `${add.name} · ${add.medium}` : add.name;
}

function status(err: unknown): number | undefined {
	return err instanceof ApiError ? err.status : undefined;
}

/** Deletes a copy; one already gone counts as deleted. */
async function deleteCopy(fetchFn: typeof fetch, id: number) {
	try {
		await request<void>(fetchFn, 'DELETE', `/api/copies/${id}`);
	} catch (err) {
		if (status(err) !== 404) throw err;
	}
}

/**
 * Deletes an edition or a title unless something still uses it: the server refuses (409) an
 * edition with copies and a title with editions. Returns whether it is gone.
 */
async function deleteUnused(fetchFn: typeof fetch, path: string): Promise<boolean> {
	try {
		await request<void>(fetchFn, 'DELETE', path);
		return true;
	} catch (err) {
		const code = status(err);
		if (code === 404) return true;
		if (code === 409) return false;
		throw err;
	}
}

/**
 * Undoes an add: deletes its copy, then the edition when the add created it and nothing else
 * uses it, then the title on the same terms. The server puts the scan queue entry the edition
 * took out back in the same step as the edition's delete, so a repeat never puts it back twice.
 * An edition or a title the add found is never touched. The add leaves the visit and its recent
 * scan reads as undone. A failure leaves the add in place; trying again picks up where the last
 * attempt stopped. Once the copy is gone, the next load no longer shows the add, even when its
 * edition or title is still to be removed, as nothing there offers to try again.
 */
export async function undoAdd(fetchFn: typeof fetch, add: SessionAdd): Promise<void> {
	await deleteCopy(fetchFn, add.copyID);
	changeVisit((v) => withoutAdd(v, add), false, false);
	if (add.createdEdition) {
		const gone = await deleteUnused(fetchFn, `/api/editions/${add.editionID}?requeue=1`);
		if (gone && add.createdTitle) await deleteUnused(fetchFn, `/api/titles/${add.titleID}`);
	}
	changeVisit((v) => withoutAdd(v, add), false);
}

/** The visit without the add, its recent scan read as undone. */
function withoutAdd(v: StoredVisit, add: SessionAdd): StoredVisit {
	return {
		...v,
		adds: v.adds.filter((a) => a.id !== add.id),
		scans: add.recentID === null ? v.scans : retracted(v.scans, add.recentID)
	};
}
