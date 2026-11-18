export type Kind = 'movie' | 'book' | 'music';
export type MovieFormat = 'dvd' | 'bluray' | 'uhd';
export type MusicFormat = 'cd' | 'vinyl' | 'cassette';
export type Format = MovieFormat | MusicFormat;
export type Binding = 'hardcover' | 'paperback' | 'other';
export type TriState = 'yes' | 'no' | 'unknown';
export type Packaging = 'steelbook' | 'slipcover' | 'digipak' | 'mediabook';
export type CopyStatus = 'owned' | 'wishlist';
export type AcquiredAs = 'new' | 'used';
export type Condition = 'mint' | 'very_good' | 'good' | 'fair' | 'poor';

export type TitlePayload = {
	kind: Kind;
	name: string;
	original_title: string | null;
	polish_title: string | null;
	year: number | null;
	tmdb_id: number | null;
	authors: string[];
	bn_id: string | null;
	openlibrary_id: string | null;
	notes: string;
};

export type Title = TitlePayload & {
	id: number;
	edition_count: number;
	copy_count: number;
	created_at: string;
	updated_at: string;
};

export type EditionPayload = {
	title_id: number;
	barcode: string | null;
	label: string;
	publisher: string;
	year: number | null;
	format: Format | null;
	pl_subtitles: TriState;
	pl_voiceover: TriState;
	packaging: Packaging[];
	binding: Binding | null;
	discogs_release_id: number | null;
	notes: string;
};

export type Edition = EditionPayload & {
	id: number;
	copy_count: number;
	created_at: string;
	updated_at: string;
};

export type CopyPayload = {
	edition_id: number;
	status: CopyStatus;
	price_cents: number | null;
	purchased_on: string | null;
	shop: string;
	acquired_as: AcquiredAs | null;
	condition: Condition | null;
	notes: string;
	max_price_cents: number | null;
};

export type Copy = CopyPayload & { id: number; created_at: string; updated_at: string };

export type TitleDetail = Title & { editions: (Edition & { copies: Copy[] })[] };
export type EditionDetail = Edition & { title: Title; copies: Copy[] };
export type CopyDetail = Copy & { edition: Edition; title: Title };

export type CollectionItem = {
	copy_id: number;
	status: CopyStatus;
	price_cents: number | null;
	purchased_on: string | null;
	shop: string;
	acquired_as: AcquiredAs | null;
	condition: Condition | null;
	edition_id: number;
	barcode: string | null;
	label: string;
	format: Format | null;
	binding: Binding | null;
	title_id: number;
	kind: Kind;
	name: string;
	original_title: string | null;
	polish_title: string | null;
	year: number | null;
};

export type Totals = {
	copies: number;
	owned: number;
	wishlist: number;
	priced: number;
	spent_cents: number;
};

export type CollectionView = { items: CollectionItem[]; totals: Totals };

export const kindLabels: Record<Kind, string> = { movie: 'Film', book: 'Książka', music: 'Muzyka' };
export const movieFormatLabels: Record<MovieFormat, string> = {
	dvd: 'DVD',
	bluray: 'Blu-ray',
	uhd: '4K UHD'
};
export const musicFormatLabels: Record<MusicFormat, string> = {
	cd: 'CD',
	vinyl: 'Winyl',
	cassette: 'Kaseta'
};
export const formatLabels: Record<Format, string> = { ...movieFormatLabels, ...musicFormatLabels };

/** The formats a title of kind comes in, as select options; books have none. */
export function formatOptions(kind: Kind): { value: Format; label: string }[] {
	if (kind === 'movie') return options(movieFormatLabels);
	if (kind === 'music') return options(musicFormatLabels);
	return [];
}

/** Whether a format belongs to a title of kind. */
export function formatFits(kind: Kind, format: Format | '' | null | undefined): boolean {
	return formatOptions(kind).some((o) => o.value === format);
}

/** Who a title of kind is by: a book's authors or an album's artists. */
export function authorsLabel(kind: Kind): string {
	return kind === 'music' ? 'Wykonawcy' : 'Autorzy';
}
export const bindingLabels: Record<Binding, string> = {
	hardcover: 'Twarda oprawa',
	paperback: 'Miękka oprawa',
	other: 'Inna oprawa'
};
export const triStateLabels: Record<TriState, string> = {
	yes: 'Tak',
	no: 'Nie',
	unknown: 'Nie wiadomo'
};
export const packagingLabels: Record<Packaging, string> = {
	steelbook: 'Steelbook',
	slipcover: 'Slipcover',
	digipak: 'Digipak',
	mediabook: 'Mediabook'
};
export const statusLabels: Record<CopyStatus, string> = {
	owned: 'Na półce',
	wishlist: 'Lista życzeń'
};
export const acquiredLabels: Record<AcquiredAs, string> = { new: 'Nowy', used: 'Używany' };
export const conditionLabels: Record<Condition, string> = {
	mint: 'Idealny',
	very_good: 'Bardzo dobry',
	good: 'Dobry',
	fair: 'Dostateczny',
	poor: 'Słaby'
};

/** Select or segment options from a label table, in its key order. */
export function options<K extends string>(
	labels: Record<K, string>
): { value: K; label: string }[] {
	return (Object.keys(labels) as K[]).map((value) => ({ value, label: labels[value] }));
}

/** The API's price cap: 1 000 000 zł. */
export const maxPriceCents = 100_000_000;

const pln = new Intl.NumberFormat('pl-PL', { style: 'currency', currency: 'PLN' });

/** Grosze as "49,99 zł"; null as an empty string. */
/** Hint under a purchase price field: blank, not 0, is an unknown price. */
export const priceHint = 'Nie pamiętasz? Zostaw puste. 0 zł liczy się jako cena.';

/** What owned copies cost, and how many still have no price to fill in. */
export function spentLabel(t: Totals): string {
	const unpriced = t.owned - t.priced;
	const spent = formatPLN(t.spent_cents);
	return unpriced > 0 ? `${spent} (${unpriced} bez ceny)` : spent;
}

export function formatPLN(cents: number | null | undefined): string {
	return cents == null ? '' : pln.format(cents / 100);
}

/** Grosze as the editable "49,99"; null as an empty string. */
export function centsToInput(cents: number | null): string {
	return cents == null ? '' : (cents / 100).toFixed(2).replace('.', ',');
}

/**
 * Parses a typed amount such as "49,99", "49.9" or "1 234,50 zł" into grosze. Blank is null;
 * anything else that is not an amount with at most two decimals is undefined.
 */
export function parsePLN(input: string): number | null | undefined {
	const cleaned = input.replace(/zł/i, '').replace(/[\s ]/g, '').replace(',', '.');
	if (cleaned === '') return null;
	if (!/^\d+(\.\d{1,2})?$/.test(cleaned)) return undefined;
	const cents = Math.round(Number(cleaned) * 100);
	return cents > maxPriceCents ? undefined : cents;
}

/** The work's name, with the other title in brackets when it differs. */
export function displayName(t: {
	name: string;
	polish_title: string | null;
	original_title: string | null;
}): string {
	const other = [t.polish_title, t.original_title].find((n) => n && n !== t.name);
	return other ? `${t.name} (${other})` : t.name;
}

/** A one-line description of an edition, such as "Blu-ray · Steelbook · 5901234123457". */
export function editionSummary(e: {
	format: Format | null;
	binding: Binding | null;
	label: string;
	packaging?: Packaging[];
	barcode: string | null;
}): string {
	const parts = [
		e.format ? formatLabels[e.format] : null,
		e.binding ? bindingLabels[e.binding] : null,
		e.label || null,
		...(e.packaging ?? []).map((p) => packagingLabels[p]),
		e.barcode
	];
	return parts.filter(Boolean).join(' · ') || 'Wydanie bez opisu';
}

export type CollectionFilter = {
	kind: Kind | '';
	format: Format | '';
	status: CopyStatus | '';
	q: string;
};

/** The /api/collection URL for a filter; empty fields are left out. */
export function collectionURL(f: CollectionFilter): string {
	const params = new URLSearchParams();
	if (f.kind) params.set('kind', f.kind);
	if (f.format) params.set('format', f.format);
	if (f.status) params.set('status', f.status);
	if (f.q.trim()) params.set('q', f.q.trim());
	const qs = params.toString();
	return qs ? `/api/collection?${qs}` : '/api/collection';
}

/** A form value failed a check before it reached the server. */
export type FormIssue = { field: string; message: string };
export type Parsed<T> = { ok: true; value: T } | { ok: false; issue: FormIssue };

function optText(s: string): string | null {
	const t = s.trim();
	return t === '' ? null : t;
}

function parseOptInt(raw: string, field: string, message: string): Parsed<number | null> {
	const t = raw.trim();
	if (t === '') return { ok: true, value: null };
	if (!/^\d+$/.test(t)) return { ok: false, issue: { field, message } };
	return { ok: true, value: Number(t) };
}

export type TitleForm = {
	kind: Kind;
	name: string;
	original_title: string;
	polish_title: string;
	year: string;
	tmdb_id: string;
	authors: string;
	bn_id: string;
	openlibrary_id: string;
	notes: string;
};

/** The editable form of a title, or an empty one of kind. */
export function titleToForm(t: TitlePayload | null, kind: Kind = 'movie'): TitleForm {
	return {
		kind: t?.kind ?? kind,
		name: t?.name ?? '',
		original_title: t?.original_title ?? '',
		polish_title: t?.polish_title ?? '',
		year: t?.year?.toString() ?? '',
		tmdb_id: t?.tmdb_id?.toString() ?? '',
		authors: t?.authors.join('\n') ?? '',
		bn_id: t?.bn_id ?? '',
		openlibrary_id: t?.openlibrary_id ?? '',
		notes: t?.notes ?? ''
	};
}

/** The request body for a title form; fields of the other kind are dropped. */
export function titlePayload(f: TitleForm): Parsed<TitlePayload> {
	if (!f.name.trim()) return { ok: false, issue: { field: 'name', message: 'Podaj tytuł.' } };
	const year = parseOptInt(f.year, 'year', 'Rok to liczba, np. 1984.');
	if (!year.ok) return year;
	const movie = f.kind === 'movie';
	const book = f.kind === 'book';
	const tmdb = movie
		? parseOptInt(f.tmdb_id, 'tmdb_id', 'Identyfikator TMDB to liczba.')
		: ({ ok: true, value: null } as const);
	if (!tmdb.ok) return tmdb;
	return {
		ok: true,
		value: {
			kind: f.kind,
			name: f.name.trim(),
			original_title: optText(f.original_title),
			polish_title: optText(f.polish_title),
			year: year.value,
			tmdb_id: tmdb.value,
			authors: movie
				? []
				: f.authors
						.split('\n')
						.map((a) => a.trim())
						.filter(Boolean),
			bn_id: book ? optText(f.bn_id) : null,
			openlibrary_id: book ? optText(f.openlibrary_id) : null,
			notes: f.notes.trim()
		}
	};
}

export type EditionForm = {
	barcode: string;
	label: string;
	publisher: string;
	year: string;
	format: Format | '';
	pl_subtitles: TriState;
	pl_voiceover: TriState;
	packaging: Packaging[];
	binding: Binding | '';
	discogs_release_id: string;
	notes: string;
};

/** The editable form of an edition, or an empty one. */
export function editionToForm(e: EditionPayload | null): EditionForm {
	return {
		barcode: e?.barcode ?? '',
		label: e?.label ?? '',
		publisher: e?.publisher ?? '',
		year: e?.year?.toString() ?? '',
		format: e?.format ?? '',
		pl_subtitles: e?.pl_subtitles ?? 'unknown',
		pl_voiceover: e?.pl_voiceover ?? 'unknown',
		packaging: [...(e?.packaging ?? [])],
		binding: e?.binding ?? '',
		discogs_release_id: e?.discogs_release_id?.toString() ?? '',
		notes: e?.notes ?? ''
	};
}

/** The request body for an edition of a title of kind; fields of the other kind are dropped. */
export function editionPayload(
	f: EditionForm,
	titleID: number,
	kind: Kind
): Parsed<EditionPayload> {
	const year = parseOptInt(f.year, 'year', 'Rok to liczba, np. 2019.');
	if (!year.ok) return year;
	const movie = kind === 'movie';
	const music = kind === 'music';
	if ((movie || music) && !formatFits(kind, f.format)) {
		return { ok: false, issue: { field: 'format', message: 'Wybierz nośnik.' } };
	}
	const discogs = music
		? parseOptInt(f.discogs_release_id, 'discogs_release_id', 'Identyfikator Discogs to liczba.')
		: ({ ok: true, value: null } as const);
	if (!discogs.ok) return discogs;
	return {
		ok: true,
		value: {
			title_id: titleID,
			barcode: optText(f.barcode),
			label: f.label.trim(),
			publisher: f.publisher.trim(),
			year: year.value,
			format: (movie || music) && f.format ? f.format : null,
			pl_subtitles: movie ? f.pl_subtitles : 'unknown',
			pl_voiceover: movie ? f.pl_voiceover : 'unknown',
			packaging: movie ? f.packaging : [],
			binding: kind === 'book' && f.binding ? f.binding : null,
			discogs_release_id: discogs.value,
			notes: f.notes.trim()
		}
	};
}

export type CopyForm = {
	status: CopyStatus;
	price: string;
	purchased_on: string;
	shop: string;
	acquired_as: AcquiredAs | '';
	condition: Condition | '';
	notes: string;
	max_price: string;
};

/** The editable form of a copy, or an empty owned one. */
export function copyToForm(c: CopyPayload | null): CopyForm {
	return {
		status: c?.status ?? 'owned',
		price: centsToInput(c?.price_cents ?? null),
		purchased_on: c?.purchased_on ?? '',
		shop: c?.shop ?? '',
		acquired_as: c?.acquired_as ?? '',
		condition: c?.condition ?? '',
		notes: c?.notes ?? '',
		max_price: centsToInput(c?.max_price_cents ?? null)
	};
}

/**
 * The request body for a copy. A wishlist copy carries no price or purchase date, and an owned
 * copy no maximum price.
 */
export function copyPayload(f: CopyForm, editionID: number): Parsed<CopyPayload> {
	const owned = f.status === 'owned';
	const price = owned ? parsePLN(f.price) : null;
	if (price === undefined) {
		return {
			ok: false,
			issue: { field: 'price_cents', message: 'Cena to kwota do 1 000 000 zł, np. 49,99.' }
		};
	}
	const maxPrice = owned ? null : parsePLN(f.max_price);
	if (maxPrice === undefined) {
		return {
			ok: false,
			issue: {
				field: 'max_price_cents',
				message: 'Cena maksymalna to kwota do 1 000 000 zł, np. 60.'
			}
		};
	}
	const date = owned ? f.purchased_on.trim() : '';
	if (date && !/^\d{4}-\d{2}-\d{2}$/.test(date)) {
		return { ok: false, issue: { field: 'purchased_on', message: 'Data w formacie RRRR-MM-DD.' } };
	}
	return {
		ok: true,
		value: {
			edition_id: editionID,
			status: f.status,
			price_cents: price,
			purchased_on: date || null,
			shop: f.shop.trim(),
			acquired_as: f.acquired_as || null,
			condition: f.condition || null,
			notes: f.notes.trim(),
			max_price_cents: maxPrice
		}
	};
}
