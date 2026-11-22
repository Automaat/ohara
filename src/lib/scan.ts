import {
	ApiError,
	errorMessage,
	invalidBarcodeMessage,
	isNoAnswer,
	isSessionExpired,
	request
} from './api';
import { queueDeletesSettled } from './queue-deletes';
import { queueTally } from './queue-tally.svelte';
import {
	copyPayload,
	displayName,
	formatFits,
	parseSeason,
	screenKind,
	seasonName,
	bindingLabels,
	bookFormat,
	formatLabels,
	packagingLabels,
	triStateLabels,
	type AcquiredAs,
	type Binding,
	type Condition,
	type Copy,
	type CopyStatus,
	type Edition,
	type EditionDetail,
	type EditionPayload,
	type Format,
	type Kind,
	type Packaging,
	type Parsed,
	type Title,
	type TitlePayload
} from './collection';
import type {
	Candidate,
	LookupResult,
	QueueEntry,
	QueuePayload,
	QueueReason,
	SearchResult
} from './api.gen';

export type {
	Candidate,
	CandidateEdition,
	CandidateTitle,
	LookupResult,
	QueueEntry,
	QueueReason
} from './api.gen';

/**
 * A candidate's name, for a book or an album after its authors or artists, and the details that
 * tell it apart: the year, and for an album the medium, year, publisher and label.
 */
export function candidateParts(c: Candidate): { name: string; details: string[] } {
	const t = c.title;
	let name = displayName({
		name: t.name,
		polish_title: t.polish_title ?? null,
		original_title: t.original_title ?? null,
		season: t.season
	});
	if (!screenKind(c.type) && t.authors?.length) name = `${t.authors.join(', ')} – ${name}`;
	const details: string[] = [];
	if (c.type !== 'music') {
		const year = t.year ?? c.edition.year;
		if (year) details.push(String(year));
	} else {
		const e = c.edition;
		for (const part of [
			e.format ? formatLabels[e.format] : '',
			e.year?.toString() ?? '',
			e.publisher ?? '',
			e.label ?? ''
		]) {
			if (part) details.push(part);
		}
	}
	return { name, details };
}

/**
 * How the quick confirm lists a candidate: its name, for an album after the artist and followed
 * by the details pressings are told apart by, then the source and its confidence.
 */
export function candidateLabel(c: Candidate): string {
	const { name, details } = candidateParts(c);
	return [name, ...details, c.provider, `${Math.round(c.confidence * 100)}%`].join(' · ');
}

/** What to type into the search by title, per kind. */
export const searchHints: Record<Kind, string> = {
	movie: 'Tytuł filmu, np. Łowca androidów. Wybrany film uzupełni pozycję.',
	book: 'Tytuł i autor, np. Solaris Lem. Wybrana książka uzupełni pozycję.',
	music:
		'Wykonawca i tytuł, np. Taco Hemingway 1-800-Oświecenie. Wybrane wydanie uzupełni pozycję.',
	tv: 'Tytuł serialu, np. Dark. Wybrany sezon uzupełni pozycję.'
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
		const res = await request<SearchResult>(fetchFn, 'GET', `/api/search?${params.toString()}`);
		return res.candidates;
	} catch (err) {
		const message = err instanceof ApiError ? searchFailures[err.status] : undefined;
		throw message ? new ApiError(message, (err as ApiError).status) : err;
	}
}

/** The source a candidate came from, as the quick confirm names it. */
export function providerLabel(c: Candidate): string {
	switch (c.provider) {
		case 'movie':
		case 'tmdb':
			return 'TMDB';
		case 'music':
			return c.edition.discogs_release_id ? 'Discogs' : 'MusicBrainz';
		case 'discogs':
			return 'Discogs';
		case 'bn':
			return 'Biblioteka Narodowa';
		case 'openlibrary':
			return 'Open Library';
		case 'google_books':
			return 'Google Books';
		case 'selection':
			return 'Twój wybór';
		default:
			return c.provider;
	}
}

export const reasonLabels: Record<QueueReason, string> = {
	not_found: 'Nieznany kod',
	lookup_failed: 'Wyszukiwanie nie powiodło się'
};

/**
 * What the quick confirm says about a barcode it sent to the queue: why no source answered,
 * with the providers' errors when the lookup failed.
 */
export function queuedMessage(entry: Pick<QueueEntry, 'reason' | 'detail'>): string {
	if (entry.reason === 'not_found') {
		return 'Kod trafił do kolejki do uzupełnienia. Dodaj go ręcznie teraz albo później.';
	}
	const detail = entry.detail.trim().replace(/\.$/, '');
	const why = detail
		? `Nie udało się sprawdzić kodu (${detail}).`
		: 'Nie udało się sprawdzić kodu.';
	return `${why} Kod trafił do kolejki do uzupełnienia.`;
}

/**
 * A stored barcode cut into the groups a reader compares with the digits under the bars:
 * 1-6-6 for an EAN-13, 3-4-6 for a book's 978 or 979 code, 4-4 for an EAN-8, 1-5-5-1 for a
 * UPC-A and 1-2-5-5-1 for a GTIN-14. Anything else stays one group.
 */
export function barcodeGroups(code: string): string[] {
	if (!/^\d+$/.test(code)) return [code];
	const sizes: Record<number, number[]> = {
		8: [4, 4],
		12: [1, 5, 5, 1],
		13: /^97[89]/.test(code) ? [3, 4, 6] : [1, 6, 6],
		14: [1, 2, 5, 5, 1]
	};
	const groups = sizes[code.length];
	if (!groups) return [code];
	let at = 0;
	return groups.map((size) => code.slice(at, (at += size)));
}

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

/** The GS1 check digit for the digits of a code before its check digit. */
function gs1CheckDigit(body: string): string {
	let sum = 0;
	for (let i = body.length - 1, weight = 3; i >= 0; i--, weight = 4 - weight) {
		sum += digit(body, i) * weight;
	}
	return String((10 - (sum % 10)) % 10);
}

/**
 * The form the server stores a barcode in, by the rules of barcode.Normalize: an ISBN-10 becomes
 * its ISBN-13, a UPC-A its EAN-13 with a leading 0, and any code padded to 14 digits loses the
 * leading zeros down to an EAN-13, or to an EAN-8 when six lead. A code whose check digit does not
 * match stays as it is.
 */
export function canonicalBarcode(code: string): string {
	if (!checkDigitMatches(code)) return code;
	if (code.length === 10) {
		const body = `978${code.slice(0, 9)}`;
		return body + gs1CheckDigit(body);
	}
	const padded = code.padStart(14, '0');
	if (padded.startsWith('000000')) return padded.slice(6);
	return padded.startsWith('0') ? padded.slice(1) : padded;
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
 * price. An ISBN-10, which may end in X, counts only when its check digit matches.
 */
export function wedgeBarcodeIn(price: string): string | null {
	const code = cleanBarcode(price);
	if (!looksLikeBarcode(code)) return null;
	return code.length !== 10 || checkDigitMatches(code) ? code : null;
}

/**
 * What a scan resolved to: an edition already in the collection, candidates, or the queue. Found
 * candidates need a pick unless the lookup trusts its first one.
 */
export type Resolution =
	| { kind: 'owned'; barcode: string; edition: EditionDetail }
	| {
			kind: 'found';
			barcode: string;
			candidates: Candidate[];
			needsConfirmation: boolean;
			errors: LookupResult['errors'];
	  }
	| { kind: 'queued'; barcode: string; entry: QueueEntry };

function providerDetail(errors: LookupResult['errors']): string {
	return errors.map((e) => `${e.provider}: ${e.error}`).join('; ');
}

/**
 * Resolves a scanned barcode: an edition that already carries it, else the lookup's candidates,
 * else a scan queue entry so the barcode is not lost. Throws an ApiError for a malformed
 * barcode, when the sign-in turns a request away, when not even the queue answers, or when the
 * lookup gets no answer (status 0, a lost connection or the request deadline): nothing was learned
 * about the code, so the caller handles it as a check that got no answer. A recheck asks each
 * source again when its cached answer is over an hour old.
 */
export async function resolveScan(
	fetchFn: typeof fetch,
	raw: string,
	recheck = false
): Promise<Resolution> {
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
		const query = recheck ? '?recheck=1' : '';
		lookup = await request<LookupResult>(
			fetchFn,
			'GET',
			`/api/lookup/${encodeURIComponent(code)}${query}`
		);
	} catch (err) {
		if (isSessionExpired(err) || isNoAnswer(err)) throw err;
		failure = errorMessage(err);
	}
	if (lookup && lookup.candidates.length > 0) {
		return {
			kind: 'found',
			barcode: lookup.barcode,
			candidates: lookup.candidates,
			needsConfirmation: lookup.needs_confirmation !== false,
			errors: lookup.errors
		};
	}
	const barcode = lookup?.barcode ?? code;
	const failed = !lookup || lookup.errors.length > 0;
	await queueDeletesSettled();
	const queued: QueuePayload = {
		barcode,
		reason: failed ? 'lookup_failed' : 'not_found',
		detail: lookup ? providerDetail(lookup.errors) : failure
	};
	const entry = await request<QueueEntry>(fetchFn, 'POST', '/api/scan-queue', queued);
	queueTally.queued(entry.barcode);
	return { kind: 'queued', barcode, entry };
}

/**
 * The fields the quick confirm asks for; the rest come from the candidate. An owned copy takes
 * the price as what it cost, a wishlist copy as the most it may cost.
 */
export type QuickForm = {
	kind: Kind;
	name: string;
	/** A TV title's season, 0 for specials; empty or absent for other kinds. */
	season?: string;
	format: Format | '';
	binding: Binding | '';
	packaging: Packaging[];
	status: CopyStatus;
	price: string;
	purchased_on: string;
	shop: string;
	acquired_as: AcquiredAs | '';
	condition: Condition | '';
	/** A wish of a movie or book matches only listings naming its edition's details. */
	exact_edition: boolean;
};

/**
 * What carries over from one scan to the next during bulk entry: the shop and new or used of the
 * last copy added, and the shops used lately, newest first.
 */
export type Sticky = { shop: string; acquired_as: AcquiredAs | ''; shops: string[] };

export const emptySticky: Sticky = { shop: '', acquired_as: '', shops: [] };

/** How many recently used shops the quick confirm offers. */
export const recentShopLimit = 6;

/** The recent shops with shop moved to the front; a blank shop changes nothing. */
export function rememberShop(shops: readonly string[], shop: string): string[] {
	const name = shop.trim();
	if (!name) return [...shops];
	const key = name.toLocaleLowerCase('pl-PL');
	return [name, ...shops.filter((s) => s.toLocaleLowerCase('pl-PL') !== key)].slice(
		0,
		recentShopLimit
	);
}

/** What the next scan starts from after f was added: a wish leaves the purchase defaults alone. */
export function stickyAfter(sticky: Sticky, f: QuickForm): Sticky {
	if (f.status !== 'owned') return sticky;
	const shop = f.shop.trim();
	return { shop, acquired_as: f.acquired_as, shops: rememberShop(sticky.shops, shop) };
}

export { isoDate } from './collection';

/**
 * The quick confirm for a candidate, or a blank one for a barcode added by hand. The price
 * always starts empty; the date is the one the visit last used.
 */
export function quickForm(
	candidate: Candidate | null,
	sticky: Sticky,
	barcode: string,
	date = ''
): QuickForm {
	return {
		kind: candidate?.type ?? (isBookBarcode(barcode) ? 'book' : 'movie'),
		name: candidate?.title.name ?? '',
		season: candidate?.title.season?.toString(),
		format: candidate?.edition.format ?? '',
		binding: candidate?.edition.binding ?? '',
		packaging: [...(candidate?.edition.packaging ?? [])],
		status: 'owned',
		price: '',
		purchased_on: date,
		shop: sticky.shop,
		acquired_as: sticky.acquired_as,
		condition: '',
		exact_edition: false
	};
}

/** The longest text the API takes in a short field (a name, label or shop), in UTF-8 bytes. */
const maxTextBytes = 500;
/** The longest cover URL the API takes, in UTF-8 bytes. */
const maxURLBytes = 10000;
/** The most authors the API takes for one title. */
const maxAuthors = 20;

function byteLength(text: string): number {
	return new TextEncoder().encode(text).length;
}

/** Trims like Go's strings.TrimSpace, which also takes U+0085 (next line) for a space. */
function trimSpace(text: string): string {
	return text.replace(/^[\s\u0085]+|[\s\u0085]+$/gu, '');
}

function tooLong(text: string): boolean {
	return byteLength(trimSpace(text)) > maxTextBytes;
}

/*
 * A provider's values as the API takes them, none for one it would refuse: the form shows none of
 * these, so a refused one would only make the save fail.
 */

function fitText(text: string | null | undefined): string | null {
	const t = text == null ? '' : trimSpace(text);
	return t && !t.includes('\0') && byteLength(t) <= maxTextBytes ? t : null;
}

function fitYear(year: number | null | undefined): number | null {
	return year != null && Number.isInteger(year) && year >= 1800 && year <= 2200 ? year : null;
}

function fitID(id: number | null | undefined): number | null {
	return id != null && Number.isSafeInteger(id) && id > 0 ? id : null;
}

/**
 * A candidate's series as the API takes it: a one-line name and, only with one, a whole number
 * from 1 to 10000. Anything else is left out, so the save keeps the rest of the pick.
 */
function fitSeries(
	name: string | null | undefined,
	number: number | null | undefined
): Pick<TitlePayload, 'series_name' | 'series_number'> {
	const fitName = fitText(name);
	if (fitName === null || /\p{Cc}/u.test(fitName)) return {};
	const fitNumber =
		number != null && Number.isInteger(number) && number >= 1 && number <= 10000 ? number : null;
	return { series_name: fitName, series_number: fitNumber };
}

function fitAuthors(authors: readonly string[] | null | undefined): string[] {
	const fit = (authors ?? []).map(trimSpace).filter(Boolean);
	return fit.length <= maxAuthors && fit.every((a) => fitText(a) !== null) ? fit : [];
}

/** A value from labels' keys, as the API's enum check takes it; none for another. */
function fitEnum<K extends string>(
	labels: Record<K, string>,
	value: string | null | undefined
): K | null {
	const v = value == null ? '' : trimSpace(value);
	return Object.hasOwn(labels, v) ? (v as K) : null;
}

/**
 * An HTTP or HTTPS URL with a host and no credentials, in a stricter form than the API's check
 * takes: a plain host name, an optional port and RFC 3986 characters with well-formed escapes.
 * A browser's URL parser takes more than Go's (a bad escape, a missing slash), so it cannot tell.
 */
const coverURL =
	/^https?:\/\/[a-z0-9._~-]+(?::\d+)?(?:[/?#](?:[\w\-.~:/?#[\]@!$&'()*+,;=]|%[0-9a-f]{2}|\P{ASCII})*)?$/iu;

function fitCoverURL(value: string | null | undefined): string | undefined {
	const raw = value == null ? '' : trimSpace(value);
	return raw && byteLength(raw) <= maxURLBytes && coverURL.test(raw) ? raw : undefined;
}

/**
 * Whether the form still names the picked candidate's season. A TV pick whose season was changed
 * is another season of the same show: the show's names and TMDB ID still fit it, the picked
 * season's year and poster do not.
 */
export function sameSeason(f: QuickForm, candidate: Candidate | null): boolean {
	if (f.kind !== 'tv') return true;
	const typed = trimSpace(f.season ?? '');
	return /^\d+$/.test(typed) && Number(typed) === candidate?.title.season;
}

/** The POST /api/titles body: the form's kind and name over the candidate's other fields. */
export function scanTitlePayload(f: QuickForm, candidate: Candidate | null): Parsed<TitlePayload> {
	// Postgres text takes no NUL, which a provider's name can carry into the form.
	const name = trimSpace(f.name.replaceAll('\0', ''));
	if (!name) return { ok: false, issue: { field: 'name', message: 'Podaj tytuł.' } };
	const t = candidate?.type === f.kind ? candidate.title : null;
	const movie = screenKind(f.kind);
	const book = f.kind === 'book';
	const season = parseSeason(f.kind, f.season ?? '');
	if (!season.ok) return season;
	return {
		ok: true,
		value: {
			kind: f.kind,
			name,
			original_title: fitText(t?.original_title),
			polish_title: fitText(t?.polish_title),
			year: sameSeason(f, candidate) ? fitYear(t?.year) : null,
			tmdb_id: movie ? fitID(t?.tmdb_id) : null,
			season: season.value ?? undefined,
			authors: movie ? [] : fitAuthors(t?.authors),
			bn_id: book ? fitText(t?.bn_id) : null,
			openlibrary_id: book ? fitText(t?.openlibrary_id) : null,
			notes: '',
			...fitSeries(t?.series_name, t?.series_number)
		}
	};
}

/**
 * The POST /api/editions body for barcode, none when it is empty: the form's medium over the
 * candidate's details.
 */
export function scanEditionPayload(
	f: QuickForm,
	candidate: Candidate | null,
	barcode: string,
	titleID: number
): Parsed<EditionPayload> {
	const movie = screenKind(f.kind);
	const music = f.kind === 'music';
	if ((movie || music) && !formatFits(f.kind, f.format)) {
		return { ok: false, issue: { field: 'format', message: 'Wybierz nośnik.' } };
	}
	const book = f.kind === 'book';
	const audiobook = book ? bookFormat(f.format) : null;
	const selected = candidate?.type === f.kind ? candidate : null;
	const e = selected?.edition;
	return {
		ok: true,
		value: {
			title_id: titleID,
			barcode: barcode || null,
			label: fitText(e?.label) ?? '',
			publisher: fitText(e?.publisher) ?? '',
			year: fitYear(e?.year),
			format: (movie || music) && f.format ? f.format : audiobook,
			pl_subtitles: (movie && fitEnum(triStateLabels, e?.pl_subtitles)) || 'unknown',
			pl_voiceover: (movie && fitEnum(triStateLabels, e?.pl_voiceover)) || 'unknown',
			// The form takes these from the pick without a choice the user made.
			packaging: movie
				? f.packaging.flatMap((p) => fitEnum<Packaging>(packagingLabels, p) ?? [])
				: [],
			// An audiobook has no binding, even one a catalog named for its ISBN.
			binding: book && !audiobook ? fitEnum<Binding>(bindingLabels, f.binding) : null,
			// The release is one pressing on its own medium; another or an unknown medium is not it.
			discogs_release_id: music && e?.format === f.format ? fitID(e?.discogs_release_id) : null,
			cover_url: sameSeason(f, selected) ? fitCoverURL(selected?.cover_url) : undefined,
			notes: ''
		}
	};
}

/**
 * The POST /api/copies body. A wish keeps only its price limit and, unless it is an album,
 * whether it takes only its edition: no purchase date, shop, new or used, or condition. The
 * condition goes with a used copy alone.
 */
export function scanCopyPayload(f: QuickForm, editionID: number, requestKey: string) {
	const owned = f.status === 'owned';
	const parsed = copyPayload(
		{
			status: f.status,
			price: owned ? f.price : '',
			purchased_on: owned ? f.purchased_on : '',
			shop: owned ? f.shop : '',
			acquired_as: owned ? f.acquired_as : '',
			condition: owned && f.acquired_as === 'used' ? f.condition : '',
			media_grade: '',
			sleeve_grade: '',
			signed: false,
			copy_number: '',
			notes: '',
			max_price: owned ? '' : f.price,
			market_percent: '',
			exact_edition: f.kind !== 'music' && f.exact_edition,
			sold_on: '',
			sale_price: '',
			expected_on: '',
			location: ''
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

/**
 * What a save went through to: the copy, the edition it joined and its title, and whether this
 * add created the edition or the title rather than finding them, so undoing it removes only what
 * it made.
 */
export type Saved = {
	titleID: number;
	titleName: string;
	kind: Kind;
	editionID: number;
	edition: Edition;
	/** The artwork the edition shows: its own, else its title's. */
	cover?: string;
	copy: Copy;
	createdTitle: boolean;
	createdEdition: boolean;
};

/**
 * What one add did on the server across its attempts: the titles and editions it created that
 * still exist, and whether a title request of an earlier attempt never answered. A retried save
 * passes the same record.
 */
export type Made = {
	titles: number[];
	editions: number[];
	titleUnanswered: boolean;
};

export function newMade(): Made {
	return { titles: [], editions: [], titleUnanswered: false };
}

/**
 * What a quick confirm saves: a copy of an owned edition, or a new item for a barcode, empty for
 * one added without.
 */
export type SaveTarget =
	| { kind: 'owned'; edition: EditionDetail }
	| { kind: 'new'; barcode: string; candidate: Candidate | null };

/** The title of body's kind with its TMDB ID and, for a TV title, its season. */
async function existingTitle(fetchFn: typeof fetch, body: TitlePayload): Promise<Title | null> {
	if (body.tmdb_id == null) return null;
	const found = await request<Title[]>(fetchFn, 'GET', `/api/titles?tmdb_id=${body.tmdb_id}`);
	return (
		found.find(
			(t) => (t.kind ?? body.kind) === body.kind && (t.season ?? null) === (body.season ?? null)
		) ?? null
	);
}

async function editionByBarcode(fetchFn: typeof fetch, barcode: string): Promise<Edition | null> {
	const found = await request<Edition[]>(
		fetchFn,
		'GET',
		`/api/editions?barcode=${encodeURIComponent(barcode)}`
	);
	return found[0] ?? null;
}

/** Deletes a title or an edition an add made; true once it is gone. */
async function discard(fetchFn: typeof fetch, path: string): Promise<boolean> {
	try {
		await request<void>(fetchFn, 'DELETE', path);
		return true;
	} catch (err) {
		return err instanceof ApiError && err.status === 404;
	}
}

/**
 * Asks the server to remove what the creates sent with requestKey made, for an add without a
 * barcode that failed: its copy, then its edition and title unless someone else uses or changed
 * them. Throws when the server did not confirm, so nothing may be saved over it yet.
 */
export async function discardRequest(fetchFn: typeof fetch, requestKey: string): Promise<void> {
	await request<void>(fetchFn, 'DELETE', `/api/requests/${encodeURIComponent(requestKey)}`);
}

function remember(ids: number[], id: number) {
	if (!ids.includes(id)) ids.push(id);
}

function forget(ids: number[], id: number) {
	const at = ids.indexOf(id);
	if (at !== -1) ids.splice(at, 1);
}

/**
 * The title a new barcode goes under: the one with the candidate's TMDB ID, else a new one sent
 * with the add's key, so the server answers a retry with the title an unanswered attempt created.
 * After an unanswered title request the TMDB lookup is skipped: the title it finds could be the
 * add's own, and only the key tells.
 */
async function titleFor(
	fetchFn: typeof fetch,
	body: TitlePayload,
	requestKey: string,
	made: Made
): Promise<Title> {
	if (!made.titleUnanswered) {
		const reused = await existingTitle(fetchFn, body);
		if (reused) return reused;
	}
	made.titleUnanswered = true;
	try {
		const title = await postTitle(fetchFn, body, requestKey);
		made.titleUnanswered = false;
		remember(made.titles, title.id);
		return title;
	} catch (err) {
		const taken = err instanceof ApiError && err.status === 409 && err.field === 'tmdb_id';
		const reused = taken ? await existingTitle(fetchFn, body) : null;
		if (!reused) throw err;
		made.titleUnanswered = false;
		return reused;
	}
}

/**
 * Creates the title. A series number another title of the series already holds (a reprint under a
 * new ISBN, or the same book saved at once from another tab) leaves the new title in the series
 * without one, so the save goes on: a lookup never takes a number from a title the reader keeps.
 */
async function postTitle(fetchFn: typeof fetch, body: TitlePayload, requestKey: string) {
	const send = (b: TitlePayload) =>
		request<Title>(fetchFn, 'POST', '/api/titles', { ...b, request_key: requestKey });
	try {
		return await send(body);
	} catch (err) {
		const numberTaken =
			err instanceof ApiError && err.status === 409 && err.field === 'series_number';
		if (!numberTaken || body.series_number == null) throw err;
		return send({ ...body, series_number: null });
	}
}

/** Throws FormRejected for a new item's form the server would refuse, before any request. */
export function checkScan(target: Extract<SaveTarget, { kind: 'new' }>, f: QuickForm): void {
	must(scanTitlePayload(f, target.candidate));
	must(scanEditionPayload(f, target.candidate, target.barcode, 0));
	must(scanCopyPayload(f, 0, ''));
	if (tooLong(f.name)) throw new FormRejected('name', 'Tytuł jest za długi.');
	if (f.status === 'owned' && tooLong(f.shop)) {
		throw new FormRejected('shop', 'Nazwa sklepu jest za długa.');
	}
}

/**
 * Saves a quick confirm as an owned or wishlist copy. A new barcode first gets its title, reusing
 * the one with the candidate's TMDB ID, then its edition; when another save created that edition
 * in the meantime, the copy joins it and a title made here is removed again. Every value is
 * checked before the first request, so a refused form changes nothing. Retry a failed save with
 * the same requestKey and made: the server answers the title, edition and copy a failed attempt
 * created instead of adding them again, and they still count as created by this add.
 */
export async function saveScan(
	fetchFn: typeof fetch,
	target: SaveTarget,
	f: QuickForm,
	requestKey: string,
	made: Made = newMade()
): Promise<Saved> {
	const saved = (
		title: Pick<Title, 'id' | 'name' | 'kind' | 'cover_url' | 'season'>,
		edition: Edition,
		copy: Copy
	): Saved => {
		const createdEdition = made.editions.includes(edition.id);
		return {
			titleID: title.id,
			titleName: seasonName(title.name, title.season),
			kind: title.kind,
			editionID: edition.id,
			edition,
			cover: edition.cover_override_url || edition.cover_url || title.cover_url || undefined,
			copy,
			createdTitle: made.titles.includes(title.id),
			createdEdition
		};
	};
	if (target.kind === 'owned') {
		const e = target.edition;
		const copy = await request<Copy>(
			fetchFn,
			'POST',
			'/api/copies?cover=auto',
			must(scanCopyPayload(f, e.id, requestKey))
		);
		return saved(e.title, e, copy);
	}

	checkScan(target, f);
	const titleBody = must(scanTitlePayload(f, target.candidate));

	const title = await titleFor(fetchFn, titleBody, requestKey, made);
	let edition: Edition;
	let owner: Title = title;
	try {
		edition = await request<Edition>(fetchFn, 'POST', '/api/editions', {
			...must(scanEditionPayload(f, target.candidate, target.barcode, title.id)),
			request_key: requestKey
		});
		remember(made.editions, edition.id);
		queueTally.resolved(edition.id, edition.barcode);
	} catch (err) {
		const raced = err instanceof ApiError && err.status === 409 && err.field === 'barcode';
		const existing = raced ? await editionByBarcode(fetchFn, target.barcode) : null;
		if (made.titles.includes(title.id) && (await discard(fetchFn, `/api/titles/${title.id}`))) {
			forget(made.titles, title.id);
		}
		if (!existing) throw err;
		edition = existing;
	}
	// A retry's key answers the edition an earlier attempt made, under the title it took then.
	if (edition.title_id !== title.id) {
		owner = await request<Title>(fetchFn, 'GET', `/api/titles/${edition.title_id}`);
	}
	const copy = await request<Copy>(
		fetchFn,
		'POST',
		'/api/copies?cover=auto',
		must(scanCopyPayload(f, edition.id, requestKey))
	);
	return saved(owner, edition, copy);
}

/**
 * Ignores the barcodes just handled while they stay in front of the camera, so one box is not
 * added twice. Each sighting of such a barcode within windowMs of the last one extends its wait;
 * another barcode, or the same one after windowMs out of view, goes through.
 */
export class RepeatGuard {
	readonly #seen = new Map<string, number>();

	constructor(readonly windowMs: number) {}

	/** Marks code as handled at now. */
	remember(code: string, now: number) {
		this.#seen.set(code, now);
	}

	/** Whether code read at now is a new scan; a repeat counts as still in view. */
	accepts(code: string, now: number): boolean {
		for (const [seen, at] of this.#seen) {
			if (now - at >= this.windowMs) this.#seen.delete(seen);
		}
		if (!this.#seen.has(code)) return true;
		this.#seen.set(code, now);
		return false;
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

/** A rectangle on the screen, in CSS pixels, as getBoundingClientRect measures it. */
export type Box = { left: number; top: number; width: number; height: number };

/**
 * The part of a video frame that shows under the scan band, scaled to at most maxWidth pixels
 * wide. The video fills `view` as `object-fit: cover` draws it: scaled to cover, centred and
 * cut at the edges. Null when the band covers none of the frame.
 */
export function bandCrop(
	videoWidth: number,
	videoHeight: number,
	view: Box,
	band: Box,
	maxWidth = 1280
): Crop | null {
	if (videoWidth <= 0 || videoHeight <= 0 || view.width <= 0 || view.height <= 0) return null;
	const scale = Math.max(view.width / videoWidth, view.height / videoHeight);
	const left = view.left + (view.width - videoWidth * scale) / 2;
	const top = view.top + (view.height - videoHeight * scale) / 2;
	const sx = Math.round(Math.max(0, (band.left - left) / scale));
	const sy = Math.round(Math.max(0, (band.top - top) / scale));
	const sw = Math.round(Math.min(videoWidth, (band.left + band.width - left) / scale)) - sx;
	const sh = Math.round(Math.min(videoHeight, (band.top + band.height - top) / scale)) - sy;
	if (sw <= 0 || sh <= 0) return null;
	const k = Math.min(1, maxWidth / sw);
	return {
		sx,
		sy,
		sw,
		sh,
		dw: Math.max(1, Math.round(sw * k)),
		dh: Math.max(1, Math.round(sh * k))
	};
}

/**
 * Whether a camera track's capabilities offer a torch: `true` in the Image Capture spec, a list
 * of the settings it takes in some browsers.
 */
export function torchSupported(capabilities: object | undefined): boolean {
	if (!capabilities || !('torch' in capabilities)) return false;
	const torch = capabilities.torch;
	return torch === true || (Array.isArray(torch) && torch.includes(true));
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

/** The browser's local storage, or none where it is off, as in a private window. */
export function browserStore(): KeyValueStore | undefined {
	try {
		return localStorage;
	} catch {
		return undefined;
	}
}

const stickyKey = 'ohara.scan.sticky';

/** The shop and new/used last used and the recent shops, or blanks when nothing usable is stored. */
export function loadSticky(store: KeyValueStore | undefined): Sticky {
	try {
		const raw = store?.getItem(stickyKey);
		const parsed: unknown = raw ? JSON.parse(raw) : null;
		if (!parsed || typeof parsed !== 'object') return { ...emptySticky, shops: [] };
		const { shop, acquired_as, shops } = parsed as Record<string, unknown>;
		const last = typeof shop === 'string' ? shop : '';
		const recent = Array.isArray(shops)
			? shops
					.filter((s): s is string => typeof s === 'string')
					.reduceRight<string[]>((list, s) => rememberShop(list, s), [])
			: [];
		return {
			shop: last,
			acquired_as: acquired_as === 'new' || acquired_as === 'used' ? acquired_as : '',
			shops: recent.length > 0 ? recent : rememberShop([], last)
		};
	} catch {
		return { ...emptySticky, shops: [] };
	}
}

/** Keeps the shop, new/used and recent shops for the next scan; storage that refuses is ignored. */
export function saveSticky(store: KeyValueStore | undefined, sticky: Sticky) {
	try {
		store?.setItem(stickyKey, JSON.stringify(sticky));
	} catch {
		return;
	}
}
