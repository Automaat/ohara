import type { CopyStatus, Kind } from './collection';
import type { RecentScan, ScanOutcome } from './recent.svelte';
import { browserStore, type KeyValueStore } from './scan';
import type { SessionAdd } from './session.svelte';

/** How long a visit lasts after its last add or scan, in milliseconds. */
export const visitLifetime = 12 * 60 * 60 * 1000;

const visitKey = 'ohara.scan.visit';

/** What a visit carries: the adds, the recent scans and the purchase date for the next add. */
export type VisitParts = {
	adds: SessionAdd[];
	scans: RecentScan[];
	date: string;
};

/** What the scanner keeps of one visit across reloads, with the time of its last add or scan. */
export type StoredVisit = VisitParts & {
	/** When the visit last added or scanned, in milliseconds since the epoch; 0 for none. */
	at: number;
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
 * Whether a visit last active at `at` has ended by `now`: `visitLifetime` after it, or with that
 * time over a lifetime ahead of now, as after a clock set back.
 */
function ended(at: number, now: number): boolean {
	return now - at >= visitLifetime || at - now >= visitLifetime;
}

/**
 * The visit a store keeps: empty when it keeps none, keeps something unusable or the visit
 * ended; null when there is no store or it cannot be read.
 */
function readStored(store: KeyValueStore | undefined, now: number): StoredVisit | null {
	if (!store) return null;
	let raw: string | null;
	try {
		raw = store.getItem(visitKey);
	} catch {
		return null;
	}
	try {
		const v = record(raw ? JSON.parse(raw) : null);
		if (!v || typeof v.at !== 'number' || !Number.isFinite(v.at) || ended(v.at, now)) {
			return emptyVisit();
		}
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

/**
 * The visit a store keeps, or an empty one when it keeps none, cannot be read, keeps something
 * unusable or the visit ended. Entries that do not read as an add or a scan are left out.
 */
export function readVisit(store: KeyValueStore | undefined, now: number): StoredVisit {
	return readStored(store, now) ?? emptyVisit();
}

/** A module's own copy of one part of the visit, which it shows and changes. */
export type VisitPart<K extends keyof VisitParts> = {
	/** The part as the module holds it now, as plain data. */
	get(): VisitParts[K];
	/** Takes the part as the visit now has it. */
	set(value: VisitParts[K]): void;
};

type Parts = { [K in keyof VisitParts]?: VisitPart<K> };

type Kept = {
	store: KeyValueStore | undefined;
	/** The visit as last changed or read here. */
	visit: StoredVisit;
	/** Whether the store holds what was last changed here, so it can be read back as the base. */
	synced: boolean;
	/** How many batches are open; the visit is written once the last one closes. */
	batch: number;
	/** Whether a change waits to be written. */
	dirty: boolean;
	parts: Parts;
};

let kept: Kept | null = null;

function adopt(k: Kept, v: VisitParts): void {
	k.parts.adds?.set(v.adds);
	k.parts.scans?.set(v.scans);
	k.parts.date?.set(v.date);
}

/** Opens the visit once per load and follows what other tabs of the app write to it. */
function opened(): Kept {
	if (kept !== null) return kept;
	const store = browserStore();
	const k: Kept = {
		store,
		visit: readVisit(store, Date.now()),
		synced: true,
		batch: 0,
		dirty: false,
		parts: {}
	};
	kept = k;
	if (typeof window !== 'undefined') {
		window.addEventListener('storage', (e: StorageEvent) => {
			if (e.key !== visitKey && e.key !== null) return;
			const stored = readStored(k.store, Date.now());
			if (!stored) return;
			k.visit = stored;
			k.synced = true;
			adopt(k, stored);
		});
	}
	return k;
}

/**
 * Binds a module's copy of one part of the visit and returns the part this load starts with: what
 * the browser's storage kept from before a reload, or nothing where storage is off or the visit
 * ended. Every change of the visit hands the module the part as it then is.
 */
export function bindVisit<K extends keyof VisitParts>(key: K, part: VisitPart<K>): VisitParts[K] {
	const k = opened();
	(k.parts as Record<K, VisitPart<K>>)[key] = part;
	return k.visit[key];
}

/** The visit as this tab holds it: each bound module's part, else what was last read or changed. */
function inMemory(k: Kept): StoredVisit {
	return {
		at: k.visit.at,
		adds: k.parts.adds?.get() ?? k.visit.adds,
		scans: k.parts.scans?.get() ?? k.visit.scans,
		date: k.parts.date?.get() ?? k.visit.date
	};
}

/**
 * What a change starts from: the visit the store keeps, so a change made in another tab is not
 * lost, unless the store is off or refused the last write; then the visit this tab holds. A visit
 * that ended starts over empty; without storage it lasts until the next load.
 */
function base(k: Kept, now: number): StoredVisit {
	if (k.dirty) return k.visit;
	const stored = k.synced ? readStored(k.store, now) : null;
	if (stored) return stored;
	const held = inMemory(k);
	return k.store && ended(held.at, now) ? emptyVisit() : held;
}

function flush(k: Kept): void {
	k.dirty = false;
	try {
		k.store?.setItem(visitKey, JSON.stringify(k.visit));
		k.synced = true;
	} catch {
		k.synced = false;
	}
}

/**
 * Changes the visit and keeps it for the next load. An add or a scan (`activity`) restarts the
 * visit's lifetime; an undo or a reset keeps its time. The bound modules take the changed visit,
 * unless `shown` is false: then only the next load sees the change. Storage that refuses is
 * ignored, so the visit then lasts until the next load. Returns the changed visit.
 */
export function changeVisit(
	change: (v: StoredVisit) => VisitParts,
	activity: boolean,
	shown = true
): StoredVisit {
	const k = opened();
	const now = Date.now();
	const from = base(k, now);
	const next: StoredVisit = { ...change(from), at: activity ? now : from.at };
	k.visit = next;
	k.dirty = true;
	if (shown) adopt(k, next);
	if (k.batch === 0) flush(k);
	return next;
}

/** Runs changes of the visit that are kept together, in one write once they all ran. */
export function batchVisit<T>(run: () => T): T {
	const k = opened();
	k.batch++;
	try {
		return run();
	} finally {
		k.batch--;
		if (k.batch === 0 && k.dirty) flush(k);
	}
}
