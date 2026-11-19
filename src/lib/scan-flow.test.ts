import { beforeEach, describe, expect, it } from 'vitest';
import { invalidBarcodeMessage } from './api';
import { formatPLN, type EditionDetail } from './collection';
import { recentScans } from './recent.svelte';
import { ScanFlow, resetVisit } from './scan-flow.svelte';
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
			'POST /api/titles': [201, { id: 9, name: 'Diuna: Część druga' }],
			'POST /api/editions': [201, { id: 21, title_id: 9, barcode: code }],
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

	it('starts the next item when a wedge scanner typed a code into the price', async () => {
		const s = server(ownedRoutes());
		const flow = new ScanFlow(s.fetchFn, memory());
		await flow.load(code);
		flow.form.price = other;
		const added = await flow.save();
		expect(added?.next).toBe(other);
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
