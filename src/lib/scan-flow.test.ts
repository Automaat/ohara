import { beforeEach, describe, expect, it, vi } from 'vitest';
import { invalidBarcodeMessage } from './api';
import { formatPLN, type EditionDetail } from './collection';
import { queueTally } from './queue-tally.svelte';
import { recentScans } from './recent.svelte';
import {
	ScanFlow,
	checkTimeout,
	announceSettle,
	resetVisit,
	resultAnnouncement,
	type Keeper
} from './scan-flow.svelte';
import { resetSession, session } from './session.svelte';
import type { Candidate, KeyValueStore } from './scan';

type Reply = [number, unknown] | (() => Promise<[number, unknown]>);
type Call = { method: string; url: string; body: unknown };

/** A fetch that answers "METHOD /path" from routes, in order when a route has several replies. */
function server(routes: Record<string, Reply | Reply[]>) {
	const calls: Call[] = [];
	const fetchFn: typeof fetch = async (input, init) => {
		const method = init?.method ?? 'GET';
		const url = String(input);
		calls.push({ method, url, body: init?.body ? JSON.parse(String(init.body)) : undefined });
		const route = routes[`${method} ${url}`];
		const queued =
			Array.isArray(route) && (Array.isArray(route[0]) || typeof route[0] === 'function');
		const reply = queued ? (route as Reply[]).shift() : (route as Reply | undefined);
		if (reply === undefined) throw new TypeError(`unexpected ${method} ${url}`);
		const [status, body] = typeof reply === 'function' ? await reply() : reply;
		return new Response(body === null ? null : JSON.stringify(body), { status });
	};
	return { fetchFn, calls, paths: () => calls.map((c) => `${c.method} ${c.url}`) };
}

function memory(initial: Record<string, string> = {}): KeyValueStore & {
	data: Record<string, string>;
} {
	const data = { ...initial };
	return {
		data,
		getItem: (k) => data[k] ?? null,
		setItem: (k, v) => {
			data[k] = v;
		}
	};
}

const movie: Candidate = {
	type: 'movie',
	title: { name: 'Diuna: Część druga', original_title: 'Dune: Part Two', year: 2024, tmdb_id: 1 },
	edition: { barcode: '5901234123457', format: 'uhd', packaging: ['steelbook'] },
	provider: 'movie',
	confidence: 0.9
};

const book: Candidate = {
	type: 'book',
	title: { name: 'Lalka', authors: ['Bolesław Prus'] },
	edition: { barcode: '5901234123457', binding: 'hardcover' },
	provider: 'bn',
	confidence: 0.5
};

/** The device's waiting codes; can is false where it keeps none. */
function device(can = true): Keeper & { kept: string[]; expired: number } {
	const d = {
		kept: [] as string[],
		expired: 0,
		keep: async (barcode: string) => {
			if (can) d.kept.push(barcode);
			return can;
		},
		sessionExpired: () => {
			d.expired++;
		}
	};
	return d;
}

const noAnswer: Reply = () => Promise.reject(new TypeError('Failed to fetch'));

const owned = {
	id: 5,
	title_id: 3,
	barcode: '5901234123457',
	format: 'dvd',
	binding: null,
	label: '',
	packaging: [],
	title: { id: 3, name: 'Rejs', kind: 'movie', authors: [], year: 1970 },
	copies: []
} as unknown as EditionDetail;

const code = '5901234123457';
const other = '9788324000012';
const noEdition = { [`GET /api/editions?barcode=${code}`]: [200, []] as Reply };

function lookup(candidates: Candidate[], needs: boolean): Record<string, Reply> {
	return {
		...noEdition,
		[`GET /api/lookup/${code}`]: [
			200,
			{ barcode: code, candidates, needs_confirmation: needs, errors: [] }
		]
	};
}

function copyReply(over: Record<string, unknown> = {}): Reply {
	return [201, { id: 40, edition_id: 5, price_cents: null, max_price_cents: null, ...over }];
}

function ownedRoutes(copies: Reply | Reply[] = copyReply()): Record<string, Reply | Reply[]> {
	return {
		[`GET /api/editions?barcode=${code}`]: [200, [{ id: 5, barcode: code }]],
		'GET /api/editions/5': [200, owned],
		'POST /api/copies?cover=auto': copies
	};
}

beforeEach(() => {
	resetVisit();
	resetSession();
	recentScans.list = [];
});

describe('ScanFlow.load', () => {
	it('offers a copy of an edition already in the collection', async () => {
		const s = server(ownedRoutes());
		const flow = new ScanFlow(s.fetchFn, memory());
		await flow.load(code);
		expect(flow.phase).toEqual({ kind: 'ready', status: 'owned' });
		expect(flow.item?.target).toEqual({ kind: 'owned', edition: owned });
		expect(flow.form).toMatchObject({ kind: 'movie', price: '', purchased_on: '' });
		expect(flow.choosing).toBe(false);
	});

	it('rechecks the sources only when asked', async () => {
		const s = server({
			...lookup([movie], false),
			[`GET /api/lookup/${code}?recheck=1`]: [
				200,
				{ barcode: code, candidates: [movie], needs_confirmation: false, errors: [] }
			]
		});
		const flow = new ScanFlow(s.fetchFn, memory());
		await flow.load(code, false, true);
		await flow.load(code);
		expect(s.paths().filter((p) => p.startsWith('GET /api/lookup/'))).toEqual([
			`GET /api/lookup/${code}?recheck=1`,
			`GET /api/lookup/${code}`
		]);
	});

	it('lists candidates with none picked when the lookup asks for a pick', async () => {
		const s = server(lookup([book, movie], true));
		const flow = new ScanFlow(s.fetchFn, memory());
		await flow.load(code);
		expect(flow.phase).toEqual({ kind: 'ready', status: 'found' });
		expect(flow.choosing).toBe(true);
		expect(flow.candidate).toBeNull();
		expect(flow.item?.target).toEqual({ kind: 'new', barcode: code, candidate: null });
		expect(await flow.save()).toBeNull();
		expect(s.paths()).not.toContain('POST /api/titles');
	});

	it('takes the lone confident candidate without a list', async () => {
		const s = server(lookup([movie], false));
		const flow = new ScanFlow(s.fetchFn, memory());
		await flow.load(code);
		expect(flow.choosing).toBe(false);
		expect(flow.candidate).toEqual(movie);
		expect(flow.form).toMatchObject({
			kind: 'movie',
			name: 'Diuna: Część druga',
			format: 'uhd',
			packaging: ['steelbook']
		});
	});

	it('queues a code no source knows', async () => {
		const entry = { id: 1, barcode: code, reason: 'not_found', detail: '', scans: 1 };
		const s = server({
			...noEdition,
			[`GET /api/lookup/${code}`]: [200, { barcode: code, candidates: [], errors: [] }],
			'POST /api/scan-queue': [201, entry]
		});
		const flow = new ScanFlow(s.fetchFn, memory());
		await flow.load(code);
		expect(flow.phase).toEqual({ kind: 'queued', entry });
		flow.dismiss();
		flow.dismiss();
		expect(recentScans.list).toMatchObject([
			{ barcode: code, outcome: 'queued', href: '/scan/queue' }
		]);
	});

	it.each([
		['no barcode shape', ' 12ab ', '„12ab” nie wygląda na kod kreskowy.'],
		['a wrong check digit', '5901234123458', invalidBarcodeMessage]
	])('refuses %s without a request', async (_, raw, message) => {
		const s = server({});
		const flow = new ScanFlow(s.fetchFn, memory());
		await flow.load(raw);
		expect(flow.phase).toEqual({ kind: 'invalid', message });
		expect(s.calls).toHaveLength(0);
	});

	it('tells a barcode the server refuses from a failed request', async () => {
		const refused = server({
			[`GET /api/editions?barcode=${code}`]: [422, { error: 'zły kod', field: 'barcode' }]
		});
		const a = new ScanFlow(refused.fetchFn, memory());
		await a.load(code);
		expect(a.phase).toMatchObject({ kind: 'invalid' });

		const down = server({ [`GET /api/editions?barcode=${code}`]: [500, { error: 'boom' }] });
		const b = new ScanFlow(down.fetchFn, memory());
		await b.load(code);
		expect(b.phase).toMatchObject({ kind: 'failed' });
	});

	it('opens a blank form for a code typed by hand', async () => {
		const s = server({});
		const flow = new ScanFlow(s.fetchFn, memory());
		await flow.load(other, true);
		expect(flow.phase).toEqual({ kind: 'ready', status: 'missing' });
		expect(flow.form).toMatchObject({ kind: 'book', name: '' });
		expect(s.calls).toHaveLength(0);
		flow.dismiss();
		expect(recentScans.list).toHaveLength(0);
	});

	it('applies only the newest of overlapping loads', async () => {
		let release: (r: [number, unknown]) => void = () => {};
		const slow = new Promise<[number, unknown]>((resolve) => (release = resolve));
		const s = server({
			[`GET /api/editions?barcode=${code}`]: () => slow,
			[`GET /api/editions?barcode=${other}`]: [200, []],
			[`GET /api/lookup/${other}`]: [
				200,
				{ barcode: other, candidates: [book], needs_confirmation: true, errors: [] }
			]
		});
		const flow = new ScanFlow(s.fetchFn, memory());
		const first = flow.load(code);
		await flow.load(other);
		release([200, []]);
		await first.catch(() => undefined);
		expect(flow.item?.barcode).toBe(other);
		expect(flow.code).toBe(other);
	});
});

describe('ScanFlow.load without a connection', () => {
	it('keeps a code whose check gets no answer and logs it as waiting', async () => {
		const kept = device();
		const flow = new ScanFlow(
			server({ [`GET /api/editions?barcode=${code}`]: noAnswer }).fetchFn,
			memory(),
			kept
		);
		await flow.load(code);
		expect(flow.phase).toEqual({ kind: 'offline' });
		expect(kept.kept).toEqual([code]);
		flow.dismiss();
		flow.dismiss();
		expect(recentScans.list).toMatchObject([
			{ barcode: code, outcome: 'waiting', label: 'Zapisano bez połączenia' }
		]);
	});

	it('keeps a code whose queue entry gets no answer after the lookup failed', async () => {
		const kept = device();
		const s = server({
			...noEdition,
			[`GET /api/lookup/${code}`]: noAnswer,
			'POST /api/scan-queue': noAnswer
		});
		const flow = new ScanFlow(s.fetchFn, memory(), kept);
		await flow.load(code);
		expect(flow.phase).toEqual({ kind: 'offline' });
		expect(kept.kept).toEqual([code]);
	});

	it('reports the failure as before and logs nothing where the device keeps no codes', async () => {
		const flow = new ScanFlow(
			server({ [`GET /api/editions?barcode=${code}`]: noAnswer }).fetchFn,
			memory(),
			device(false)
		);
		await flow.load(code);
		expect(flow.phase).toEqual({ kind: 'failed', message: 'Serwer nie odpowiada.' });
		flow.dismiss();
		expect(recentScans.list).toHaveLength(0);
	});

	it.each([
		[true, [{ barcode: code, outcome: 'waiting', label: 'Zapisano, sesja wygasła' }]],
		[false, []]
	])(
		'keeps the code and asks to sign in again when the sign-in turns the check away (stored: %s)',
		async (can, logged) => {
			const kept = device(can);
			const s = server({ [`GET /api/editions?barcode=${code}`]: [401, null] });
			const flow = new ScanFlow(s.fetchFn, memory(), kept);
			await flow.load(code);
			expect(flow.phase).toEqual({ kind: 'expired', saved: can });
			expect(kept.expired).toBe(1);
			flow.dismiss();
			expect(recentScans.list).toMatchObject(logged);
		}
	);

	it('keeps a code whose check answer breaks off after its status', async () => {
		const kept = device();
		const fetchFn: typeof fetch = async () =>
			new Response(
				new ReadableStream({
					start: (controller) => controller.error(new TypeError('network'))
				}),
				{ status: 200 }
			);
		const flow = new ScanFlow(fetchFn, memory(), kept);
		await flow.load(code);
		expect(flow.phase).toEqual({ kind: 'offline' });
		expect(kept.kept).toEqual([code]);
	});

	it('keeps a code whose check the network holds open past its deadline', async () => {
		vi.useFakeTimers();
		const kept = device();
		const fetchFn: typeof fetch = (_, init) =>
			new Promise<Response>((_resolve, reject) => {
				init?.signal?.addEventListener('abort', () => reject(init.signal?.reason));
			});
		const flow = new ScanFlow(fetchFn, memory(), kept);
		const loading = flow.load(code);
		await vi.advanceTimersByTimeAsync(checkTimeout - 1);
		expect(flow.phase).toEqual({ kind: 'loading' });
		await vi.advanceTimersByTimeAsync(1);
		await loading;
		expect(flow.phase).toEqual({ kind: 'offline' });
		expect(kept.kept).toEqual([code]);
		vi.useRealTimers();
	});

	it('asks to sign in again when the lookup is turned away and the queue fails', async () => {
		const kept = device();
		const s = server({
			...noEdition,
			[`GET /api/lookup/${code}`]: [401, null],
			'POST /api/scan-queue': [503, { error: 'down' }]
		});
		const flow = new ScanFlow(s.fetchFn, memory(), kept);
		await flow.load(code);
		expect(flow.phase).toEqual({ kind: 'expired', saved: true });
		expect(kept.kept).toEqual([code]);
		expect(kept.expired).toBe(1);
		expect(s.paths()).not.toContain('POST /api/scan-queue');
	});

	it('keeps nothing for a check the server answered', async () => {
		const kept = device();
		const down = server({ [`GET /api/editions?barcode=${code}`]: [502, null] });
		const a = new ScanFlow(down.fetchFn, memory(), kept);
		await a.load(code);
		expect(a.phase).toEqual({ kind: 'failed', message: 'Serwer zwrócił błąd 502.' });
		const refused = server({
			[`GET /api/editions?barcode=${code}`]: [422, { error: 'zły kod', field: 'barcode' }]
		});
		const b = new ScanFlow(refused.fetchFn, memory(), kept);
		await b.load(code);
		expect(b.phase).toMatchObject({ kind: 'invalid' });
		expect(kept.kept).toEqual([]);
		expect(kept.expired).toBe(0);
	});

	it('keeps a code the user moved on from, without touching the newer one', async () => {
		let drop: () => void = () => {};
		const slow = new Promise<[number, unknown]>((_, reject) => {
			drop = () => reject(new TypeError('Failed to fetch'));
		});
		const kept = device();
		const s = server({
			[`GET /api/editions?barcode=${code}`]: () => slow,
			[`GET /api/editions?barcode=${other}`]: [200, []],
			[`GET /api/lookup/${other}`]: [
				200,
				{ barcode: other, candidates: [book], needs_confirmation: true, errors: [] }
			]
		});
		const flow = new ScanFlow(s.fetchFn, memory(), kept);
		const first = flow.load(code);
		await flow.load(other);
		drop();
		await first;
		expect(kept.kept).toEqual([code]);
		expect(flow.phase).toEqual({ kind: 'ready', status: 'found' });
		expect(flow.item?.barcode).toBe(other);
	});
});

describe('ScanFlow picks', () => {
	it('keeps what the user typed about the purchase when another candidate is picked', async () => {
		const s = server(lookup([movie, book], true));
		const flow = new ScanFlow(s.fetchFn, memory());
		await flow.load(code);
		flow.pick(0);
		flow.form.price = '12';
		flow.form.shop = 'Empik';
		flow.form.packaging = [];
		flow.choose();
		expect(flow.choosing).toBe(true);
		flow.pick(1);
		expect(flow.candidate).toEqual(book);
		expect(flow.form).toMatchObject({
			kind: 'book',
			name: 'Lalka',
			binding: 'hardcover',
			price: '12',
			shop: 'Empik'
		});
		flow.pick(9);
		expect(flow.candidate).toEqual(book);
	});

	it('takes the packaging of the picked candidate, never of one not picked', async () => {
		const bare: Candidate = { ...movie, edition: { barcode: code, format: 'dvd' } };
		const s = server({
			...lookup([movie, bare], true),
			'GET /api/titles?tmdb_id=1': [200, []],
			'POST /api/titles': [201, { id: 9, name: 'Diuna: Część druga', kind: 'movie' }],
			'POST /api/editions': [
				201,
				{
					id: 21,
					title_id: 9,
					barcode: code,
					format: 'uhd',
					binding: null,
					cover_url: 'https://image.tmdb.org/t/p/w342/d.jpg'
				}
			],
			'POST /api/copies?cover=auto': copyReply()
		});
		const flow = new ScanFlow(s.fetchFn, memory());
		await flow.load(code);
		expect(flow.form).toMatchObject({ format: '', packaging: [] });
		flow.pick(1);
		expect(flow.form).toMatchObject({ format: 'dvd', packaging: [] });
		await flow.save();
		const edition = s.calls.find((c) => c.url === '/api/editions');
		expect(edition?.body).toMatchObject({ format: 'dvd', packaging: [] });

		flow.pick(0);
		flow.form.packaging = ['digipak'];
		flow.pick(1);
		expect(flow.form.packaging).toEqual([]);
	});

	it('does nothing on an owned edition', async () => {
		const s = server(ownedRoutes());
		const flow = new ScanFlow(s.fetchFn, memory());
		await flow.load(code);
		flow.pick(0);
		flow.choose();
		expect(flow.item?.target.kind).toBe('owned');
	});
});

describe('ScanFlow.search', () => {
	const url = (q: string) => `GET /api/search?kind=movie&q=${q}&barcode=${code}&format=uhd`;

	async function found(routes: Record<string, Reply | Reply[]>) {
		const s = server({ ...lookup([movie], false), ...routes });
		const flow = new ScanFlow(s.fetchFn, memory());
		await flow.load(code);
		return flow;
	}

	it('fills the form from a single result', async () => {
		const flow = await found({ [url('Diuna')]: [200, { candidates: [movie] }] });
		flow.query = ' Diuna ';
		await flow.search();
		expect(flow.choosing).toBe(false);
		expect(flow.candidate).toEqual(movie);
	});

	it('lists several results for a pick', async () => {
		const flow = await found({ [url('Diuna')]: [200, { candidates: [movie, movie] }] });
		flow.query = 'Diuna';
		await flow.search();
		expect(flow.choosing).toBe(true);
		expect(flow.item?.candidates).toHaveLength(2);
	});

	it('says when nothing was found or the source failed', async () => {
		const flow = await found({
			[url('Nic')]: [200, { candidates: [] }],
			[url('Zepsute')]: [502, { error: 'down' }]
		});
		flow.query = 'Nic';
		await flow.search();
		expect(flow.notice).toBe('Nic nie znaleziono dla „Nic”.');
		flow.query = 'Zepsute';
		await flow.search();
		expect(flow.notice).toBe('Źródło nie odpowiada, spróbuj za chwilę.');
		expect(flow.searching).toBe(false);
	});

	it('drops a search answer that arrives after the user picked a candidate', async () => {
		let release: (r: [number, unknown]) => void = () => {};
		const held = new Promise<[number, unknown]>((resolve) => (release = resolve));
		const s = server({
			...lookup([movie, book], true),
			'GET /api/search?kind=movie&q=Diuna&barcode=5901234123457': () => held
		});
		const flow = new ScanFlow(s.fetchFn, memory());
		await flow.load(code);
		flow.query = 'Diuna';
		const searching = flow.search();
		expect(flow.searching).toBe(true);
		flow.pick(1);
		expect(flow.searching).toBe(false);
		release([200, { candidates: [{ ...movie, title: { name: 'Search result' } }] }]);
		await searching;
		expect(flow.candidate).toEqual(book);
		expect(flow.form.name).toBe('Lalka');
		expect(flow.item?.candidates).toHaveLength(2);
	});

	it('ignores a blank query', async () => {
		const flow = await found({});
		flow.query = '  ';
		await flow.search();
		expect(flow.searching).toBe(false);
	});
});

describe('ScanFlow.save', () => {
	it('adds an owned copy and carries shop, new or used and date to the next scan', async () => {
		const store = memory();
		const s = server(ownedRoutes([copyReply({ price_cents: 2999 }), copyReply()]));
		const flow = new ScanFlow(s.fetchFn, store);
		await flow.load(code);
		Object.assign(flow.form, {
			price: '29,99',
			purchased_on: '2026-11-18',
			shop: 'Empik',
			acquired_as: 'used',
			condition: 'good'
		});
		const added = await flow.save();
		expect(added).toMatchObject({ barcode: code, next: null, saved: { editionID: 5 } });
		expect(s.calls.at(-1)?.body).toMatchObject({
			status: 'owned',
			price_cents: 2999,
			purchased_on: '2026-11-18',
			shop: 'Empik',
			acquired_as: 'used',
			condition: 'good'
		});
		expect(JSON.parse(store.data['ohara.scan.sticky'])).toEqual({
			shop: 'Empik',
			acquired_as: 'used',
			shops: ['Empik']
		});
		expect(recentScans.list[0]).toMatchObject({
			outcome: 'added',
			label: `Rejs · ${formatPLN(2999)}`,
			href: '/copies/40'
		});
		flow.dismiss();
		expect(recentScans.list).toHaveLength(1);

		await flow.load(code);
		expect(flow.form).toMatchObject({
			price: '',
			purchased_on: '2026-11-18',
			shop: 'Empik',
			acquired_as: 'used',
			condition: ''
		});
		expect(flow.sticky.shops).toEqual(['Empik']);
	});

	it('adds a wish without touching what carries over', async () => {
		const store = memory({
			'ohara.scan.sticky': '{"shop":"OLX","acquired_as":"new","shops":["OLX"]}'
		});
		const s = server(ownedRoutes(copyReply({ status: 'wishlist', max_price_cents: 6000 })));
		const flow = new ScanFlow(s.fetchFn, store);
		await flow.load(code);
		flow.form.purchased_on = '2026-11-18';
		flow.wish(true);
		flow.form.price = '60';
		await flow.save();
		expect(s.calls.at(-1)?.body).toMatchObject({
			status: 'wishlist',
			max_price_cents: 6000,
			shop: '',
			acquired_as: null,
			purchased_on: null
		});
		expect(JSON.parse(store.data['ohara.scan.sticky'])).toEqual({
			shop: 'OLX',
			acquired_as: 'new',
			shops: ['OLX']
		});
		expect(recentScans.list[0].label).toBe(`Rejs · ${formatPLN(6000)} · lista życzeń`);
		flow.wish(false);
		expect(flow.form.status).toBe('owned');
	});

	it('logs the copy the server kept when a retry after a lost answer changed the form', async () => {
		const lost = (): Promise<[number, unknown]> => Promise.reject(new TypeError('lost'));
		const store = memory();
		const s = server(ownedRoutes([lost, copyReply({ status: 'owned', price_cents: 1000 })]));
		const flow = new ScanFlow(s.fetchFn, store);
		await flow.load(code);
		Object.assign(flow.form, { price: '10', shop: 'Empik', purchased_on: '2026-11-18' });
		expect(await flow.save()).toBeNull();
		flow.wish(true);
		expect(await flow.save()).not.toBeNull();
		const keys = s.calls.filter((c) => c.method === 'POST').map((c) => c.body);
		expect(keys).toMatchObject([
			{ status: 'owned', request_key: flow.item?.key },
			{ status: 'wishlist', request_key: flow.item?.key }
		]);
		expect(recentScans.list[0].label).toBe(`Rejs · ${formatPLN(1000)}`);
		expect(JSON.parse(store.data['ohara.scan.sticky']).shop).toBe('Empik');
		await flow.load(code);
		expect(flow.form.purchased_on).toBe('2026-11-18');
	});

	it('records an owned add of an edition it found for the session tray', async () => {
		const s = server(ownedRoutes(copyReply({ price_cents: 5000 })));
		const flow = new ScanFlow(s.fetchFn, memory());
		await flow.load(code);
		flow.form.price = '50';
		const added = await flow.save();
		expect(session.adds).toHaveLength(1);
		expect(added?.add).toEqual(session.adds[0]);
		expect(session.adds[0]).toMatchObject({
			copyID: 40,
			editionID: 5,
			titleID: 3,
			createdEdition: false,
			createdTitle: false,
			name: 'Rejs',
			medium: 'DVD',
			kind: 'movie',
			status: 'owned',
			priceCents: 5000,
			recentID: recentScans.list[0].id
		});
	});

	it('records a new item with what it created, its medium and cover', async () => {
		const s = server({
			...lookup([{ ...movie, cover_url: 'https://image.tmdb.org/t/p/w342/d.jpg' }], false),
			'GET /api/titles?tmdb_id=1': [200, []],
			'POST /api/titles': [201, { id: 9, name: 'Diuna: Część druga', kind: 'movie' }],
			'POST /api/editions': [
				201,
				{
					id: 21,
					title_id: 9,
					barcode: code,
					format: 'uhd',
					binding: null,
					cover_url: 'https://image.tmdb.org/t/p/w342/d.jpg'
				}
			],
			'POST /api/copies?cover=auto': copyReply({ id: 41, edition_id: 21, price_cents: null })
		});
		const flow = new ScanFlow(s.fetchFn, memory());
		await flow.load(code);
		const added = await flow.save();
		expect(added?.add).toMatchObject({
			copyID: 41,
			editionID: 21,
			titleID: 9,
			createdEdition: true,
			createdTitle: true,
			name: 'Diuna: Część druga',
			medium: '4K UHD',
			cover: 'https://image.tmdb.org/t/p/w342/d.jpg',
			priceCents: null
		});
	});

	it('takes a code out of the queue count only when its add created the edition', async () => {
		await queueTally.load(
			server({
				'GET /api/scan-queue': [
					200,
					[
						{ id: 1, barcode: code },
						{ id: 2, barcode: other }
					]
				]
			}).fetchFn
		);
		const s = server({
			...lookup([movie], false),
			'GET /api/titles?tmdb_id=1': [200, []],
			'POST /api/titles': [201, { id: 9, name: 'Diuna: Część druga', kind: 'movie' }],
			'POST /api/editions': [201, { id: 21, title_id: 9, barcode: code, format: 'uhd' }],
			'POST /api/copies?cover=auto': copyReply({ id: 41, edition_id: 21 })
		});
		const found = new ScanFlow(server(ownedRoutes()).fetchFn, memory());
		await found.load(code);
		await found.save();
		expect(queueTally.count).toBe(2);
		const flow = new ScanFlow(s.fetchFn, memory());
		await flow.load(code);
		await flow.save();
		expect(queueTally.count).toBe(1);
	});

	it('records a book by its binding and a wish without a price', async () => {
		const s = server({
			...lookup([book], false),
			'POST /api/titles': [201, { id: 1, name: 'Lalka', kind: 'book' }],
			'POST /api/editions': [
				201,
				{ id: 2, title_id: 1, barcode: code, format: null, binding: 'hardcover' }
			],
			'POST /api/copies?cover=auto': copyReply({
				id: 42,
				edition_id: 2,
				status: 'wishlist',
				max_price_cents: 3000
			})
		});
		const flow = new ScanFlow(s.fetchFn, memory());
		await flow.load(code);
		flow.wish(true);
		flow.form.price = '30';
		const added = await flow.save();
		expect(added?.add).toMatchObject({
			name: 'Lalka',
			medium: 'Twarda oprawa',
			kind: 'book',
			status: 'wishlist',
			priceCents: null
		});
	});

	it('names the medium of the edition another sheet created, not the one it asked for', async () => {
		const s = server({
			...lookup([movie], false),
			'GET /api/titles?tmdb_id=1': [200, []],
			'POST /api/titles': [201, { id: 9, name: 'Diuna: Część druga', kind: 'movie' }],
			'POST /api/editions': [409, { error: 'dup', field: 'barcode' }],
			[`GET /api/editions?barcode=${code}`]: [
				[200, []],
				[200, [{ id: 7, title_id: 4, barcode: code, format: 'bluray', binding: null }]]
			],
			'DELETE /api/titles/9': [204, null],
			'GET /api/titles/4': [200, { id: 4, name: 'Diuna: Część druga', kind: 'movie' }],
			'POST /api/copies?cover=auto': copyReply({ id: 43, edition_id: 7 })
		});
		const flow = new ScanFlow(s.fetchFn, memory());
		await flow.load(code);
		expect(flow.form.format).toBe('uhd');
		const added = await flow.save();
		expect(added?.add).toMatchObject({
			editionID: 7,
			titleID: 4,
			createdEdition: false,
			createdTitle: false,
			medium: 'Blu-ray'
		});
	});

	it('starts the next item when a wedge scanner typed a code into the price', async () => {
		const s = server(ownedRoutes());
		const flow = new ScanFlow(s.fetchFn, memory());
		await flow.load(code);
		flow.form.price = other;
		const added = await flow.save();
		expect(added?.next).toBe(other);
		expect(s.calls.at(-1)?.body).toMatchObject({ price_cents: null });
	});

	it('starts the next item when a wedge scanner typed an ISBN-10 ending in X', async () => {
		const s = server(ownedRoutes());
		const flow = new ScanFlow(s.fetchFn, memory());
		await flow.load(code);
		flow.form.price = '080442957x';
		const added = await flow.save();
		expect(added?.next).toBe('080442957X');
		expect(s.calls.at(-1)?.body).toMatchObject({ price_cents: null });
	});

	it('shows why a save was refused and keeps the wedge code to scan again', async () => {
		const s = server(lookup([movie], false));
		const flow = new ScanFlow(s.fetchFn, memory());
		await flow.load(code);
		flow.form.name = ' ';
		flow.form.price = other;
		expect(await flow.save()).toBeNull();
		expect(flow.issue).toEqual({ field: 'name', message: 'Podaj tytuł.' });
		expect(flow.notice).toBe(`Kod ${other} nie został sprawdzony: zeskanuj go ponownie.`);
		expect(flow.saving).toBe(false);
	});

	it('reports a failed request with its field', async () => {
		const s = server(ownedRoutes([422, { error: 'zła cena', field: 'price_cents' }]));
		const flow = new ScanFlow(s.fetchFn, memory());
		await flow.load(code);
		expect(await flow.save()).toBeNull();
		expect(flow.issue).toMatchObject({ field: 'price_cents' });
		expect(flow.issue?.message).toContain('zła cena');
	});

	it('saves and logs a copy whose barcode the user already left, without reporting it', async () => {
		let release: (r: [number, unknown]) => void = () => {};
		const held = new Promise<[number, unknown]>((resolve) => (release = resolve));
		const store = memory();
		const s = server({
			...ownedRoutes(() => held),
			[`GET /api/editions?barcode=${other}`]: [200, []],
			[`GET /api/lookup/${other}`]: [
				200,
				{ barcode: other, candidates: [book], needs_confirmation: true, errors: [] }
			]
		});
		const flow = new ScanFlow(s.fetchFn, store);
		await flow.load(code);
		flow.form.shop = 'Empik';
		const saving = flow.save();
		await flow.load(other);
		release([201, { id: 41, edition_id: 5, price_cents: null, max_price_cents: null }]);
		expect(await saving).toBeNull();
		expect(flow.item?.barcode).toBe(other);
		expect(flow.issue).toBeNull();
		expect(recentScans.list[0]).toMatchObject({ barcode: code, outcome: 'added' });
		expect(JSON.parse(store.data['ohara.scan.sticky']).shop).toBe('Empik');
	});

	it('saves and logs a copy whose page is gone, without reporting it', async () => {
		let release: (r: [number, unknown]) => void = () => {};
		const held = new Promise<[number, unknown]>((resolve) => (release = resolve));
		const store = memory();
		const flow = new ScanFlow(server(ownedRoutes(() => held)).fetchFn, store);
		await flow.load(code);
		flow.form.shop = 'Empik';
		const saving = flow.save();
		flow.detach();
		release([201, { id: 41, edition_id: 5, price_cents: null, max_price_cents: null }]);
		expect(await saving).toBeNull();
		expect(recentScans.list[0]).toMatchObject({ barcode: code, outcome: 'added' });
		expect(session.adds).toHaveLength(1);
		expect(JSON.parse(store.data['ohara.scan.sticky']).shop).toBe('Empik');
	});

	it('reports nothing when a save fails after its page is gone', async () => {
		let release: (r: [number, unknown]) => void = () => {};
		const held = new Promise<[number, unknown]>((resolve) => (release = resolve));
		const flow = new ScanFlow(server(ownedRoutes(() => held)).fetchFn, memory());
		await flow.load(code);
		flow.form.price = other;
		const saving = flow.save();
		flow.detach();
		release([422, { error: 'zła cena', field: 'price_cents' }]);
		expect(await saving).toBeNull();
		expect(flow.issue).toBeNull();
		expect(flow.notice).toBeNull();
	});

	it('keeps the shop another page saved while a wish here was on its way', async () => {
		let releasePurchase: (r: [number, unknown]) => void = () => {};
		let releaseWish: (r: [number, unknown]) => void = () => {};
		const purchase = new Promise<[number, unknown]>((resolve) => (releasePurchase = resolve));
		const wish = new Promise<[number, unknown]>((resolve) => (releaseWish = resolve));
		const store = memory({
			'ohara.scan.sticky': '{"shop":"OLX","acquired_as":"","shops":["OLX"]}'
		});
		const addPage = new ScanFlow(server(ownedRoutes(() => purchase)).fetchFn, store);
		const scanner = new ScanFlow(server(ownedRoutes(() => wish)).fetchFn, store);
		await addPage.load(code);
		addPage.form.shop = 'Empik';
		const buying = addPage.save();
		await scanner.load(code);
		scanner.wish(true);
		const wishing = scanner.save();
		releasePurchase([201, { id: 41, edition_id: 5, status: 'owned', price_cents: null }]);
		await buying;
		releaseWish([201, { id: 42, edition_id: 5, status: 'wishlist', max_price_cents: null }]);
		await wishing;
		expect(JSON.parse(store.data['ohara.scan.sticky'])).toEqual({
			shop: 'Empik',
			acquired_as: '',
			shops: ['Empik', 'OLX']
		});
	});

	it('keeps the shops of both pages when their purchases answer in turn', async () => {
		let releaseFirst: (r: [number, unknown]) => void = () => {};
		const first = new Promise<[number, unknown]>((resolve) => (releaseFirst = resolve));
		const store = memory();
		const addPage = new ScanFlow(server(ownedRoutes(() => first)).fetchFn, store);
		const scanner = new ScanFlow(server(ownedRoutes()).fetchFn, store);
		await addPage.load(code);
		addPage.form.shop = 'Empik';
		const buying = addPage.save();
		await scanner.load(code);
		scanner.form.shop = 'Allegro';
		await scanner.save();
		releaseFirst([201, { id: 41, edition_id: 5, status: 'owned', price_cents: null }]);
		await buying;
		expect(JSON.parse(store.data['ohara.scan.sticky'])).toMatchObject({
			shop: 'Empik',
			shops: ['Empik', 'Allegro']
		});
	});
});

describe('ScanFlow without a barcode', () => {
	const album: Candidate = {
		type: 'music',
		title: { name: 'Kind of Blue', authors: ['Miles Davis'], year: 1959 },
		edition: { barcode: '', format: 'vinyl', label: 'Columbia', discogs_release_id: 7 },
		provider: 'discogs',
		confidence: 0.6
	};

	it('opens a blank item of the asked kind to search or fill in by hand', () => {
		const s = server({});
		const flow = new ScanFlow(s.fetchFn, memory());
		flow.start('music');
		expect(flow.phase).toEqual({ kind: 'ready', status: 'missing' });
		expect(flow.code).toBe('');
		expect(flow.item).toMatchObject({
			barcode: '',
			byHand: true,
			target: { kind: 'new', barcode: '', candidate: null }
		});
		expect(flow.form).toMatchObject({ kind: 'music', name: '', price: '' });
		flow.dismiss();
		expect(s.calls).toHaveLength(0);
		expect(recentScans.list).toHaveLength(0);
	});

	it('saves a result found by title as a title, edition and copy, outside the recent scans', async () => {
		const cd = { ...album, edition: { ...album.edition, format: 'cd' as const } };
		const s = server({
			'GET /api/search?kind=music&q=Kind+of+Blue': [200, { candidates: [album, cd] }],
			'POST /api/titles': [201, { id: 4, name: 'Kind of Blue', kind: 'music' }],
			'POST /api/editions': [
				201,
				{ id: 30, title_id: 4, barcode: null, format: 'vinyl', binding: null }
			],
			'POST /api/copies?cover=auto': copyReply({ id: 50, edition_id: 30, price_cents: 8000 })
		});
		const flow = new ScanFlow(s.fetchFn, memory());
		flow.start('music');
		flow.query = 'Kind of Blue';
		await flow.search();
		expect(flow.choosing).toBe(true);
		flow.pick(0);
		flow.form.price = '80';
		const added = await flow.save();
		expect(added).toMatchObject({ barcode: '', next: null, saved: { titleID: 4, editionID: 30 } });
		expect(s.calls.find((c) => c.url === '/api/titles')?.body).toMatchObject({
			kind: 'music',
			name: 'Kind of Blue',
			authors: ['Miles Davis'],
			year: 1959
		});
		expect(s.calls.find((c) => c.url === '/api/editions')?.body).toMatchObject({
			title_id: 4,
			barcode: null,
			format: 'vinyl',
			label: 'Columbia',
			discogs_release_id: 7
		});
		expect(recentScans.list).toHaveLength(0);
		expect(session.adds[0]).toMatchObject({
			copyID: 50,
			createdTitle: true,
			createdEdition: true,
			medium: 'Winyl',
			priceCents: 8000,
			recentID: null
		});
	});

	it('removes what a refused add made and starts over, so adding it again makes no duplicate', async () => {
		const s = server({
			'GET /api/search?kind=music&q=Kind+of+Blue': [200, { candidates: [album] }],
			'POST /api/titles': [
				[201, { id: 4, name: 'Kind of Blue', kind: 'music' }],
				[201, { id: 5, name: 'Kind of Blue', kind: 'music' }]
			],
			'POST /api/editions': [
				[201, { id: 30, title_id: 4, barcode: null, format: 'vinyl', binding: null }],
				[201, { id: 31, title_id: 5, barcode: null, format: 'vinyl', binding: null }]
			],
			'POST /api/copies?cover=auto': [
				[500, { error: 'boom' }],
				copyReply({ id: 50, edition_id: 31 })
			],
			'DELETE /api/requests/first': [204, null]
		});
		const flow = new ScanFlow(s.fetchFn, memory());
		flow.start('music');
		flow.item!.key = 'first';
		flow.query = 'Kind of Blue';
		await flow.search();
		expect(await flow.save()).toBeNull();
		expect(flow.issue?.message).toBeTruthy();
		expect(s.paths().at(-1)).toBe('DELETE /api/requests/first');
		expect(flow.item?.key).not.toBe('first');
		expect(flow.item?.made).toEqual({ titles: [], editions: [], titleUnanswered: false });

		const added = await flow.save();
		expect(added?.saved).toMatchObject({ titleID: 5, editionID: 31 });
		const keys = s.calls.filter((c) => c.url === '/api/titles').map((c) => c.body);
		expect(keys).toMatchObject([{ request_key: 'first' }, { request_key: flow.item?.key }]);
	});

	describe('after a lost answer, another pick', () => {
		const rejs: Candidate = {
			type: 'movie',
			title: { name: 'Rejs', year: 1970, tmdb_id: 2 },
			edition: { barcode: '', format: 'dvd' },
			provider: 'tmdb',
			confidence: 0.6
		};
		const diuna = { ...movie, edition: { barcode: '', format: 'uhd' as const } };
		const routes = (discard: Reply | Reply[]): Record<string, Reply | Reply[]> => ({
			'GET /api/search?kind=movie&q=Diuna': [200, { candidates: [diuna, rejs] }],
			'GET /api/titles?tmdb_id=1': [200, []],
			'GET /api/titles?tmdb_id=2': [200, [{ id: 20, name: 'Rejs', kind: 'movie' }]],
			'POST /api/titles': [201, { id: 10, name: 'Diuna: Część druga', kind: 'movie' }],
			'POST /api/editions': [
				[201, { id: 42, title_id: 10, barcode: null, format: 'uhd', binding: null }],
				[201, { id: 43, title_id: 20, barcode: null, format: 'dvd', binding: null }]
			],
			'POST /api/copies?cover=auto': [
				() => Promise.reject(new TypeError('lost')),
				copyReply({ id: 61, edition_id: 43 })
			],
			'DELETE /api/requests/first': discard
		});

		async function lostDiuna(s: ReturnType<typeof server>) {
			const flow = new ScanFlow(s.fetchFn, memory());
			flow.start('movie');
			flow.item!.key = 'first';
			flow.query = 'Diuna';
			await flow.search();
			flow.pick(0);
			expect(await flow.save()).toBeNull();
			expect(flow.item?.key).toBe('first');
			flow.choose();
			flow.pick(1);
			return flow;
		}

		it('removes what the first pick wrote before saving the new one under its own key', async () => {
			const s = server(routes([204, null]));
			const flow = await lostDiuna(s);
			const added = await flow.save();
			expect(added?.saved).toMatchObject({ titleID: 20, titleName: 'Rejs', editionID: 43 });
			const paths = s.paths();
			const discarded = paths.indexOf('DELETE /api/requests/first');
			expect(discarded).toBeGreaterThan(paths.indexOf('POST /api/copies?cover=auto'));
			expect(paths.lastIndexOf('POST /api/editions')).toBeGreaterThan(discarded);
			const editions = s.calls.filter((c) => c.url === '/api/editions').map((c) => c.body);
			expect(editions).toMatchObject([
				{ title_id: 10, request_key: 'first' },
				{ title_id: 20, request_key: flow.item?.key }
			]);
			expect(flow.item?.key).not.toBe('first');
		});

		it('saves nothing while the server has not confirmed the removal', async () => {
			const s = server(
				routes([
					[503, { error: 'down' }],
					[204, null]
				])
			);
			const flow = await lostDiuna(s);
			const before = s.calls.length;
			expect(await flow.save()).toBeNull();
			expect(s.paths().slice(before)).toEqual(['DELETE /api/requests/first']);
			expect(flow.issue?.message).toContain('poprzedniej próby');
			expect(flow.item?.key).toBe('first');
			expect(await flow.save()).toMatchObject({ saved: { titleID: 20, editionID: 43 } });
		});

		it('saves the pick shown when the save began, even if the user picks again meanwhile', async () => {
			let release: (r: [number, unknown]) => void = () => {};
			const held = new Promise<[number, unknown]>((resolve) => (release = resolve));
			const s = server(routes(() => held));
			const flow = await lostDiuna(s);
			const saving = flow.save();
			flow.choose();
			flow.pick(0);
			release([204, null]);
			await saving;
			const titles = s.calls.filter((c) => c.url === '/api/titles?tmdb_id=2');
			expect(titles).toHaveLength(1);
			const editions = s.calls.filter((c) => c.url === '/api/editions').map((c) => c.body);
			expect(editions.at(-1)).toMatchObject({ title_id: 20, format: 'dvd' });
		});

		it('keeps what the first pick wrote when the new pick is not valid', async () => {
			const s = server(routes([204, null]));
			const flow = await lostDiuna(s);
			flow.form.name = '  ';
			const before = s.calls.length;
			expect(await flow.save()).toBeNull();
			expect(s.calls.length).toBe(before);
			expect(flow.issue?.field).toBe('name');
			expect(flow.item?.key).toBe('first');
		});

		it('keeps what the first pick wrote when the new title is longer than the server takes', async () => {
			const s = server(routes([204, null]));
			const flow = await lostDiuna(s);
			flow.form.name = 'ą'.repeat(251);
			const before = s.calls.length;
			expect(await flow.save()).toBeNull();
			expect(s.calls.length).toBe(before);
			expect(flow.issue).toEqual({ field: 'name', message: 'Tytuł jest za długi.' });
		});

		it('saves a new pick with provider data the server would refuse without it', async () => {
			const long = 'ą'.repeat(251);
			const odd: Candidate = {
				type: 'movie',
				title: { name: 'Rejs', original_title: long, year: 1799, tmdb_id: 3 },
				edition: { barcode: '', format: 'dvd', label: long, publisher: 'Zespół', year: 2201 },
				cover_url: 'ftp://img.example/rejs.jpg',
				provider: 'tmdb',
				confidence: 0.6
			};
			const s = server({
				...routes([204, null]),
				'GET /api/search?kind=movie&q=Diuna': [200, { candidates: [diuna, odd] }],
				'GET /api/titles?tmdb_id=3': [200, []],
				'POST /api/titles': [
					[201, { id: 10, name: 'Diuna: Część druga', kind: 'movie' }],
					[201, { id: 20, name: 'Rejs', kind: 'movie' }]
				]
			});
			const flow = await lostDiuna(s);
			expect(await flow.save()).toMatchObject({ saved: { titleID: 20, editionID: 43 } });
			const bodies = (url: string) => s.calls.filter((c) => c.url === url).map((c) => c.body);
			expect(bodies('/api/titles').at(-1)).toMatchObject({ original_title: null, year: null });
			const edition = bodies('/api/editions').at(-1);
			expect(edition).toMatchObject({ label: '', publisher: 'Zespół', year: null });
			expect(edition).not.toHaveProperty('cover_url');
		});

		it('retries the same pick under the same key without removing anything', async () => {
			const s = server({
				...routes([204, null]),
				'POST /api/editions': [
					[201, { id: 42, title_id: 10, barcode: null, format: 'uhd', binding: null }],
					[200, { id: 42, title_id: 10, barcode: null, format: 'uhd', binding: null }]
				],
				'POST /api/copies?cover=auto': [
					() => Promise.reject(new TypeError('lost')),
					copyReply({ id: 60, edition_id: 42 })
				]
			});
			const flow = new ScanFlow(s.fetchFn, memory());
			flow.start('movie');
			flow.item!.key = 'first';
			flow.query = 'Diuna';
			await flow.search();
			flow.pick(0);
			expect(await flow.save()).toBeNull();
			flow.form.price = '30';
			expect(await flow.save()).toMatchObject({ saved: { titleID: 10, editionID: 42 } });
			expect(s.paths().some((p) => p.startsWith('DELETE'))).toBe(false);
		});
	});

	it('reports the title of the edition a replayed key answered', async () => {
		const s = server({
			...lookup([movie], false),
			'GET /api/titles?tmdb_id=1': [200, [{ id: 20, name: 'Inny', kind: 'movie' }]],
			'POST /api/editions': [201, { id: 42, title_id: 10, barcode: code, format: 'uhd' }],
			'GET /api/titles/10': [200, { id: 10, name: 'Diuna: Część druga', kind: 'movie' }],
			'POST /api/copies?cover=auto': copyReply({ id: 60, edition_id: 42 })
		});
		const flow = new ScanFlow(s.fetchFn, memory());
		await flow.load(code);
		const added = await flow.save();
		expect(added?.saved).toMatchObject({ titleID: 10, titleName: 'Diuna: Część druga' });
	});

	it('keeps the key after a lost answer, so a retry reuses the edition it made', async () => {
		const s = server({
			'GET /api/search?kind=movie&q=Diuna': [
				200,
				{ candidates: [{ ...movie, edition: { barcode: '', format: 'uhd' } }] }
			],
			'GET /api/titles?tmdb_id=1': [200, [{ id: 10, name: 'Diuna: Część druga', kind: 'movie' }]],
			'POST /api/editions': [
				() => Promise.reject(new TypeError('lost')),
				[200, { id: 41, title_id: 10, barcode: null, format: 'uhd', binding: null }]
			],
			'POST /api/copies?cover=auto': copyReply({ id: 70, edition_id: 41 })
		});
		const flow = new ScanFlow(s.fetchFn, memory());
		flow.start('movie');
		flow.query = 'Diuna';
		await flow.search();
		const key = flow.item?.key;
		expect(await flow.save()).toBeNull();
		expect(flow.item?.key).toBe(key);
		expect(s.paths().some((p) => p.startsWith('DELETE'))).toBe(false);
		const added = await flow.save();
		expect(added?.saved).toMatchObject({ titleID: 10, editionID: 41 });
		const keys = s.calls.filter((c) => c.url === '/api/editions').map((c) => c.body);
		expect(keys).toMatchObject([{ request_key: key }, { request_key: key }]);
	});

	it('drops a search answer for the kind the user switched away from', async () => {
		let release: (r: [number, unknown]) => void = () => {};
		const held = new Promise<[number, unknown]>((resolve) => (release = resolve));
		const s = server({ 'GET /api/search?kind=music&q=Kind+of+Blue': () => held });
		const flow = new ScanFlow(s.fetchFn, memory());
		flow.start('music');
		flow.query = 'Kind of Blue';
		const searching = flow.search();
		flow.form.kind = 'book';
		flow.form.name = 'Lalka';
		release([200, { candidates: [album, album] }]);
		await searching;
		expect(flow.searching).toBe(false);
		expect(flow.choosing).toBe(false);
		expect(flow.item?.candidates).toEqual([]);
		expect(flow.form).toMatchObject({ kind: 'book', name: 'Lalka' });
	});

	it('goes back from the search results to the form filled in by hand', async () => {
		const s = server({
			'GET /api/search?kind=music&q=Kind': [200, { candidates: [album, album] }],
			'GET /api/search?kind=music&q=Nic': [200, { candidates: [] }]
		});
		const flow = new ScanFlow(s.fetchFn, memory());
		flow.start('music');
		flow.form.name = 'Kind of Blue (mój)';
		flow.query = 'Kind';
		await flow.search();
		flow.query = 'Nic';
		await flow.search();
		expect(flow.choosing).toBe(true);
		flow.typeIn();
		expect(flow.choosing).toBe(false);
		expect(flow.notice).toBeNull();
		expect(flow.candidate).toBeNull();
		expect(flow.item?.target).toEqual({ kind: 'new', barcode: '', candidate: null });
		expect(flow.form).toMatchObject({ kind: 'music', name: 'Kind of Blue (mój)' });
	});

	it('goes back to the draft the user had before picking a result', async () => {
		const s = server({
			'GET /api/search?kind=music&q=Kind&format=cd': [200, { candidates: [album, album] }]
		});
		const flow = new ScanFlow(s.fetchFn, memory());
		flow.start('music');
		flow.form.name = 'Mój szkic';
		flow.form.format = 'cd';
		flow.form.price = '20';
		flow.query = 'Kind';
		await flow.search();
		flow.pick(0);
		expect(flow.form).toMatchObject({ name: 'Kind of Blue', format: 'vinyl' });
		flow.choose();
		flow.typeIn();
		expect(flow.form).toMatchObject({
			kind: 'music',
			name: 'Mój szkic',
			format: 'cd',
			price: '20'
		});
		expect(flow.item?.searched).toBe(false);
	});

	it('leaves a title search on a resolved barcode for the edited form', async () => {
		const s = server({
			...lookup([movie, book], true),
			'GET /api/search?kind=movie&q=Diuna&barcode=5901234123457&format=uhd': [
				200,
				{ candidates: [movie, movie] }
			]
		});
		const flow = new ScanFlow(s.fetchFn, memory());
		await flow.load(code);
		flow.pick(0);
		flow.form.name = 'Mój szkic';
		flow.choose();
		flow.query = 'Diuna';
		await flow.search();
		expect(flow.item?.searched).toBe(true);
		flow.typeIn();
		expect(flow.choosing).toBe(false);
		expect(flow.form.name).toBe('Mój szkic');
	});

	it('drops a search answer once the user typed another title, even back on the same kind', async () => {
		let release: (r: [number, unknown]) => void = () => {};
		const held = new Promise<[number, unknown]>((resolve) => (release = resolve));
		const s = server({ 'GET /api/search?kind=music&q=Kind': () => held });
		const flow = new ScanFlow(s.fetchFn, memory());
		flow.start('music');
		flow.query = 'Kind';
		const searching = flow.search();
		flow.form.kind = 'book';
		flow.form.kind = 'music';
		flow.form.name = 'Mój szkic';
		release([200, { candidates: [album] }]);
		await searching;
		expect(flow.form.name).toBe('Mój szkic');
		expect(flow.item?.candidates).toEqual([]);
	});

	it('drops a search answer once the form was edited, even back to how it was', async () => {
		let release: (r: [number, unknown]) => void = () => {};
		const held = new Promise<[number, unknown]>((resolve) => (release = resolve));
		const s = server({ 'GET /api/search?kind=music&q=Kind': () => held });
		const flow = new ScanFlow(s.fetchFn, memory());
		flow.start('music');
		flow.form.name = 'Mój szkic';
		flow.query = 'Kind';
		const searching = flow.search();
		flow.edited();
		expect(flow.searching).toBe(false);
		release([200, { candidates: [album] }]);
		await searching;
		expect(flow.form.name).toBe('Mój szkic');
		expect(flow.item?.candidates).toEqual([]);
		flow.edited();
		expect(flow.searching).toBe(false);
	});

	it('keeps the candidates a barcode resolved to', async () => {
		const s = server(lookup([movie, book], true));
		const flow = new ScanFlow(s.fetchFn, memory());
		await flow.load(code);
		flow.typeIn();
		expect(flow.choosing).toBe(true);
		expect(flow.item?.candidates).toHaveLength(2);
	});

	it('drops what a barcode was still answering', async () => {
		let release: (r: [number, unknown]) => void = () => {};
		const slow = new Promise<[number, unknown]>((resolve) => (release = resolve));
		const s = server({ [`GET /api/editions?barcode=${code}`]: () => slow });
		const flow = new ScanFlow(s.fetchFn, memory());
		const loading = flow.load(code);
		flow.start('book');
		release([200, []]);
		await loading.catch(() => undefined);
		expect(flow.phase).toEqual({ kind: 'ready', status: 'missing' });
		expect(flow.item?.barcode).toBe('');
		expect(flow.form.kind).toBe('book');
	});
});

describe('ScanFlow.manual and dismiss', () => {
	it('fills a queued code in by hand and logs it as queued when left', async () => {
		const entry = { id: 1, barcode: code, reason: 'lookup_failed', detail: 'x', scans: 1 };
		const s = server({
			...noEdition,
			[`GET /api/lookup/${code}`]: [500, { error: 'boom' }],
			'POST /api/scan-queue': [201, entry]
		});
		const flow = new ScanFlow(s.fetchFn, memory());
		await flow.load(code);
		flow.manual();
		expect(flow.phase).toEqual({ kind: 'ready', status: 'missing' });
		flow.dismiss();
		expect(recentScans.list[0]).toMatchObject({ outcome: 'queued' });
	});

	it('logs a found code left without adding as skipped', async () => {
		const s = server(lookup([movie], false));
		const flow = new ScanFlow(s.fetchFn, memory());
		await flow.load(code);
		flow.dismiss();
		expect(recentScans.list[0]).toMatchObject({ outcome: 'skipped', label: 'Nie dodano' });
	});

	it('leaves nothing to log before a code resolved', () => {
		const flow = new ScanFlow(server({}).fetchFn, undefined);
		flow.dismiss();
		flow.manual();
		expect(recentScans.list).toHaveLength(0);
	});
});

describe('resultAnnouncement', () => {
	const ready = { kind: 'ready', status: 'found' } as const;

	it('reads the result line, then the name', () => {
		expect(resultAnnouncement(ready, '5901234123457', 'Znaleziono · TMDB', 'Matrix')).toBe(
			'Znaleziono · TMDB. Matrix'
		);
		expect(
			resultAnnouncement(
				{ kind: 'ready', status: 'owned' },
				'5901234123457',
				'Masz już to wydanie',
				'Matrix'
			)
		).toBe('Masz już to wydanie. Matrix');
	});

	it('reads the line alone for a result without a name', () => {
		expect(resultAnnouncement(ready, '5901234123457', 'Znaleziono propozycje: 3')).toBe(
			'Znaleziono propozycje: 3'
		);
	});

	it('stays silent while checking, for results with a live callout and without a code', () => {
		const silent = [
			{ kind: 'loading' },
			{ kind: 'invalid', message: 'x' },
			{ kind: 'failed', message: 'x' },
			{ kind: 'offline' },
			{ kind: 'expired', saved: true },
			{
				kind: 'queued',
				entry: {
					id: 1,
					barcode: '5901234123457',
					reason: 'lookup_failed',
					detail: '',
					scans: 1,
					created_at: '',
					last_scanned_at: ''
				}
			}
		] as const;
		for (const phase of silent) {
			expect(resultAnnouncement(phase, '5901234123457', 'Linia', 'Matrix')).toBe('');
		}
		expect(resultAnnouncement(ready, '', 'Dodaj ręcznie')).toBe('');
	});
});

describe('ScanFlow announcement', () => {
	/** Loads the code and returns what the status holds before and after the words settle. */
	async function heard(routes: Record<string, Reply | Reply[]>, kept = device()) {
		vi.useFakeTimers();
		try {
			const flow = new ScanFlow(server(routes).fetchFn, memory(), kept);
			await flow.load(code);
			const before = flow.announcement;
			vi.advanceTimersByTime(announceSettle);
			return { flow, before, after: flow.announcement };
		} finally {
			vi.useRealTimers();
		}
	}

	it('reads an owned edition with its name once the words settle', async () => {
		const { before, after } = await heard(ownedRoutes());
		expect(before).toBe('');
		expect(after).toBe('Masz już to wydanie. Rejs');
	});

	it('reads a found item with its source and name', async () => {
		const { after } = await heard(lookup([movie], false));
		expect(after).toBe('Znaleziono · TMDB. Diuna: Część druga');
	});

	it('reads the one candidate waiting for a pick by name', async () => {
		const { after } = await heard(lookup([book], true));
		expect(after).toBe('Znaleziono propozycję. Bolesław Prus – Lalka');
	});

	it('names pressings of one item, and counts different items alone', async () => {
		const paperback: Candidate = { ...book, edition: { ...book.edition, binding: 'paperback' } };
		expect((await heard(lookup([book, paperback], true))).after).toBe(
			'Znaleziono propozycje: 2. Bolesław Prus – Lalka'
		);
		expect((await heard(lookup([book, movie], true))).after).toBe('Znaleziono propozycje: 2');
	});

	it('leaves results with their own live callout to it', async () => {
		const queued = await heard({
			...noEdition,
			[`GET /api/lookup/${code}`]: [200, { barcode: code, candidates: [], errors: [] }],
			'POST /api/scan-queue': [
				201,
				{
					id: 1,
					barcode: code,
					reason: 'not_found',
					detail: '',
					scans: 1,
					created_at: '',
					last_scanned_at: ''
				}
			]
		});
		expect(queued.flow.phase.kind).toBe('queued');
		expect(queued.after).toBe('');
		const offline = await heard({ [`GET /api/editions?barcode=${code}`]: noAnswer });
		expect(offline.flow.phase).toEqual({ kind: 'offline' });
		expect(offline.after).toBe('');
	});

	it('is not read again as the form changes, and empties for the next code', async () => {
		vi.useFakeTimers();
		try {
			const flow = new ScanFlow(server(lookup([movie], false)).fetchFn, memory());
			await flow.load(code);
			vi.advanceTimersByTime(announceSettle);
			flow.form.name = 'Inny tytuł';
			flow.wish(true);
			expect(flow.announcement).toBe('Znaleziono · TMDB. Diuna: Część druga');
			const next = flow.load(code);
			expect(flow.announcement).toBe('');
			await next;
		} finally {
			vi.useRealTimers();
		}
	});
});
