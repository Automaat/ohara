import { trimSpace } from './go-text';
import { tagKey, tagMatches, tagTerm } from './tags';
import { editionCount, orderedCount, purchaseDate } from './polish';
import type {
	AcquiredAs,
	Binding,
	CollectionItem,
	Condition,
	CopyPayload,
	CopyStatus,
	ContentTitle,
	Edition,
	EditionPayload,
	Format,
	Kind,
	Loan,
	LoanPayload,
	MovieFormat,
	MusicFormat,
	Packaging,
	Title,
	TitlePayload,
	Totals,
	TriState
} from './api.gen';

export type {
	AcquiredAs,
	Binding,
	BoxSet,
	CollectionItem,
	CollectionView,
	Condition,
	ContentTitle,
	Copy,
	CopyDetail,
	CopyPayload,
	CopyStatus,
	Edition,
	EditionDetail,
	EditionPayload,
	FinishPayload,
	Format,
	Kind,
	Loan,
	LoanPayload,
	MovieFormat,
	MusicFormat,
	Packaging,
	RatingPayload,
	ReturnPayload,
	TagCount,
	Title,
	TitleDetail,
	TitlePayload,
	Totals,
	TriState
} from './api.gen';

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
	ordered: 'Zamówiony',
	wishlist: 'Lista życzeń',
	sold: 'Sprzedany'
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
const count = new Intl.NumberFormat('pl-PL');

/** How many copies of each kind a list holds. */
export function kindCounts(items: readonly Pick<CollectionItem, 'kind'>[]): Record<Kind, number> {
	const counts: Record<Kind, number> = { movie: 0, book: 0, music: 0 };
	for (const { kind } of items) counts[kind] += 1;
	return counts;
}

/** How many copies are on the shelf, on order or on the wishlist: every copy but the sold ones. */
export function shelvedCount(t: Pick<Totals, 'owned' | 'ordered' | 'wishlist'>): number {
	return t.owned + (t.ordered ?? 0) + t.wishlist;
}

/**
 * The collection header's one line, such as "20 na półce · 2 zamówione · 4 na liście życzeń";
 * copies on order are named only when there are some.
 */
export function shelfSummary(t: Pick<Totals, 'owned' | 'ordered' | 'wishlist'>): string {
	return [
		`${count.format(t.owned)} na półce`,
		t.ordered ? orderedCount(t.ordered) : null,
		`${count.format(t.wishlist)} na liście życzeń`
	]
		.filter(Boolean)
		.join(' · ');
}

/** Grosze as "49,99 zł"; null as an empty string. */
/** Hint under a purchase price field: blank, not 0, is an unknown price. */
export const priceHint = 'Nie pamiętasz? Zostaw puste. 0 zł liczy się jako cena.';

/** How many owned copies have no purchase price yet. */
export function unpricedCount(t: Pick<Totals, 'owned' | 'priced'>): number {
	return t.owned - t.priced;
}

/** The phone header's link to the copies without a price, such as "3 bez ceny". */
export function unpricedLabel(n: number): string {
	return `${count.format(n)} bez ceny`;
}

/** What owned and ordered copies cost, and how many owned ones still have no price to fill in. */
export function spentLabel(t: Totals): string {
	const unpriced = unpricedCount(t);
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

const wholePLN = new Intl.NumberFormat('pl-PL', {
	style: 'currency',
	currency: 'PLN',
	minimumFractionDigits: 0
});

/**
 * A wish's line under its name, such as "Lista życzeń · do 80 zł" or "Lista życzeń". A no-break
 * space keeps "do 80 zł" on one line when the line wraps.
 */
export function wishLine(limitCents: number | null): string {
	if (limitCents == null) return statusLabels.wishlist;
	const limit = limitCents % 100 === 0 ? wholePLN.format(limitCents / 100) : formatPLN(limitCents);
	return `${statusLabels.wishlist} · do\u00a0${limit}`;
}

/** A date as the API and a date field take it, YYYY-MM-DD, in local time. */
export function isoDate(d: Date): string {
	const pad = (n: number) => String(n).padStart(2, '0');
	return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

/** Whether a loan due on a YYYY-MM-DD day is late on today: only once that day has passed. */
export function overdue(dueOn: string | null | undefined, today: string): boolean {
	return dueOn != null && dueOn < today;
}

/**
 * The day "Zwrócono" records: today, or the loan's own day when it began later, as a date typed
 * ahead or the server's Polish today seen from a browser west of Poland, which the API would
 * refuse as a return before the loan.
 */
export function returnDay(lentOn: string, today: string): string {
	return lentOn > today ? lentOn : today;
}

/** A lent copy's line, such as "Wypożyczony: Ania" or "Wypożyczony: Ania · po terminie". */
export function loanLine(
	borrower: string,
	dueOn: string | null | undefined,
	today: string
): string {
	const line = `Wypożyczony: ${borrower}`;
	return overdue(dueOn, today) ? `${line} · po terminie` : line;
}

/**
 * A sold copy's line under its name, such as "Sprzedany 10 maj 2026 · 35 zł": the day and, when
 * known, the price it went for.
 */
export function saleLine(soldOn: string | null | undefined, priceCents: number | null | undefined) {
	const day = purchaseDate(soldOn ?? null);
	const price = priceCents == null ? '' : formatPLN(priceCents);
	return [[statusLabels.sold, day].filter(Boolean).join(' '), price].filter(Boolean).join(' · ');
}

/**
 * An ordered copy's line under its name: the day it should arrive, such as "Dostawa 15 gru
 * 2026", with "po terminie" once that day has passed, or "Zamówiony" without one. Today, as
 * YYYY-MM-DD, is still on time.
 */
export function orderLine(expectedOn: string | null | undefined, today: string): string {
	if (!expectedOn) return statusLabels.ordered;
	const line = `Dostawa ${purchaseDate(expectedOn)}`;
	return expectedOn < today ? `${line} · po terminie` : line;
}

/**
 * A shelf tile's line under the name: the wish and its limit for a wishlist copy, the sale for
 * a sold one, the delivery for an ordered one, who has a lent copy, otherwise the creators and
 * the edition, such as "Stanisław Lem · Twarda oprawa". A loan or a delivery is late once its
 * day is before today, the reader's local day.
 */
export function shelfLine(
	item: Pick<
		CollectionItem,
		| 'status'
		| 'max_price_cents'
		| 'sold_on'
		| 'sale_price_cents'
		| 'expected_on'
		| 'authors'
		| 'format'
		| 'binding'
		| 'label'
	> &
		Partial<Pick<CollectionItem, 'loan_borrower' | 'loan_due_on'>>,
	today: string = isoDate(new Date())
): string {
	if (item.status === 'wishlist') return wishLine(item.max_price_cents);
	if (item.status === 'sold') return saleLine(item.sold_on, item.sale_price_cents);
	if (item.status === 'ordered') return orderLine(item.expected_on, today);
	if (item.loan_borrower != null) return loanLine(item.loan_borrower, item.loan_due_on, today);
	return [item.authors?.join(', '), editionSummary({ ...item, barcode: null })]
		.filter(Boolean)
		.join(' · ');
}

/**
 * What the collection lists: copies of a status, `unpriced`, the owned copies without a purchase
 * price, or `none`, the titles that have no copy yet. Without one it lists every copy but the
 * sold ones.
 */
export type CollectionStatus = CopyStatus | 'unpriced' | 'none';
export const collectionStatusLabels: Record<CollectionStatus, string> = {
	...statusLabels,
	ordered: 'Zamówione',
	sold: 'Sprzedane',
	unpriced: 'Bez ceny',
	none: 'Bez egzemplarzy'
};

/** The filter that keeps only copies out on loan, the server's `?loaned=1`. */
export const loanedLabel = 'Wypożyczone';

/**
 * The filter that keeps only owned copies of titles not yet watched, read or heard, the server's
 * `?unfinished=1`.
 */
export const unfinishedLabel = 'Do nadrobienia';

/** Whether a status leaves owned copies to list, so "Do nadrobienia" can narrow it. */
export function listsOwned(status: CollectionFilter['status']): boolean {
	return status === '' || status === 'owned' || status === 'unpriced';
}

export type CollectionFilter = {
	kind: Kind | '';
	format: Format | '';
	status: CollectionStatus | '';
	/** Where the copies are stored, the whole location, case aside; absent for any place. */
	location?: string;
	q: string;
	/** Only copies with an open loan; left out when off. */
	loaned?: boolean;
	/** Only owned copies of titles not finished; left out when off. */
	unfinished?: boolean;
	/** A tag of the title, the whole tag, case aside; absent for any title. */
	tag?: string;
};

/**
 * The filter a collection address names. A title has no format, location or loan, so `none`
 * drops them; "Do nadrobienia" stays only beside a status that lists owned copies, as the sheet
 * offers it.
 */
export function readCollectionFilter(params: Pick<URLSearchParams, 'get'>): CollectionFilter {
	function read<K extends string>(key: string, labels: Record<K, string>): K | '' {
		const value = params.get(key);
		return value && Object.hasOwn(labels, value) ? (value as K) : '';
	}

	const status = read('status', collectionStatusLabels);
	const location = status === 'none' ? '' : trimSpace(params.get('location') ?? '');
	const tag = tagTerm(params.get('tag') ?? '');
	return {
		kind: read('kind', kindLabels),
		format: status === 'none' ? '' : read('format', formatLabels),
		status,
		...(location ? { location } : {}),
		q: params.get('q') ?? '',
		...(status !== 'none' && params.get('loaned') === '1' ? { loaned: true } : {}),
		...(listsOwned(status) && params.get('unfinished') === '1' ? { unfinished: true } : {}),
		...(tag ? { tag } : {})
	};
}

export function collectionSearch(f: CollectionFilter): string {
	const params = new URLSearchParams();
	if (f.kind) params.set('kind', f.kind);
	if (f.format) params.set('format', f.format);
	if (f.status) params.set('status', f.status);
	if (f.loaned) params.set('loaned', '1');
	if (f.unfinished) params.set('unfinished', '1');
	const location = trimSpace(f.location ?? '');
	if (location) params.set('location', location);
	if (f.q.trim()) params.set('q', f.q.trim());
	const tag = tagTerm(f.tag ?? '');
	if (tag) params.set('tag', tag);
	const qs = params.toString();
	return qs ? `?${qs}` : '';
}

/** Kolekcja listing what carries a tag. */
export function tagHref(tag: string): string {
	return `/${collectionSearch({ kind: '', format: '', status: '', q: '', tag })}`;
}

/** The titles that have no copy yet and no owned box set holds. */
export function withoutCopies(titles: readonly Title[]): Title[] {
	return titles.filter((t) => t.copy_count === 0 && !t.in_owned_box_set);
}

/**
 * The titles without a copy of the filter's kind and tag whose names or authors contain its
 * words, as the server searches titles.
 */
export function uncopiedTitles(
	titles: readonly Title[],
	f: Pick<CollectionFilter, 'kind' | 'q' | 'tag'>
): Title[] {
	const words = f.q.trim().toLocaleLowerCase('pl-PL');
	return withoutCopies(titles).filter(
		(t) =>
			(!f.kind || t.kind === f.kind) &&
			tagMatches(t.tags, f.tag) &&
			(!words ||
				[t.name, t.original_title, t.polish_title, ...t.authors]
					.filter(Boolean)
					.join(' ')
					.toLocaleLowerCase('pl-PL')
					.includes(words))
	);
}

/**
 * The tags the lists carry, each once with the first spelling met, in name order, so the filter
 * offers only tags that list something.
 */
export function tagsInUse(...lists: readonly (readonly { tags?: string[] }[])[]): string[] {
	const byKey = new Map<string, string>();
	for (const list of lists) {
		for (const { tags } of list) {
			for (const tag of tags ?? []) {
				const key = tagKey(tag);
				if (!byKey.has(key)) byKey.set(key, tag);
			}
		}
	}
	return [...byKey.values()].sort((a, b) => a.localeCompare(b, 'pl-PL'));
}

/** Adds a typed tag to a title's tags unless blank or already there, case aside. */
export function addTag(tags: readonly string[], typed: string): string[] {
	const tag = trimSpace(typed);
	if (!tag || tags.some((t) => tagKey(t) === tagKey(tag))) return [...tags];
	return [...tags, tag];
}

/** A title tile's line under the name, such as "Stanisław Lem · Brak wydań" or "Film · 2 wydania". */
export function uncopiedLine(t: Pick<Title, 'kind' | 'authors' | 'edition_count'>): string {
	const who = t.authors.join(', ') || kindLabels[t.kind];
	return `${who} · ${t.edition_count ? editionCount(t.edition_count) : 'Brak wydań'}`;
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
	cover_url: string;
	notes: string;
	tags: string[];
	/** A tag typed but not added yet; saving adds it. */
	tag: string;
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
		cover_url: t?.cover_url ?? '',
		notes: t?.notes ?? '',
		tags: [...(t?.tags ?? [])],
		tag: ''
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
			cover_url: f.cover_url.trim(),
			notes: f.notes.trim(),
			tags: addTag(f.tags, f.tag)
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
	cover_url: string;
	notes: string;
	/** The titles a box set holds, in their order; left out while the edition lists none. */
	contents?: ContentTitle[];
	/**
	 * The IDs of the titles the edition listed when the form opened. A save sends the contents
	 * only once the reader changed them, so it keeps what another save listed meanwhile.
	 */
	savedContents?: number[];
};

/** The editable form of an edition, or an empty one. */
export function editionToForm(e: Edition | null): EditionForm {
	return {
		...(e?.contents?.length
			? { contents: [...e.contents], savedContents: e.contents.map((c) => c.id) }
			: {}),
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
		cover_url: e?.cover_url ?? '',
		notes: e?.notes ?? ''
	};
}

/** The box set contents a save sends: none unless the reader changed the list. */
function contentsChange(f: EditionForm): { contents?: number[] } {
	const ids = (f.contents ?? []).map((c) => c.id);
	const saved = f.savedContents ?? [];
	const same = ids.length === saved.length && ids.every((id, i) => id === saved[i]);
	return same ? {} : { contents: ids };
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
			cover_url: f.cover_url.trim(),
			notes: f.notes.trim(),
			...contentsChange(f)
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
	market_percent: string;
	sold_on: string;
	sale_price: string;
	expected_on: string;
	location: string;
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
		max_price: centsToInput(c?.max_price_cents ?? null),
		market_percent: c?.max_market_percent == null ? '' : String(c.max_market_percent),
		sold_on: c?.sold_on ?? '',
		sale_price: centsToInput(c?.sale_price_cents ?? null),
		expected_on: c?.expected_on ?? '',
		location: c?.location ?? ''
	};
}

/** A share of the market price as typed, "70" or "70 %": a whole 1 to 100, null when blank. */
export function parsePercent(input: string): number | null | undefined {
	const cleaned = input.trim().replace(/\s*%$/, '');
	if (cleaned === '') return null;
	if (!/^\d{1,3}$/.test(cleaned)) return undefined;
	const percent = Number(cleaned);
	return percent >= 1 && percent <= 100 ? percent : undefined;
}

/**
 * The request body for a copy. A wishlist copy carries no price or purchase date, an owned, sold
 * or ordered copy no maximum price or market percent, only a sold copy its sale's day, which it
 * needs, and price, only an ordered copy its expected delivery and only an owned copy its
 * location. A field a copy does not carry is left out, which the server reads as none, so a sold
 * copy put back on the shelf drops its sale and a received one its delivery day.
 */
export function copyPayload(f: CopyForm, editionID: number): Parsed<CopyPayload> {
	const sold = f.status === 'sold';
	const owned = f.status === 'owned' || sold || f.status === 'ordered';
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
	const marketPercent = owned ? null : parsePercent(f.market_percent);
	if (marketPercent === undefined) {
		return {
			ok: false,
			issue: {
				field: 'max_market_percent',
				message: 'Procent ceny rynkowej to liczba od 1 do 100, np. 70.'
			}
		};
	}
	// Trimmed as the server trims, so saving other fields leaves a stored location as it is.
	const location = f.status === 'owned' ? trimSpace(f.location) : '';
	const date = owned ? f.purchased_on.trim() : '';
	if (date && !isDate(date)) {
		return { ok: false, issue: { field: 'purchased_on', message: 'Data w formacie RRRR-MM-DD.' } };
	}
	const sale = sold ? saleOf(f) : {};
	if ('issue' in sale) return { ok: false, issue: sale.issue };
	const expected = f.status === 'ordered' ? f.expected_on.trim() : '';
	if (expected && !isDate(expected)) {
		return { ok: false, issue: { field: 'expected_on', message: 'Data w formacie RRRR-MM-DD.' } };
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
			max_price_cents: maxPrice,
			...(marketPercent == null ? {} : { max_market_percent: marketPercent }),
			...sale,
			...(expected ? { expected_on: expected } : {}),
			...(location ? { location } : {})
		}
	};
}

/** Whether text is a day of the calendar as YYYY-MM-DD, so not 2026-02-30. */
function isDate(text: string): boolean {
	if (!/^\d{4}-\d{2}-\d{2}$/.test(text)) return false;
	const [year, month, day] = text.split('-').map(Number) as [number, number, number];
	const date = new Date(Date.UTC(year, month - 1, day));
	return date.getUTCMonth() === month - 1 && date.getUTCDate() === day;
}

/** A sold copy's sale as the request carries it: the day it needs and the price, if given. */
function saleOf(
	f: CopyForm
): Pick<CopyPayload, 'sold_on' | 'sale_price_cents'> | { issue: FormIssue } {
	const day = f.sold_on.trim();
	if (!day) return { issue: { field: 'sold_on', message: 'Podaj datę sprzedaży.' } };
	if (!isDate(day)) return { issue: { field: 'sold_on', message: 'Data w formacie RRRR-MM-DD.' } };
	const price = parsePLN(f.sale_price);
	if (price === undefined) {
		return {
			issue: {
				field: 'sale_price_cents',
				message: 'Cena sprzedaży to kwota do 1 000 000 zł, np. 35.'
			}
		};
	}
	return { sold_on: day, ...(price == null ? {} : { sale_price_cents: price }) };
}

export type LoanForm = { borrower: string; lent_on: string; due_on: string };

/** An empty loan form for a copy lent today. */
export function loanToForm(today: string): LoanForm {
	return { borrower: '', lent_on: today, due_on: '' };
}

const isoDay = /^\d{4}-\d{2}-\d{2}$/;

/** The request body for a loan form; a blank lent date is today on the server. */
export function loanPayload(f: LoanForm): Parsed<LoanPayload> {
	const borrower = f.borrower.trim();
	const lent = f.lent_on.trim();
	const due = f.due_on.trim();
	if (!borrower) return { ok: false, issue: { field: 'borrower', message: 'Podaj, komu.' } };
	if (lent && !isoDay.test(lent)) {
		return { ok: false, issue: { field: 'lent_on', message: 'Data w formacie RRRR-MM-DD.' } };
	}
	if (due && !isoDay.test(due)) {
		return { ok: false, issue: { field: 'due_on', message: 'Data w formacie RRRR-MM-DD.' } };
	}
	if (lent && due && due < lent) {
		return {
			ok: false,
			issue: { field: 'due_on', message: 'Termin zwrotu nie może być przed wypożyczeniem.' }
		};
	}
	return { ok: true, value: { borrower, lent_on: lent || null, due_on: due || null } };
}

/**
 * A loan in a copy's history: "Ania · od 1 maja 2026 · termin 1 czerwca 2026" while it is out,
 * with "po terminie" once that day has passed, or "Ania · 1 maja 2026 – 20 maja 2026" once back.
 */
export function loanSummary(
	l: Pick<Loan, 'borrower' | 'lent_on' | 'due_on' | 'returned_on'>,
	today: string
): string {
	const lent = purchaseDate(l.lent_on);
	if (l.returned_on) return `${l.borrower} · ${lent} – ${purchaseDate(l.returned_on)}`;
	return [
		l.borrower,
		`od ${lent}`,
		l.due_on && `termin ${purchaseDate(l.due_on)}`,
		overdue(l.due_on, today) && 'po terminie'
	]
		.filter(Boolean)
		.join(' · ');
}
