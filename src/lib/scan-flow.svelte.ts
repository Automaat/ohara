import { ApiError, errorMessage, invalidBarcodeMessage } from './api';
import { formatPLN, type FormIssue } from './collection';
import { logScan } from './recent.svelte';
import {
	FormRejected,
	LatestLoad,
	checkDigitMatches,
	cleanBarcode,
	emptySticky,
	isInvalidBarcode,
	loadSticky,
	looksLikeBarcode,
	newRequestKey,
	quickForm,
	resolveScan,
	saveScan,
	saveSticky,
	searchTitles,
	stickyAfter,
	wedgeBarcodeIn,
	type Candidate,
	type KeyValueStore,
	type LookupResult,
	type QueueEntry,
	type QuickForm,
	type Saved,
	type SaveTarget,
	type Sticky
} from './scan';

/** found and owned have candidates or the edition; missing is filled in by hand. */
export type ItemStatus = 'found' | 'owned' | 'missing';

export type FlowPhase =
	| { kind: 'loading' }
	| { kind: 'queued'; entry: QueueEntry }
	| { kind: 'ready'; status: ItemStatus }
	| { kind: 'invalid'; message: string }
	| { kind: 'failed'; message: string };

/** The barcode being added and what it resolved to. picked is null while the user chooses. */
export type FlowItem = {
	key: string;
	barcode: string;
	byHand: boolean;
	target: SaveTarget;
	candidates: Candidate[];
	picked: number | null;
	errors: LookupResult['errors'];
	/** Logged in the recent scans, as added or not. */
	logged: boolean;
};

/** A save that went through: the copy and, when a wedge scanner typed it, the next barcode. */
export type Added = { saved: Saved; barcode: string; next: string | null };

/**
 * What one visit carries from an add to the next: the purchase date. It lives in the module, so
 * it outlasts the trips between the scanner and the add page, and resets on a reload.
 */
const visit = { date: '' };

/** Forgets the visit's purchase date, as a reload does. */
export function resetVisit() {
	visit.date = '';
}

/**
 * The quick confirm for one barcode after another: resolves it, lets the user pick a candidate or
 * search by title, and saves the copy. The scanner's sheet and the add page share it, so both
 * offer the same fields. Only the newest barcode applies what it answers.
 */
export class ScanFlow {
	/** The barcode as read or typed, cleaned; the item's barcode is the server's form of it. */
	code = $state('');
	phase = $state<FlowPhase>({ kind: 'loading' });
	item = $state<FlowItem | null>(null);
	form = $state<QuickForm>(quickForm(null, emptySticky, ''));
	issue = $state<FormIssue | null>(null);
	saving = $state(false);
	notice = $state<string | null>(null);
	query = $state('');
	searching = $state(false);
	sticky = $state<Sticky>({ ...emptySticky, shops: [] });
	readonly #loads = new LatestLoad();
	#current: () => boolean = () => false;
	/** Numbers title searches; a pick or a newer search makes an older answer stale. */
	#search = 0;
	readonly #fetch: typeof fetch;
	readonly #store: KeyValueStore | undefined;

	constructor(fetchFn: typeof fetch, store: KeyValueStore | undefined) {
		this.#fetch = fetchFn;
		this.#store = store;
		this.sticky = loadSticky(store);
	}

	/** Whether the candidates wait for a pick before the form shows. */
	get choosing(): boolean {
		const item = this.item;
		return (
			item !== null &&
			item.target.kind === 'new' &&
			item.picked === null &&
			item.candidates.length > 0
		);
	}

	/** The candidate the form starts from, if any. */
	get candidate(): Candidate | null {
		const item = this.item;
		return item?.picked == null ? null : (item.candidates[item.picked] ?? null);
	}

	#blank(code: string, byHand: boolean): FlowItem {
		this.form = quickForm(null, this.sticky, code, visit.date);
		return {
			key: newRequestKey(),
			barcode: code,
			byHand,
			target: { kind: 'new', barcode: code, candidate: null },
			candidates: [],
			picked: null,
			errors: [],
			logged: false
		};
	}

	/** Resolves a barcode, or opens a blank form for one typed by hand. */
	async load(raw: string, byHand = false): Promise<void> {
		const current = this.#loads.start();
		this.#current = current;
		const code = cleanBarcode(raw);
		this.code = code;
		this.phase = { kind: 'loading' };
		this.item = null;
		this.issue = null;
		this.notice = null;
		this.query = '';
		this.saving = false;
		this.searching = false;
		this.sticky = loadSticky(this.#store);
		if (!looksLikeBarcode(code)) {
			this.phase = { kind: 'invalid', message: `„${raw.trim()}” nie wygląda na kod kreskowy.` };
			return;
		}
		if (!checkDigitMatches(code)) {
			this.phase = { kind: 'invalid', message: invalidBarcodeMessage };
			return;
		}
		if (byHand) {
			this.item = this.#blank(code, true);
			this.phase = { kind: 'ready', status: 'missing' };
			return;
		}
		try {
			const r = await resolveScan(this.#fetch, code);
			if (!current()) return;
			if (r.kind === 'owned') {
				this.item = {
					...this.#blank(r.barcode, false),
					target: { kind: 'owned', edition: r.edition }
				};
				this.form.kind = r.edition.title.kind;
				this.phase = { kind: 'ready', status: 'owned' };
			} else if (r.kind === 'found') {
				const picked = r.needsConfirmation ? null : 0;
				const first = r.candidates[0] ?? null;
				this.item = {
					...this.#blank(r.barcode, false),
					target: {
						kind: 'new',
						barcode: r.barcode,
						candidate: picked === null ? null : first
					},
					candidates: r.candidates,
					picked,
					errors: r.errors
				};
				this.form = quickForm(picked === null ? null : first, this.sticky, r.barcode, visit.date);
				if (first) this.form.kind = first.type;
				this.phase = { kind: 'ready', status: 'found' };
			} else {
				this.item = this.#blank(r.entry.barcode, false);
				this.phase = { kind: 'queued', entry: r.entry };
			}
		} catch (err) {
			if (!current()) return;
			this.phase = isInvalidBarcode(err)
				? { kind: 'invalid', message: errorMessage(err) }
				: { kind: 'failed', message: errorMessage(err) };
		}
	}

	/**
	 * Fills the form from a candidate, keeping what the user typed about the purchase. The
	 * packaging is the candidate's own, so one edition's steelbook never carries to another.
	 */
	pick(index: number) {
		const item = this.item;
		const candidate = item?.candidates[index];
		if (!item || item.target.kind !== 'new' || !candidate) return;
		this.#settleSearch();
		item.picked = index;
		item.target = { kind: 'new', barcode: item.barcode, candidate };
		const next = quickForm(candidate, this.sticky, item.barcode);
		const f = this.form;
		const sameKind = next.kind === f.kind;
		this.form = {
			...next,
			format: next.format || (sameKind ? f.format : ''),
			binding: next.binding || (sameKind ? f.binding : ''),
			status: f.status,
			price: f.price,
			purchased_on: f.purchased_on,
			shop: f.shop,
			acquired_as: f.acquired_as,
			condition: f.condition
		};
		this.issue = null;
	}

	/** Shows the candidates again to pick another one. */
	choose() {
		const item = this.item;
		if (!item || item.target.kind !== 'new' || item.candidates.length === 0) return;
		this.#settleSearch();
		item.picked = null;
		item.target = { kind: 'new', barcode: item.barcode, candidate: null };
		this.issue = null;
	}

	/** Drops the answer of a title search still on its way. */
	#settleSearch() {
		this.#search++;
		this.searching = false;
	}

	/** Switches between adding an owned copy and a wish; the typed price stays. */
	wish(on: boolean) {
		this.form.status = on ? 'wishlist' : 'owned';
		this.issue = null;
	}

	/** Turns a barcode no source knows into a form filled in by hand, in place. */
	manual() {
		const item = this.item;
		if (!item) return;
		this.item = { ...this.#blank(item.barcode, item.byHand), logged: item.logged };
		this.phase = { kind: 'ready', status: 'missing' };
	}

	/** Searches by the typed title; one result fills the form, several wait for a pick. */
	async search(): Promise<void> {
		const item = this.item;
		const query = this.query.trim();
		if (!item || item.target.kind !== 'new' || this.searching || !query) return;
		const current = this.#current;
		const search = ++this.#search;
		const fresh = () => current() && search === this.#search;
		this.searching = true;
		try {
			const found = await searchTitles(
				this.#fetch,
				this.form.kind,
				query,
				item.barcode,
				this.form.format
			);
			if (!fresh()) return;
			if (found.length === 0) {
				this.notice = `Nic nie znaleziono dla „${query}”.`;
				return;
			}
			this.notice = null;
			item.candidates = found;
			item.errors = [];
			item.picked = null;
			if (found.length === 1) this.pick(0);
			else this.choose();
		} catch (err) {
			if (fresh()) this.notice = errorMessage(err);
		} finally {
			if (fresh()) this.searching = false;
		}
	}

	/**
	 * Saves the form. A copy that went through is logged and its shop, new or used and date
	 * carry over even when the user has moved on; only the barcode still shown gets the result.
	 * The copy the server returns decides what is logged: a retry of a save whose answer was
	 * lost returns the copy the first attempt made, even when the form changed in between.
	 */
	async save(): Promise<Added | null> {
		const item = this.item;
		if (!item || this.saving || this.choosing) return null;
		const current = this.#current;
		const next = wedgeBarcodeIn(this.form.price);
		if (next) this.form.price = '';
		const f = $state.snapshot(this.form);
		this.saving = true;
		this.issue = null;
		try {
			const saved = await saveScan(this.#fetch, item.target, f, item.key);
			const kept = { ...f, status: saved.copy.status ?? f.status };
			if (kept.status === 'owned') visit.date = kept.purchased_on;
			this.sticky = stickyAfter(this.sticky, kept);
			saveSticky(this.#store, this.sticky);
			const price = formatPLN(saved.copy.price_cents ?? saved.copy.max_price_cents);
			const what = kept.status === 'owned' ? '' : ' · lista życzeń';
			logScan(
				item.barcode,
				'added',
				saved.titleName + (price ? ` · ${price}` : '') + what,
				`/copies/${saved.copy.id}`
			);
			item.logged = true;
			return current() ? { saved, barcode: item.barcode, next } : null;
		} catch (err) {
			if (!current()) return null;
			this.issue =
				err instanceof FormRejected
					? { field: err.field, message: err.message }
					: {
							field: err instanceof ApiError ? (err.field ?? '') : '',
							message: errorMessage(err)
						};
			if (next) this.notice = `Kod ${next} nie został sprawdzony: zeskanuj go ponownie.`;
			return null;
		} finally {
			if (current()) this.saving = false;
		}
	}

	/**
	 * Logs a barcode the user left without adding: queued when no source knew it, else skipped.
	 * A barcode opened to be typed by hand, one already logged and one still saving are left
	 * alone.
	 */
	dismiss() {
		const item = this.item;
		if (!item || item.logged || item.byHand || this.saving) return;
		item.logged = true;
		const p = this.phase;
		const missing = p.kind === 'queued' || (p.kind === 'ready' && p.status === 'missing');
		logScan(
			item.barcode,
			missing ? 'queued' : 'skipped',
			missing ? 'Brak danych, do uzupełnienia' : 'Nie dodano',
			missing ? '/scan/queue' : undefined
		);
	}
}
