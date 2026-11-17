import { ApiError, errorMessage, request } from './api';
import {
	copyPayload,
	type AcquiredAs,
	type Binding,
	type Copy,
	type Edition,
	type EditionDetail,
	type EditionPayload,
	type Format,
	type Kind,
	type Packaging,
	type Parsed,
	type Title,
	type TitlePayload,
	type TriState
} from './collection';

/** A candidate's title fields, as GET /api/lookup/{barcode} sends them; absent means unknown. */
export type CandidateTitle = {
	name: string;
	original_title?: string | null;
	polish_title?: string | null;
	year?: number | null;
	tmdb_id?: number | null;
	authors?: string[];
	bn_id?: string | null;
	openlibrary_id?: string | null;
};

/** A candidate's edition fields, as GET /api/lookup/{barcode} sends them. */
export type CandidateEdition = {
	barcode: string;
	label?: string;
	publisher?: string;
	year?: number | null;
	format?: Format | null;
	pl_subtitles?: TriState;
	pl_voiceover?: TriState;
	packaging?: Packaging[];
	binding?: Binding | null;
};

export type Candidate = {
	type: Kind;
	title: CandidateTitle;
	edition: CandidateEdition;
	provider: string;
	confidence: number;
};

export type LookupResult = {
	barcode: string;
	candidates: Candidate[];
	errors: { provider: string; error: string }[];
};

export type QueueReason = 'not_found' | 'lookup_failed';

export type QueueEntry = {
	id: number;
	barcode: string;
	reason: QueueReason;
	detail: string;
	scans: number;
	created_at: string;
	last_scanned_at: string;
};

export const reasonLabels: Record<QueueReason, string> = {
	not_found: 'Nieznany kod',
	lookup_failed: 'Wyszukiwanie nie powiodło się'
};

/** The symbologies on discs and books; QR and the rest only slow the reader down. */
export const scanFormats = ['EAN13', 'EAN8', 'UPCA'] as const;

/** Drops the spaces and hyphens a person or a keyboard-wedge scanner adds around the digits. */
export function cleanBarcode(raw: string): string {
	return raw.replace(/[\s-]/g, '').toUpperCase();
}

/** Whether a cleaned input has a barcode's shape: 8, 12, 13 or 14 digits, or an ISBN-10. */
export function looksLikeBarcode(code: string): boolean {
	return /^(\d{8}|\d{12,14}|\d{9}[\dX])$/.test(code);
}

/** Whether a barcode is in the Bookland ISBN range, so a hand-added item starts as a book. */
export function isBookBarcode(code: string): boolean {
	return /^97[89]\d{10}$/.test(code) || /^\d{9}[\dX]$/.test(code);
}

/**
 * A price field that holds a barcode instead: a wedge scanner typed the next item into it.
 * No price has 8 digits before the comma (the cap is 1 000 000 zł), so such input is never a
 * price.
 */
export function wedgeBarcodeIn(price: string): string | null {
	const code = cleanBarcode(price);
	return /^\d{8,14}$/.test(code) && looksLikeBarcode(code) ? code : null;
}

/** What a scan resolved to: an edition already in the collection, candidates, or the queue. */
export type Resolution =
	| { kind: 'owned'; barcode: string; edition: EditionDetail }
	| { kind: 'found'; barcode: string; candidates: Candidate[]; errors: LookupResult['errors'] }
	| { kind: 'queued'; barcode: string; entry: QueueEntry };

function providerDetail(errors: LookupResult['errors']): string {
	return errors.map((e) => `${e.provider}: ${e.error}`).join('; ');
}

/**
 * Resolves a scanned barcode: an edition that already carries it, else the lookup's candidates,
 * else a scan queue entry so the barcode is not lost. Throws an ApiError for a malformed
 * barcode or when not even the queue answers.
 */
export async function resolveScan(fetchFn: typeof fetch, raw: string): Promise<Resolution> {
	const code = cleanBarcode(raw);
	const editions = await request<Edition[]>(
		fetchFn,
		'GET',
		`/api/editions?barcode=${encodeURIComponent(code)}`
	);
	const known = editions[0];
	if (known) {
		const edition = await request<EditionDetail>(fetchFn, 'GET', `/api/editions/${known.id}`);
		return { kind: 'owned', barcode: known.barcode ?? code, edition };
	}

	let lookup: LookupResult | null = null;
	let failure = '';
	try {
		lookup = await request<LookupResult>(fetchFn, 'GET', `/api/lookup/${encodeURIComponent(code)}`);
	} catch (err) {
		failure = errorMessage(err);
	}
	if (lookup && lookup.candidates.length > 0) {
		return {
			kind: 'found',
			barcode: lookup.barcode,
			candidates: lookup.candidates,
			errors: lookup.errors
		};
	}
	const barcode = lookup?.barcode ?? code;
	const failed = !lookup || lookup.errors.length > 0;
	const entry = await request<QueueEntry>(fetchFn, 'POST', '/api/scan-queue', {
		barcode,
		reason: failed ? 'lookup_failed' : 'not_found',
		detail: lookup ? providerDetail(lookup.errors) : failure
	});
	return { kind: 'queued', barcode, entry };
}

/** The fields the quick confirm asks for; the rest come from the candidate. */
export type QuickForm = {
	kind: Kind;
	name: string;
	format: Format | '';
	binding: Binding | '';
	price: string;
	shop: string;
	acquired_as: AcquiredAs | '';
};

/** What carries over from one scan to the next during bulk entry from one shop. */
export type Sticky = { shop: string; acquired_as: AcquiredAs | '' };

export const emptySticky: Sticky = { shop: '', acquired_as: '' };

/** The quick confirm for a candidate, or a blank one for a barcode added by hand. */
export function quickForm(candidate: Candidate | null, sticky: Sticky, barcode: string): QuickForm {
	return {
		kind: candidate?.type ?? (isBookBarcode(barcode) ? 'book' : 'movie'),
		name: candidate?.title.name ?? '',
		format: candidate?.edition.format ?? '',
		binding: candidate?.edition.binding ?? '',
		price: '',
		shop: sticky.shop,
		acquired_as: sticky.acquired_as
	};
}

function opt<T>(v: T | null | undefined): T | null {
	return v ?? null;
}

/** The POST /api/titles body: the form's kind and name over the candidate's other fields. */
export function scanTitlePayload(f: QuickForm, candidate: Candidate | null): Parsed<TitlePayload> {
	const name = f.name.trim();
	if (!name) return { ok: false, issue: { field: 'name', message: 'Podaj tytuł.' } };
	const t = candidate?.type === f.kind ? candidate.title : null;
	const movie = f.kind === 'movie';
	return {
		ok: true,
		value: {
			kind: f.kind,
			name,
			original_title: opt(t?.original_title),
			polish_title: opt(t?.polish_title),
			year: opt(t?.year),
			tmdb_id: movie ? opt(t?.tmdb_id) : null,
			authors: movie ? [] : (t?.authors ?? []),
			bn_id: movie ? null : opt(t?.bn_id),
			openlibrary_id: movie ? null : opt(t?.openlibrary_id),
			notes: ''
		}
	};
}

/** The POST /api/editions body for barcode: the form's medium over the candidate's details. */
export function scanEditionPayload(
	f: QuickForm,
	candidate: Candidate | null,
	barcode: string,
	titleID: number
): Parsed<EditionPayload> {
	const movie = f.kind === 'movie';
	if (movie && !f.format) {
		return { ok: false, issue: { field: 'format', message: 'Wybierz nośnik.' } };
	}
	const e = candidate?.type === f.kind ? candidate.edition : null;
	return {
		ok: true,
		value: {
			title_id: titleID,
			barcode,
			label: e?.label ?? '',
			publisher: e?.publisher ?? '',
			year: opt(e?.year),
			format: movie && f.format ? f.format : null,
			pl_subtitles: movie ? (e?.pl_subtitles ?? 'unknown') : 'unknown',
			pl_voiceover: movie ? (e?.pl_voiceover ?? 'unknown') : 'unknown',
			packaging: movie ? (e?.packaging ?? []) : [],
			binding: !movie && f.binding ? f.binding : null,
			notes: ''
		}
	};
}

function scanCopyPayload(f: QuickForm, editionID: number) {
	return copyPayload(
		{
			status: 'owned',
			price: f.price,
			purchased_on: '',
			shop: f.shop,
			acquired_as: f.acquired_as,
			condition: '',
			notes: ''
		},
		editionID
	);
}

/** A form value the server would refuse, caught before the first request. */
export class FormRejected extends Error {
	constructor(
		readonly field: string,
		message: string
	) {
		super(message);
	}
}

function must<T>(p: Parsed<T>): T {
	if (!p.ok) throw new FormRejected(p.issue.field, p.issue.message);
	return p.value;
}

export type Saved = { titleID: number; titleName: string; editionID: number; copy: Copy };

/** What a quick confirm saves: a copy of an owned edition, or a new item for a barcode. */
export type SaveTarget =
	| { kind: 'owned'; edition: EditionDetail }
	| { kind: 'new'; barcode: string; candidate: Candidate | null };

async function existingTitle(fetchFn: typeof fetch, tmdbID: number | null): Promise<Title | null> {
	if (tmdbID === null) return null;
	const found = await request<Title[]>(fetchFn, 'GET', `/api/titles?tmdb_id=${tmdbID}`);
	return found[0] ?? null;
}

async function editionByBarcode(fetchFn: typeof fetch, barcode: string): Promise<Edition | null> {
	const found = await request<Edition[]>(
		fetchFn,
		'GET',
		`/api/editions?barcode=${encodeURIComponent(barcode)}`
	);
	return found[0] ?? null;
}

async function discardTitle(fetchFn: typeof fetch, id: number) {
	try {
		await request<void>(fetchFn, 'DELETE', `/api/titles/${id}`);
	} catch {
		return;
	}
}

/**
 * Saves a quick confirm as an owned copy. A new barcode first gets its title, reusing the one
 * with the candidate's TMDB ID, then its edition; when another save created that edition in the
 * meantime, the copy joins it and a title made here is removed again. Every value is checked
 * before the first request, so a refused form changes nothing.
 */
export async function saveScan(
	fetchFn: typeof fetch,
	target: SaveTarget,
	f: QuickForm
): Promise<Saved> {
	if (target.kind === 'owned') {
		const e = target.edition;
		const copy = await request<Copy>(
			fetchFn,
			'POST',
			'/api/copies',
			must(scanCopyPayload(f, e.id))
		);
		return { titleID: e.title.id, titleName: e.title.name, editionID: e.id, copy };
	}

	const titleBody = must(scanTitlePayload(f, target.candidate));
	must(scanEditionPayload(f, target.candidate, target.barcode, 0));
	must(scanCopyPayload(f, 0));

	const reused = await existingTitle(fetchFn, titleBody.tmdb_id);
	const title = reused ?? (await request<Title>(fetchFn, 'POST', '/api/titles', titleBody));
	let edition: Edition;
	let titleID = title.id;
	let titleName = title.name;
	try {
		edition = await request<Edition>(
			fetchFn,
			'POST',
			'/api/editions',
			must(scanEditionPayload(f, target.candidate, target.barcode, title.id))
		);
	} catch (err) {
		const raced = err instanceof ApiError && err.status === 409 && err.field === 'barcode';
		const existing = raced ? await editionByBarcode(fetchFn, target.barcode) : null;
		if (!reused) await discardTitle(fetchFn, title.id);
		if (!existing) throw err;
		edition = existing;
		const owner = await request<Title>(fetchFn, 'GET', `/api/titles/${existing.title_id}`);
		titleID = owner.id;
		titleName = owner.name;
	}
	const copy = await request<Copy>(
		fetchFn,
		'POST',
		'/api/copies',
		must(scanCopyPayload(f, edition.id))
	);
	return { titleID, titleName, editionID: edition.id, copy };
}

/**
 * Ignores the barcode just handled while it stays in front of the camera, so one box is not
 * added twice. Each sighting of that barcode within windowMs of the last one extends the wait;
 * another barcode, or the same one after windowMs out of view, goes through.
 */
export class RepeatGuard {
	#code = '';
	#seen = -Infinity;

	constructor(readonly windowMs: number) {}

	/** Marks code as handled at now. */
	remember(code: string, now: number) {
		this.#code = code;
		this.#seen = now;
	}

	/** Whether code read at now is a new scan; a repeat counts as still in view. */
	accepts(code: string, now: number): boolean {
		if (code === this.#code && now - this.#seen < this.windowMs) {
			this.#seen = now;
			return false;
		}
		return true;
	}
}

/** The part of a video frame the reader decodes, scaled down for speed. */
export type Crop = { sx: number; sy: number; sw: number; sh: number; dw: number; dh: number };

/**
 * The middle band of a frame: the full width and half the height, where the aiming guide is,
 * scaled to at most maxWidth pixels wide.
 */
export function cropRect(width: number, height: number, maxWidth = 1280): Crop {
	const sh = Math.round(height / 2);
	const sy = Math.round((height - sh) / 2);
	const scale = Math.min(1, maxWidth / width);
	return {
		sx: 0,
		sy,
		sw: width,
		sh,
		dw: Math.round(width * scale),
		dh: Math.round(sh * scale)
	};
}

/** The Polish message for a getUserMedia failure. */
export function cameraErrorMessage(err: unknown): string {
	const name = err instanceof Error || err instanceof DOMException ? err.name : '';
	switch (name) {
		case 'NotAllowedError':
			return 'Brak zgody na użycie aparatu. Zezwól na nią w ustawieniach przeglądarki.';
		case 'NotFoundError':
		case 'OverconstrainedError':
			return 'Nie znaleziono aparatu.';
		case 'NotReadableError':
			return 'Aparat jest zajęty przez inną aplikację.';
		default:
			return 'Nie udało się włączyć aparatu.';
	}
}

/** The subset of Storage the scanner keeps its settings in. */
export type KeyValueStore = Pick<Storage, 'getItem' | 'setItem'>;

const stickyKey = 'ohara.scan.sticky';

/** The shop and new/used last used, or blanks when nothing usable is stored. */
export function loadSticky(store: KeyValueStore | undefined): Sticky {
	try {
		const raw = store?.getItem(stickyKey);
		const parsed: unknown = raw ? JSON.parse(raw) : null;
		if (!parsed || typeof parsed !== 'object') return { ...emptySticky };
		const { shop, acquired_as } = parsed as Record<string, unknown>;
		return {
			shop: typeof shop === 'string' ? shop : '',
			acquired_as: acquired_as === 'new' || acquired_as === 'used' ? acquired_as : ''
		};
	} catch {
		return { ...emptySticky };
	}
}

/** Keeps the shop and new/used for the next scan; storage that refuses is ignored. */
export function saveSticky(store: KeyValueStore | undefined, sticky: Sticky) {
	try {
		store?.setItem(stickyKey, JSON.stringify(sticky));
	} catch {
		return;
	}
}
