import type { CopyStatus, Kind } from './collection';
import type { RecentScan, ScanOutcome } from './recent.svelte';
import { browserStore, type KeyValueStore } from './scan';
import type { SessionAdd } from './session.svelte';

/** How long a visit lasts after its last add or scan, in milliseconds. */
export const visitLifetime = 12 * 60 * 60 * 1000;

const visitKey = 'ohara.scan.visit';

/**
 * What the scanner keeps of one visit across reloads: the adds, the recent scans and the purchase
 * date carried to the next add, with the time of the last add or scan.
 */
export type StoredVisit = {
	/** When the visit last added or scanned, in milliseconds since the epoch; 0 for none. */
	at: number;
	adds: SessionAdd[];
	scans: RecentScan[];
	date: string;
};

function emptyVisit(): StoredVisit {
	return { at: 0, adds: [], scans: [], date: '' };
}

const kinds: readonly Kind[] = ['movie', 'book', 'music'];
const statuses: readonly CopyStatus[] = ['owned', 'wishlist'];
const outcomes: readonly ScanOutcome[] = ['added', 'queued', 'waiting', 'skipped', 'undone'];

function isID(v: unknown): v is number {
	return typeof v === 'number' && Number.isSafeInteger(v) && v > 0;
}

function isOneOf<T extends string>(list: readonly T[], v: unknown): v is T {
	return typeof v === 'string' && (list as readonly string[]).includes(v);
}

function record(v: unknown): Record<string, unknown> | null {
	return v && typeof v === 'object' && !Array.isArray(v) ? (v as Record<string, unknown>) : null;
}

function toAdd(v: unknown): SessionAdd | null {
	const a = record(v);
	if (
		!a ||
		!isID(a.id) ||
		!isID(a.copyID) ||
		!isID(a.editionID) ||
		!isID(a.titleID) ||
		typeof a.createdEdition !== 'boolean' ||
		typeof a.createdTitle !== 'boolean' ||
		typeof a.name !== 'string' ||
		typeof a.medium !== 'string' ||
		!isOneOf(kinds, a.kind) ||
		!isOneOf(statuses, a.status) ||
		(a.cover !== undefined && typeof a.cover !== 'string') ||
		(a.priceCents !== null && !(Number.isSafeInteger(a.priceCents) && Number(a.priceCents) >= 0)) ||
		(a.recentID !== null && !isID(a.recentID))
	) {
		return null;
	}
	return {
		id: a.id,
		copyID: a.copyID,
		editionID: a.editionID,
		titleID: a.titleID,
		createdEdition: a.createdEdition,
		createdTitle: a.createdTitle,
		name: a.name,
		medium: a.medium,
		kind: a.kind,
		...(a.cover === undefined ? {} : { cover: a.cover }),
		status: a.status,
		priceCents: a.priceCents as number | null,
		recentID: a.recentID as number | null
	};
}

function toScan(v: unknown): RecentScan | null {
	const s = record(v);
	if (
		!s ||
		!isID(s.id) ||
		typeof s.barcode !== 'string' ||
		!isOneOf(outcomes, s.outcome) ||
		typeof s.label !== 'string' ||
		(s.href !== undefined && typeof s.href !== 'string')
	) {
		return null;
	}
	return {
		id: s.id,
		barcode: s.barcode,
		outcome: s.outcome,
		label: s.label,
		...(s.href === undefined ? {} : { href: s.href })
	};
}

function entries<T>(v: unknown, item: (v: unknown) => T | null): T[] {
	return Array.isArray(v) ? v.map(item).filter((x): x is T => x !== null) : [];
}

/**
 * The visit a store keeps, or an empty one when it keeps none, keeps something unusable or the
 * visit ended: `visitLifetime` after its last add or scan, or with that time over a lifetime ahead
 * of now, as after a clock set back. Entries that do not read as an add or a scan are left out.
 */
export function readVisit(store: KeyValueStore | undefined, now: number): StoredVisit {
	try {
		const raw = store?.getItem(visitKey);
		const v = record(raw ? JSON.parse(raw) : null);
		if (!v || typeof v.at !== 'number' || !Number.isFinite(v.at)) return emptyVisit();
		if (now - v.at >= visitLifetime || v.at - now >= visitLifetime) return emptyVisit();
		return {
			at: v.at,
			adds: entries(v.adds, toAdd),
			scans: entries(v.scans, toScan),
			date: typeof v.date === 'string' && /^\d{4}-\d{2}-\d{2}$/.test(v.date) ? v.date : ''
		};
	} catch {
		return emptyVisit();
	}
}

/** Where the visit is kept and what was kept last; read once, on the first ask. */
let kept: { store: KeyValueStore | undefined; visit: StoredVisit } | null = null;

function opened(): { store: KeyValueStore | undefined; visit: StoredVisit } {
	if (kept === null) {
		const store = browserStore();
		kept = { store, visit: readVisit(store, Date.now()) };
	}
	return kept;
}

/**
 * The visit this page load started with: what the browser's storage kept from before a reload,
 * or an empty visit where storage is off or the visit ended.
 */
export function storedVisit(): StoredVisit {
	return opened().visit;
}

/**
 * Keeps part of the visit for the next load. An add or a scan (`activity`) restarts the visit's
 * lifetime; an undo or a reset keeps its time. Storage that refuses is ignored, so the visit then
 * lasts until the next load.
 */
export function keepVisit(part: Partial<Omit<StoredVisit, 'at'>>, activity: boolean): void {
	const k = opened();
	k.visit = { ...k.visit, ...part, at: activity ? Date.now() : k.visit.at };
	try {
		k.store?.setItem(visitKey, JSON.stringify(k.visit));
	} catch {
		return;
	}
}
