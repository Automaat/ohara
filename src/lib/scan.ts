import { ApiError, errorMessage, invalidBarcodeMessage, request } from './api';
import {
	copyPayload,
	displayName,
	formatFits,
	formatLabels,
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
	discogs_release_id?: number | null;
};

export type Candidate = {
	type: Kind;
	title: CandidateTitle;
	edition: CandidateEdition;
	cover_url?: string;
	provider: string;
	confidence: number;
};

/**
 * How the quick confirm lists a candidate: its name, for an album after the artist and followed
 * by the details pressings are told apart by, then the source and its confidence.
 */
export function candidateLabel(c: Candidate): string {
	const t = c.title;
	let name = displayName({
		name: t.name,
		polish_title: t.polish_title ?? null,
		original_title: t.original_title ?? null
	});
	const parts: string[] = [];
	if (c.type === 'book' && t.authors?.length) name = `${t.authors.join(', ')} – ${name}`;
	if (c.type !== 'music') {
		const year = t.year ?? c.edition.year;
		if (year) parts.push(String(year));
	}
	if (c.type === 'music') {
		if (t.authors?.length) name = `${t.authors.join(', ')} – ${name}`;
		const e = c.edition;
		for (const part of [
			e.format ? formatLabels[e.format] : '',
			e.year?.toString() ?? '',
			e.publisher ?? '',
			e.label ?? ''
		]) {
			if (part) parts.push(part);
		}
	}
	return [name, ...parts, c.provider, `${Math.round(c.confidence * 100)}%`].join(' · ');
}

/** What to type into the search by title, per kind. */
export const searchHints: Record<Kind, string> = {
	movie: 'Tytuł filmu, np. Łowca androidów. Wybrany film uzupełni pozycję.',
	book: 'Tytuł i autor, np. Solaris Lem. Wybrana książka uzupełni pozycję.',
	music: 'Wykonawca i tytuł, np. Taco Hemingway 1-800-Oświecenie. Wybrane wydanie uzupełni pozycję.'
};

/** What the search tells the user when its source is off or down. */
const searchFailures: Record<number, string> = {
	502: 'Źródło nie odpowiada, spróbuj za chwilę.',
	503: 'Wyszukiwanie dla tego typu jest wyłączone: brak klucza API.'
};

/**
 * Titles a search finds for text typed by hand (TMDB films, Google Books books, Discogs albums),
 * as candidates for the barcode being added. The picked format narrows an album search and stays
 * on a film's edition.
 */
export async function searchTitles(
	fetchFn: typeof fetch,
	kind: Kind,
	query: string,
	barcode: string,
	format: Format | ''
): Promise<Candidate[]> {
	const params = new URLSearchParams({ kind, q: query.trim() });
	if (barcode) params.set('barcode', barcode);
	if (format) params.set('format', format);
	try {
		const res = await request<{ candidates: Candidate[] }>(
			fetchFn,
			'GET',
			`/api/search?${params.toString()}`
		);
		return res.candidates;
	} catch (err) {
		const message = err instanceof ApiError ? searchFailures[err.status] : undefined;
		throw message ? new ApiError(message, (err as ApiError).status) : err;
	}
}

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

function digit(code: string, i: number): number {
	return code.charCodeAt(i) - 48;
}

/**
 * Whether a cleaned input is a barcode whose check digit matches, by the rules of the server's
 * barcode.Normalize: mod 11 with weights 10 to 1 for an ISBN-10, where X stands for 10, and
 * for EAN-8, UPC-A, EAN-13 and GTIN-14 the GS1 mod 10, whose weights 3 and 1 alternate from
 * the digit left of the check digit. The server still decides; this only spares a pointless
 * request.
 */
export function checkDigitMatches(code: string): boolean {
	if (!looksLikeBarcode(code)) return false;
	let sum = 0;
	if (code.length === 10) {
		for (let i = 0; i < 10; i++) sum += (code[i] === 'X' ? 10 : digit(code, i)) * (10 - i);
		return sum % 11 === 0;
	}
	for (let i = code.length - 2, weight = 3; i >= 0; i--, weight = 4 - weight) {
		sum += digit(code, i) * weight;
	}
	return (10 - (sum % 10)) % 10 === digit(code, code.length - 1);
}

/**
 * Why typed input cannot be opened: no barcode's shape, or a wrong check digit. Undefined when
 * the code can go to the add page.
 */
export function barcodeInputError(code: string): string | undefined {
	if (!looksLikeBarcode(code)) return 'Wpisz pełny kod kreskowy lub ISBN i spróbuj ponownie.';
	if (!checkDigitMatches(code)) return invalidBarcodeMessage;
	return undefined;
}

/** Whether a request failed because the server refused the barcode itself. */
export function isInvalidBarcode(err: unknown): boolean {
	return err instanceof ApiError && err.status === 422 && err.field === 'barcode';
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
	const book = f.kind === 'book';
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
			bn_id: book ? opt(t?.bn_id) : null,
			openlibrary_id: book ? opt(t?.openlibrary_id) : null,
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
	const music = f.kind === 'music';
	if ((movie || music) && !formatFits(f.kind, f.format)) {
		return { ok: false, issue: { field: 'format', message: 'Wybierz nośnik.' } };
	}
	const selected = candidate?.type === f.kind ? candidate : null;
	const e = selected?.edition;
	return {
		ok: true,
		value: {
			title_id: titleID,
			barcode,
			label: e?.label ?? '',
			publisher: e?.publisher ?? '',
			year: opt(e?.year),
			format: (movie || music) && f.format ? f.format : null,
			pl_subtitles: movie ? (e?.pl_subtitles ?? 'unknown') : 'unknown',
			pl_voiceover: movie ? (e?.pl_voiceover ?? 'unknown') : 'unknown',
			packaging: movie ? (e?.packaging ?? []) : [],
			binding: f.kind === 'book' && f.binding ? f.binding : null,
			discogs_release_id: music ? opt(e?.discogs_release_id) : null,
			cover_url: selected?.cover_url,
			notes: ''
		}
	};
}

function scanCopyPayload(f: QuickForm, editionID: number, requestKey: string) {
	const parsed = copyPayload(
		{
			status: 'owned',
			price: f.price,
			purchased_on: '',
			shop: f.shop,
			acquired_as: f.acquired_as,
			condition: '',
			notes: '',
			max_price: ''
		},
		editionID
	);
	return parsed.ok ? { ...parsed, value: { ...parsed.value, request_key: requestKey } } : parsed;
}

/**
 * A fresh key for one quick confirm. Sent as the copy's request_key, it makes a retried save
 * return the copy an earlier attempt created instead of adding a second one.
 */
export function newRequestKey(): string {
	const bytes = crypto.getRandomValues(new Uint8Array(16));
	return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('');
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
 * before the first request, so a refused form changes nothing. Retry a failed save with the
 * same requestKey: a copy the failed attempt created is returned rather than added again.
 */
export async function saveScan(
	fetchFn: typeof fetch,
	target: SaveTarget,
	f: QuickForm,
	requestKey: string
): Promise<Saved> {
	if (target.kind === 'owned') {
		const e = target.edition;
		const copy = await request<Copy>(
			fetchFn,
			'POST',
			'/api/copies?cover=auto',
			must(scanCopyPayload(f, e.id, requestKey))
		);
		return { titleID: e.title.id, titleName: e.title.name, editionID: e.id, copy };
	}

	const titleBody = must(scanTitlePayload(f, target.candidate));
	must(scanEditionPayload(f, target.candidate, target.barcode, 0));
	must(scanCopyPayload(f, 0, requestKey));

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
		'/api/copies?cover=auto',
		must(scanCopyPayload(f, edition.id, requestKey))
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

/**
 * Lets only the newest of overlapping loads apply its outcome: a page that moved on to another
 * barcode, or reloaded the same one, ignores what an earlier load answers, failures included.
 */
export class LatestLoad {
	#latest = 0;

	/** Starts a load; the returned check stays true until another load starts. */
	start(): () => boolean {
		const ticket = ++this.#latest;
		return () => ticket === this.#latest;
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
