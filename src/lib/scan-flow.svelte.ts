import {
	ApiError,
	deadlineFetch,
	errorMessage,
	invalidBarcodeMessage,
	isNoAnswer,
	isSessionExpired,
	requestDeadlineMs
} from './api';
import {
	bindingLabels,
	formatLabels,
	formatPLN,
	type Edition,
	type FormIssue,
	type Kind
} from './collection';
import { pendingScans, type PendingScans } from './pending-scans.svelte';
import { logScan } from './recent.svelte';
import {
	FormRejected,
	LatestLoad,
	checkDigitMatches,
	checkScan,
	cleanBarcode,
	discardRequest,
	emptySticky,
	isInvalidBarcode,
	loadSticky,
	looksLikeBarcode,
	newMade,
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
	type Made,
	type QueueEntry,
	type QuickForm,
	type Saved,
	type SaveTarget,
	type Sticky
} from './scan';
import { recordAdd, type SessionAdd } from './session.svelte';

/** found and owned have candidates or the edition; missing is filled in by hand. */
export type ItemStatus = 'found' | 'owned' | 'missing';

export type FlowPhase =
	| { kind: 'loading' }
	| { kind: 'queued'; entry: QueueEntry }
	| { kind: 'ready'; status: ItemStatus }
	| { kind: 'invalid'; message: string }
	| { kind: 'failed'; message: string }
	/** The check got no answer, and the device keeps the code until the scan queue takes it. */
	| { kind: 'offline' }
	/** The sign-in turned the check away; saved when the device keeps the code meanwhile. */
	| { kind: 'expired'; saved: boolean };

/**
 * How long one request of a barcode's check may stay open: every API request's deadline, below
 * the server's 30-second request timeout. One still open after this got no answer and the code
 * is kept.
 */
export const checkTimeout = requestDeadlineMs;

/** Where a code whose check got no answer waits for the connection. */
export type Keeper = Pick<PendingScans, 'keep' | 'sessionExpired'>;

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
	/** What saves of this item created, kept across retries. */
	made: Made;
	/**
	 * The title and edition an earlier save of an item without a barcode sent its key with, while
	 * that key may still replay what it wrote; null once nothing stands behind the key.
	 */
	sent: string | null;
	/** The candidates came from a title search, which the user may leave for the draft. */
	searched: boolean;
	/** What the user had in the form when the first search answered, to go back to. */
	draft: Draft | null;
};

/** The title and medium the user fills in, which a picked candidate replaces. */
type Draft = Pick<QuickForm, 'kind' | 'name' | 'format' | 'binding' | 'packaging'>;

function draftOf(f: QuickForm): Draft {
	return {
		kind: f.kind,
		name: f.name,
		format: f.format,
		binding: f.binding,
		packaging: [...f.packaging]
	};
}

function sameDraft(a: Draft, b: Draft): boolean {
	return JSON.stringify(a) === JSON.stringify(b);
}

/**
 * A save that went through: the copy, the visit's record of it and, when a wedge scanner typed
 * it, the next barcode.
 */
export type Added = { saved: Saved; add: SessionAdd; barcode: string; next: string | null };

/**
 * Whether a save failed on an answer that says the request wrote nothing: a 4xx or the API's own
 * 500. No answer, or a gateway's 502 to 504, may hide a write the add's key replays.
 */
function refused(err: unknown): boolean {
	return err instanceof ApiError && err.status >= 400 && err.status <= 500;
}

/** What a save without a barcode cannot change once its key may have written: title and edition. */
function sentFor(target: SaveTarget, f: QuickForm): string {
	const candidate = target.kind === 'new' ? target.candidate : null;
	const { kind, name, format, binding, packaging } = f;
	return JSON.stringify([candidate, kind, name.trim(), format, binding, packaging]);
}

/** Why a changed item without a barcode is not saved: the earlier attempt still stands. */
const unsettled =
	'Nie udało się cofnąć poprzedniej próby zapisu, więc nowa pozycja nie została dodana. Spróbuj ponownie.';

/** The medium of the edition a save joined: its format or binding. */
function mediumOf(e: Pick<Edition, 'format' | 'binding'>): string {
	if (e.format) return formatLabels[e.format];
	return e.binding ? bindingLabels[e.binding] : '';
}

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
 * search by title, and saves the copy. The scanner's sheet, its add page and the collection's add
 * page, which starts without a barcode, share it, so all offer the same fields. Only the newest
 * barcode applies what it answers.
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
	readonly #pending: Keeper;

	constructor(
		fetchFn: typeof fetch,
		store: KeyValueStore | undefined,
		pending: Keeper = pendingScans
	) {
		this.#fetch = fetchFn;
		this.#store = store;
		this.#pending = pending;
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
			logged: false,
			made: newMade(),
			sent: null,
			searched: false,
			draft: null
		};
	}

	/** Drops what the last item answered and starts a new one; the result tells it is current. */
	#restart(code: string): () => boolean {
		const current = this.#loads.start();
		this.#current = current;
		this.code = code;
		this.phase = { kind: 'loading' };
		this.item = null;
		this.issue = null;
		this.notice = null;
		this.query = '';
		this.saving = false;
		this.searching = false;
		this.sticky = loadSticky(this.#store);
		return current;
	}

	/** Opens a blank item without a barcode, to search by title or fill in by hand. */
	start(kind: Kind) {
		this.#restart('');
		this.item = this.#blank('', true);
		this.form.kind = kind;
		this.phase = { kind: 'ready', status: 'missing' };
	}

	/**
	 * Resolves a barcode, or opens a blank form for one typed by hand. A check that gets no answer,
	 * or that the sign-in turns away, keeps the code on the device, even once the user moved on. A
	 * recheck asks each source again when its cached answer is over an hour old.
	 */
	async load(raw: string, byHand = false, recheck = false): Promise<void> {
		const code = cleanBarcode(raw);
		const current = this.#restart(code);
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
			const r = await resolveScan(deadlineFetch(this.#fetch, checkTimeout), code, recheck);
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
			const expired = isSessionExpired(err);
			const kept = (expired || isNoAnswer(err)) && (await this.#pending.keep(code));
			if (expired) this.#pending.sessionExpired();
			if (!current()) return;
			if (isInvalidBarcode(err)) {
				this.phase = { kind: 'invalid', message: errorMessage(err) };
				return;
			}
			if (kept) this.item = this.#blank(code, false);
			if (expired) this.phase = { kind: 'expired', saved: kept };
			else if (kept) this.phase = { kind: 'offline' };
			else this.phase = { kind: 'failed', message: errorMessage(err) };
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

	/**
	 * Leaves the results of a title search for the form as the user had it when the search first
	 * answered; the purchase fields stay as they are now.
	 */
	typeIn() {
		const item = this.item;
		if (!item || item.target.kind !== 'new' || !item.searched) return;
		this.#settleSearch();
		if (item.draft) this.form = { ...this.form, ...item.draft };
		item.candidates = [];
		item.picked = null;
		item.target = { kind: 'new', barcode: item.barcode, candidate: null };
		item.searched = false;
		item.draft = null;
		this.notice = null;
		this.issue = null;
	}

	/** The user changed the form or the query: a search still on its way answers nothing. */
	edited() {
		if (this.searching) this.#settleSearch();
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
		const asked = draftOf(this.form);
		const latest = () => current() && search === this.#search;
		// The user changed the title or medium meanwhile: the answer is for a form no longer shown.
		const fresh = () => latest() && sameDraft(draftOf(this.form), asked);
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
			if (!item.searched || item.target.candidate === null) item.draft = asked;
			item.searched = true;
			item.candidates = found;
			item.errors = [];
			item.picked = null;
			if (found.length === 1) this.pick(0);
			else this.choose();
		} catch (err) {
			if (fresh()) this.notice = errorMessage(err);
		} finally {
			if (latest()) this.searching = false;
		}
	}

	/**
	 * Saves the form. A copy that went through is logged, in the recent scans only when it has a
	 * barcode, and its shop, new or used and date carry over even when the user has moved on; only
	 * the barcode still shown gets the result.
	 * The copy the server returns decides what is logged: a retry of a save whose answer was
	 * lost returns the copy the first attempt made, even when the form changed in between. An
	 * owned copy's shop joins the ones stored when it answers, so another page's save that
	 * answered meanwhile keeps its shop. An add without a barcode the server refused removes what
	 * it made and starts over with a new key, as no barcode would find it again; after a lost
	 * answer it keeps the key, so a retry returns what the request made. A retry for another
	 * title or edition removes what the earlier attempt made first, and saves nothing while that
	 * is not confirmed.
	 */
	async save(): Promise<Added | null> {
		const item = this.item;
		if (!item || this.saving || this.choosing) return null;
		const current = this.#current;
		const next = wedgeBarcodeIn(this.form.price);
		if (next) this.form.price = '';
		const f = $state.snapshot(this.form);
		// Taken with the form: a pick made while an earlier attempt is removed belongs to the next save.
		const target = $state.snapshot(item.target);
		this.saving = true;
		this.issue = null;
		try {
			if (!item.barcode && target.kind === 'new') {
				// A form the server would refuse must not cost the earlier attempt its rows.
				checkScan(target, f);
				const sending = sentFor(target, f);
				if (item.sent !== null && item.sent !== sending && !(await this.#discard(item))) {
					throw new FormRejected('', unsettled);
				}
				item.sent = sending;
			}
			const saved = await saveScan(this.#fetch, target, f, item.key, item.made);
			const kept = { ...f, status: saved.copy.status ?? f.status };
			if (kept.status === 'owned') {
				visit.date = kept.purchased_on;
				this.sticky = stickyAfter(this.#store ? loadSticky(this.#store) : this.sticky, kept);
				saveSticky(this.#store, this.sticky);
			}
			const price = formatPLN(saved.copy.price_cents ?? saved.copy.max_price_cents);
			const what = kept.status === 'owned' ? '' : ' · lista życzeń';
			const recentID = item.barcode
				? logScan(
						item.barcode,
						'added',
						saved.titleName + (price ? ` · ${price}` : '') + what,
						`/copies/${saved.copy.id}`
					)
				: null;
			item.logged = true;
			const add = recordAdd({
				copyID: saved.copy.id,
				editionID: saved.editionID,
				titleID: saved.titleID,
				createdEdition: saved.createdEdition,
				createdTitle: saved.createdTitle,
				name: saved.titleName,
				medium: mediumOf(saved.edition),
				kind: saved.kind,
				cover: saved.cover,
				status: kept.status,
				priceCents: kept.status === 'owned' ? (saved.copy.price_cents ?? null) : null,
				recentID
			});
			return current() ? { saved, add, barcode: item.barcode, next } : null;
		} catch (err) {
			if (!item.barcode && refused(err)) await this.#discard(item);
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
	 * Removes what an item's earlier save made, then gives the item a new key, so nothing the key
	 * replays lands under another title. False when the server did not confirm the removal.
	 */
	async #discard(item: FlowItem): Promise<boolean> {
		try {
			await discardRequest(this.#fetch, item.key);
		} catch {
			return false;
		}
		item.key = newRequestKey();
		item.made = newMade();
		item.sent = null;
		return true;
	}

	/**
	 * Drops every answer still on its way, as a newer barcode does, once the page that shows the
	 * flow is gone. A save still running is logged and keeps its shop, but reports nothing.
	 */
	detach() {
		this.#loads.start();
	}

	/**
	 * Logs a barcode the user left without adding: waiting when the device keeps it, queued when
	 * no source knew it, else skipped. A barcode opened to be typed by hand, one already logged and
	 * one still saving are left alone.
	 */
	dismiss() {
		const item = this.item;
		if (!item || item.logged || item.byHand || this.saving) return;
		item.logged = true;
		const p = this.phase;
		if (p.kind === 'offline' || p.kind === 'expired') {
			const why = p.kind === 'offline' ? 'Zapisano bez połączenia' : 'Zapisano, sesja wygasła';
			logScan(item.barcode, 'waiting', why);
			return;
		}
		const missing = p.kind === 'queued' || (p.kind === 'ready' && p.status === 'missing');
		logScan(
			item.barcode,
			missing ? 'queued' : 'skipped',
			missing ? 'Brak danych, do uzupełnienia' : 'Nie dodano',
			missing ? '/scan/queue' : undefined
		);
	}
}
