import { queueDeletesSettled } from './queue-deletes';
import { queueWaiting } from './recent.svelte';
import { canonicalBarcode } from './scan';

/** What the sheet says about a code the check got no answer for, once the device kept it. */
export const offlineSavedMessage =
	'Brak połączenia. Kod zapisano i trafi do kolejki, gdy połączenie wróci.';

/** The scan queue's detail for a code scanned without a connection. */
export const offlineDetail = 'Zeskanowano bez połączenia';

/** How often codes still waiting are sent again, for a weak signal the browser calls online. */
export const sendInterval = 20_000;

/** How long one code's POST may take before the run counts it as unanswered. */
export const postTimeout = 15_000;

/**
 * Runs a send while no other tab of the app sends; false when another tab holds the lock and
 * nothing ran, as that tab sends the same codes.
 */
export type SendLock = (run: () => Promise<void>) => Promise<boolean>;

const lockName = 'ohara-pending-scans';

/** The browser's Web Locks, shared by the app's tabs; without them each tab sends on its own. */
export const browserLock: SendLock = async (run) => {
	const locks = globalThis.navigator?.locks;
	if (!locks) {
		await run();
		return true;
	}
	return locks.request(lockName, { ifAvailable: true }, async (lock) => {
		if (!lock) return false;
		await run();
		return true;
	});
};

/** Runs work with a signal that aborts it after ms, and rejects once ms passed either way. */
function withDeadline<T>(ms: number, work: (signal: AbortSignal) => Promise<T>): Promise<T> {
	const controller = new AbortController();
	let timer: ReturnType<typeof setTimeout> | undefined;
	const deadline = new Promise<never>((_, reject) => {
		timer = setTimeout(() => {
			controller.abort();
			reject(new DOMException('No answer in time', 'TimeoutError'));
		}, ms);
	});
	return Promise.race([work(controller.signal), deadline]).finally(() => clearTimeout(timer));
}

/** The field an API error body names, if the body is one. */
async function errorField(res: Response): Promise<string | undefined> {
	try {
		const body: unknown = await res.json();
		const field =
			body && typeof body === 'object' ? (body as { field?: unknown }).field : undefined;
		return typeof field === 'string' ? field : undefined;
	} catch {
		return undefined;
	}
}

/** The barcodes a device keeps until the scan queue takes them, each once. */
export type PendingStore = {
	list(): Promise<string[]>;
	add(barcode: string): Promise<void>;
	remove(barcode: string): Promise<void>;
};

const dbName = 'ohara';
const storeName = 'pending-scans';

function finished(tx: IDBTransaction): Promise<void> {
	return new Promise((resolve, reject) => {
		tx.oncomplete = () => resolve();
		tx.onerror = () => reject(tx.error);
		tx.onabort = () => reject(tx.error ?? new DOMException('Transaction aborted', 'AbortError'));
	});
}

/** Lets a newer version of the app upgrade the database; this page then opens it again. */
function yieldToUpgrades(db: IDBDatabase) {
	db.onversionchange = () => db.close();
}

function indexedStore(db: IDBDatabase): PendingStore {
	yieldToUpgrades(db);
	async function run<T>(
		mode: IDBTransactionMode,
		work: (store: IDBObjectStore) => IDBRequest<T>
	): Promise<T> {
		const tx = db.transaction(storeName, mode);
		const req = work(tx.objectStore(storeName));
		await finished(tx);
		return req.result;
	}
	return {
		list: async () =>
			(await run('readonly', (s) => s.getAllKeys())).filter(
				(key): key is string => typeof key === 'string'
			),
		add: async (barcode) => {
			await run('readwrite', (s) => s.put({ barcode }));
		},
		remove: async (barcode) => {
			await run('readwrite', (s) => s.delete(barcode));
		}
	};
}

function browserDatabases(): IDBFactory | undefined {
	try {
		return globalThis.indexedDB;
	} catch {
		return undefined;
	}
}

/**
 * The device's IndexedDB store of waiting codes, or null where IndexedDB is missing, refuses to
 * open or fails while opening, as some private windows do.
 */
export function openPendingStore(
	factory: IDBFactory | undefined = browserDatabases()
): Promise<PendingStore | null> {
	return new Promise((resolve) => {
		let req: IDBOpenDBRequest;
		try {
			if (!factory) throw new Error('no IndexedDB');
			req = factory.open(dbName, 1);
		} catch {
			resolve(null);
			return;
		}
		req.onupgradeneeded = () => {
			if (!req.result.objectStoreNames.contains(storeName)) {
				req.result.createObjectStore(storeName, { keyPath: 'barcode' });
			}
		};
		req.onsuccess = () => resolve(indexedStore(req.result));
		req.onerror = () => resolve(null);
	});
}

/**
 * How one waiting code's POST ended: in the queue, off the device for another reason, kept for the
 * next try, or the run stops.
 */
type Outcome = 'queued' | 'settled' | 'kept' | 'stop';

/**
 * Barcodes scanned while the check got no answer, kept on the device until the scan queue takes
 * them. Only the barcode is kept: no price, pick or form.
 */
export class PendingScans {
	/** Codes waiting on this device, as last read from its store. */
	count = $state(0);
	/**
	 * Goes up each time codes leave the device as this tab sees it: its run took some off, or a
	 * read found fewer than it counted, as when another tab sent them. A code kept meanwhile
	 * cannot hide a departure, as it can in the count.
	 */
	departures = $state(0);
	/** Whether this device keeps such codes; null until its store was opened. */
	available = $state<boolean | null>(null);
	/** The sign-in turned the API's requests away: the page must reload to sign in again. */
	expired = $state(false);
	readonly #fetch: typeof fetch;
	readonly #open: () => Promise<PendingStore | null>;
	readonly #lock: SendLock;
	#store: Promise<PendingStore | null> | null = null;
	#sending: Promise<void> | null = null;

	constructor(
		fetchFn: typeof fetch,
		open: () => Promise<PendingStore | null>,
		lock: SendLock = browserLock
	) {
		this.#fetch = fetchFn;
		this.#open = open;
		this.#lock = lock;
	}

	/** The store, opened once; a store that failed an operation is opened again next time. */
	#opened(): Promise<PendingStore | null> {
		this.#store ??= this.#open().then(
			(store) => {
				this.available = store !== null;
				return store;
			},
			() => {
				this.available = false;
				return null;
			}
		);
		return this.#store;
	}

	/** Drops a store that failed, so the next use opens it again. */
	#lost() {
		this.#store = null;
	}

	/** Sets the count; left says this tab's run took codes off the device. */
	#counted(count: number, left = false) {
		if (left || count < this.count) this.departures++;
		this.count = count;
	}

	async #recount(store: PendingStore, left = false) {
		try {
			this.#counted((await store.list()).length, left);
		} catch {
			this.#lost();
			if (left) this.departures++;
		}
	}

	/**
	 * Keeps a code whose check got no answer, in the form the server stores it, so two spellings of
	 * one barcode wait once. False when the device cannot keep it; until a later write succeeds,
	 * the device then counts as keeping no codes.
	 */
	async keep(barcode: string): Promise<boolean> {
		const store = await this.#opened();
		if (!store) return false;
		try {
			await store.add(canonicalBarcode(barcode));
		} catch {
			this.available = false;
			this.#lost();
			return false;
		}
		await this.#recount(store);
		return true;
	}

	/** Notes that the sign-in turned a request away, as a scan's check found. */
	sessionExpired() {
		this.expired = true;
	}

	/**
	 * Sends every waiting code to the scan queue, one run at a time across the app's tabs, after
	 * the queue removals on their way, so a removal never races the same code queued again. A code
	 * leaves the device once the queue took it (200 or 201, even if the body is lost), an edition
	 * carries it (409 on barcode) or the queue refuses it (422); any other answer keeps it for the
	 * next run. No answer within postTimeout, or the sign-in turning the request away, ends the
	 * run. While another tab sends, this one only reads the count.
	 */
	send(): Promise<void> {
		this.#sending ??= this.#lock(() => this.#sendAll())
			.then(async (ran) => {
				if (!ran) await this.#refresh();
			})
			.finally(() => {
				this.#sending = null;
			});
		return this.#sending;
	}

	async #refresh() {
		const store = await this.#opened();
		if (store) await this.#recount(store);
	}

	async #sendAll(): Promise<void> {
		const store = await this.#opened();
		if (!store) return;
		let codes: string[];
		try {
			codes = await store.list();
		} catch {
			this.#lost();
			return;
		}
		this.#counted(codes.length);
		if (codes.length === 0) return;
		await queueDeletesSettled();
		const run = { left: false };
		try {
			await this.#sendEach(store, codes, run);
		} catch {
			this.#lost();
			if (run.left) this.departures++;
			return;
		}
		await this.#recount(store, run.left);
	}

	/**
	 * Sends codes one after another, so a run that gets no answer stops before the rest; run.left
	 * notes that a code left the device.
	 */
	async #sendEach(
		store: PendingStore,
		codes: readonly string[],
		run: { left: boolean }
	): Promise<void> {
		const [barcode, ...rest] = codes;
		if (barcode === undefined) return;
		const outcome = await this.#post(barcode);
		if (outcome === 'stop') return;
		if (outcome !== 'kept') {
			await store.remove(barcode);
			run.left = true;
		}
		if (outcome === 'queued') queueWaiting(barcode);
		await this.#sendEach(store, rest, run);
	}

	#post(barcode: string): Promise<Outcome> {
		return withDeadline(postTimeout, async (signal) => {
			const res = await this.#fetch('/api/scan-queue', {
				method: 'POST',
				headers: { accept: 'application/json', 'content-type': 'application/json' },
				body: JSON.stringify({ barcode, reason: 'lookup_failed', detail: offlineDetail }),
				redirect: 'manual',
				signal
			});
			return this.#outcome(res);
		}).catch((): Outcome => 'stop');
	}

	/**
	 * What the scan queue's answer means for the code. A 422 can never succeed, so it settles by its
	 * status; a 409 settles only as the API's own conflict on the barcode. A 200 or 201 puts the
	 * code in the queue, so its waiting scans read as queued.
	 */
	async #outcome(res: Response): Promise<Outcome> {
		if (res.type === 'opaqueredirect' || res.status === 401) {
			this.expired = true;
			return 'stop';
		}
		const queued = res.status === 200 || res.status === 201;
		const settled =
			queued || res.status === 422 || (res.status === 409 && (await errorField(res)) === 'barcode');
		if (settled) this.expired = false;
		if (queued) return 'queued';
		return settled ? 'settled' : 'kept';
	}

	/**
	 * Sends now, whenever the browser comes online or the app is shown again, and every interval,
	 * as another tab may have kept codes this one has not counted. Returns what stops it.
	 */
	start(
		win: Pick<EventTarget, 'addEventListener' | 'removeEventListener'>,
		doc: Pick<Document, 'addEventListener' | 'removeEventListener' | 'visibilityState'>,
		every = sendInterval
	): () => void {
		const send = () => void this.send();
		const shown = () => {
			if (doc.visibilityState === 'visible') send();
		};
		send();
		win.addEventListener('online', send);
		doc.addEventListener('visibilitychange', shown);
		const timer = setInterval(send, every);
		return () => {
			win.removeEventListener('online', send);
			doc.removeEventListener('visibilitychange', shown);
			clearInterval(timer);
		};
	}
}

/** The app's waiting codes, in this browser's IndexedDB. */
export const pendingScans = new PendingScans(
	(input, init) => fetch(input, init),
	() => openPendingStore()
);
