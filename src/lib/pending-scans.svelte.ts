import { ApiError, request } from './api';
import { queueDeletesSettled } from './queue-deletes';

/** What the sheet says about a code the check got no answer for, once the device kept it. */
export const offlineSavedMessage =
	'Brak połączenia. Kod zapisano i trafi do kolejki, gdy połączenie wróci.';

/** The scan queue's detail for a code scanned without a connection. */
export const offlineDetail = 'Zeskanowano bez połączenia';

/** How often codes still waiting are sent again, for a weak signal the browser calls online. */
export const sendInterval = 20_000;

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

/** How one waiting code's POST ended: in the queue, kept for the next try, or the run stops. */
type Outcome = 'settled' | 'kept' | 'stop';

/**
 * Barcodes scanned while the check got no answer, kept on the device until the scan queue takes
 * them. Only the barcode is kept: no price, pick or form.
 */
export class PendingScans {
	/** Codes waiting on this device, as last read from its store. */
	count = $state(0);
	/** Whether this device keeps such codes; null until its store was opened. */
	available = $state<boolean | null>(null);
	/** The sign-in turned the API's requests away: the page must reload to sign in again. */
	expired = $state(false);
	readonly #fetch: typeof fetch;
	readonly #open: () => Promise<PendingStore | null>;
	#store: Promise<PendingStore | null> | null = null;
	#sending: Promise<void> | null = null;

	constructor(fetchFn: typeof fetch, open: () => Promise<PendingStore | null>) {
		this.#fetch = fetchFn;
		this.#open = open;
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

	async #recount(store: PendingStore) {
		try {
			this.count = (await store.list()).length;
		} catch {
			this.#lost();
		}
	}

	/** Keeps a code whose check got no answer; false when the device cannot keep it. */
	async keep(barcode: string): Promise<boolean> {
		const store = await this.#opened();
		if (!store) return false;
		try {
			await store.add(barcode);
		} catch {
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
	 * Sends every waiting code to the scan queue, one run at a time, after the queue removals on
	 * their way, so a removal never races the same code queued again. A code leaves the device
	 * once the queue took it (200 or 201), an edition carries it (409) or the queue can never take
	 * it (422); any other answer keeps it for the next run. No answer, or the sign-in turning the
	 * request away, ends the run.
	 */
	send(): Promise<void> {
		this.#sending ??= this.#sendAll().finally(() => {
			this.#sending = null;
		});
		return this.#sending;
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
		this.count = codes.length;
		if (codes.length === 0) return;
		await queueDeletesSettled();
		try {
			await this.#sendEach(store, codes);
		} catch {
			this.#lost();
			return;
		}
		await this.#recount(store);
	}

	/** Sends codes one after another, so a run that gets no answer stops before the rest. */
	async #sendEach(store: PendingStore, codes: readonly string[]): Promise<void> {
		const [barcode, ...rest] = codes;
		if (barcode === undefined) return;
		const outcome = await this.#post(barcode);
		if (outcome === 'stop') return;
		if (outcome === 'settled') await store.remove(barcode);
		await this.#sendEach(store, rest);
	}

	async #post(barcode: string): Promise<Outcome> {
		try {
			await request(this.#fetch, 'POST', '/api/scan-queue', {
				barcode,
				reason: 'lookup_failed',
				detail: offlineDetail
			});
		} catch (err) {
			const status = err instanceof ApiError ? err.status : -1;
			if (status === 401) {
				this.expired = true;
				return 'stop';
			}
			if (status === 0) return 'stop';
			if (status !== 409 && status !== 422) return 'kept';
		}
		this.expired = false;
		return 'settled';
	}

	/**
	 * Sends now, whenever the browser comes online or the app is shown again, and every interval
	 * while codes wait. Returns what stops it.
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
		const timer = setInterval(() => {
			if (this.count > 0) send();
		}, every);
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
