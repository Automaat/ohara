import { afterEach, describe, expect, it, vi } from 'vitest';
import type { RecentScan } from './recent.svelte';
import type { KeyValueStore } from './scan';
import type { SessionAdd } from './session.svelte';
import { readVisit, visitLifetime, type StoredVisit } from './visit';

const key = 'ohara.scan.visit';
const now = Date.parse('2026-11-21T12:00:00Z');

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

const diuna: SessionAdd = {
	id: 2,
	copyID: 40,
	editionID: 21,
	titleID: 9,
	createdEdition: true,
	createdTitle: false,
	name: 'Diuna: Część druga',
	medium: '4K UHD',
	kind: 'movie',
	cover: 'https://example.com/diuna.jpg',
	status: 'owned',
	priceCents: 5000,
	recentID: 3
};

const lalka: SessionAdd = {
	...diuna,
	id: 1,
	copyID: 41,
	name: 'Lalka',
	medium: '',
	kind: 'book',
	cover: undefined,
	status: 'wishlist',
	priceCents: null,
	recentID: null
};

function stored(visit: Partial<StoredVisit>): KeyValueStore {
	return memory({
		[key]: JSON.stringify({ at: now - 1000, adds: [], scans: [], date: '', ...visit })
	});
}

describe('readVisit', () => {
	it('reads back a visit within its lifetime', () => {
		const scans = [
			{ id: 3, barcode: '5901234123457', outcome: 'added', label: 'Diuna', href: '/copies/40' },
			{ id: 1, barcode: '5901234123458', outcome: 'waiting', label: 'Zapisano bez połączenia' }
		] as const;
		const v = readVisit(
			stored({ adds: [diuna, lalka], scans: [...scans], date: '2026-11-20' }),
			now
		);
		expect(v).toEqual({
			at: now - 1000,
			adds: [diuna, lalka],
			scans,
			date: '2026-11-20'
		});
		expect('cover' in v.adds[1]).toBe(false);
	});

	it.each([
		['just before its end', visitLifetime - 1, true],
		['at its end', visitLifetime, false],
		['past its end', visitLifetime + 60_000, false],
		['a little ahead of the clock', -60_000, true],
		['a lifetime ahead of the clock', -visitLifetime, false]
	])('keeps a visit last active %s: %s', (_, age, kept) => {
		const v = readVisit(stored({ at: now - age, adds: [diuna] }), now);
		expect(v.adds).toEqual(kept ? [diuna] : []);
	});

	it.each([
		['nothing', memory()],
		['no store', undefined],
		['broken JSON', memory({ [key]: '{' })],
		['an array', memory({ [key]: '[]' })],
		['no time', memory({ [key]: JSON.stringify({ adds: [diuna] }) })],
		['a time that is no number', memory({ [key]: JSON.stringify({ at: 'now', adds: [diuna] }) })],
		[
			'a store that throws',
			{
				getItem: () => {
					throw new DOMException('denied', 'SecurityError');
				},
				setItem: () => undefined
			}
		]
	])('starts empty from %s', (_, store) => {
		expect(readVisit(store, now)).toEqual({ at: 0, adds: [], scans: [], date: '' });
	});

	it('leaves out entries that do not read as an add or a scan', () => {
		const v = readVisit(
			stored({
				adds: [
					diuna,
					{ ...diuna, id: 0 },
					{ ...diuna, kind: 'game' },
					{ ...diuna, priceCents: -1 },
					{ ...diuna, recentID: 'x' },
					{ ...diuna, cover: 7 },
					null
				] as unknown as SessionAdd[],
				scans: [
					{ id: 1, barcode: '1', outcome: 'lost', label: '' },
					{ id: 2, barcode: '2', outcome: 'added', label: '', href: 3 },
					{ id: 3, barcode: '3', outcome: 'queued', label: 'Brak danych' }
				] as unknown as StoredVisit['scans']
			}),
			now
		);
		expect(v.adds).toEqual([diuna]);
		expect(v.scans).toEqual([{ id: 3, barcode: '3', outcome: 'queued', label: 'Brak danych' }]);
	});

	it.each([['21.11.2026'], [7], [null]])('drops a purchase date of %j', (date) => {
		expect(readVisit(stored({ date } as unknown as StoredVisit), now).date).toBe('');
	});

	it('reads lists that are not arrays as empty', () => {
		const v = readVisit(stored({ adds: {}, scans: 'x' } as unknown as StoredVisit), now);
		expect(v).toMatchObject({ adds: [], scans: [] });
	});
});

/** The scanner's modules as a fresh page load starts them, with the browser storage given. */
async function load(storage: unknown) {
	vi.resetModules();
	vi.stubGlobal('localStorage', storage);
	const session = await import('./session.svelte');
	const recent = await import('./recent.svelte');
	const flow = await import('./scan-flow.svelte');
	return { ...session, ...recent, ...flow };
}

const code = '5901234123457';

/** A fetch that answers a scan of an edition already in the collection and its copy's add. */
function shop(): typeof fetch {
	let copy = 40;
	return async (input, init) => {
		const url = `${init?.method ?? 'GET'} ${String(input)}`;
		const replies: Record<string, () => unknown> = {
			[`GET /api/editions?barcode=${code}`]: () => [{ id: 5, barcode: code }],
			'GET /api/editions/5': () => ({
				id: 5,
				title_id: 3,
				barcode: code,
				format: 'dvd',
				binding: null,
				label: '',
				packaging: [],
				title: { id: 3, name: 'Rejs', kind: 'movie', authors: [], year: 1970 },
				copies: []
			}),
			'POST /api/copies?cover=auto': () => ({
				id: ++copy,
				edition_id: 5,
				price_cents: JSON.parse(String(init?.body)).price_cents,
				max_price_cents: null,
				status: 'owned'
			})
		};
		const reply = replies[url];
		if (!reply) throw new TypeError(`unexpected ${url}`);
		return new Response(JSON.stringify(reply()), { status: url.startsWith('POST') ? 201 : 200 });
	};
}

const device = {
	keep: async () => true,
	sessionExpired: () => undefined
};

/** Scans the code and adds a copy at the price, on the date. */
async function addCopy(m: Awaited<ReturnType<typeof load>>, price: string, date: string) {
	const flow = new m.ScanFlow(shop(), memory(), device);
	await flow.load(code);
	flow.form.price = price;
	flow.form.purchased_on = date;
	return flow.save();
}

describe('the visit across reloads', () => {
	afterEach(() => {
		vi.unstubAllGlobals();
		vi.useRealTimers();
	});

	it('keeps the adds, the recent scans and the purchase date', async () => {
		vi.useFakeTimers({ now, toFake: ['Date'] });
		const storage = memory();
		const before = await load(storage);
		await addCopy(before, '25', '2026-11-20');
		await addCopy(before, '30,50', '2026-11-20');
		before.logScan('5901234123458', 'skipped', 'Nie dodano');
		const adds: SessionAdd[] = JSON.parse(JSON.stringify(before.session.adds));
		const scans: RecentScan[] = JSON.parse(JSON.stringify(before.recentScans.list));
		expect(adds).toHaveLength(2);

		vi.setSystemTime(now + visitLifetime - 60_000);
		const after = await load(storage);
		expect(after.session.adds).toEqual(adds);
		expect(after.sessionSpent(after.session.adds)).toBe(5550);
		expect(after.recentScans.list).toEqual(scans);
		const flow = new after.ScanFlow(shop(), memory(), device);
		flow.start('movie');
		expect(flow.form.purchased_on).toBe('2026-11-20');

		const third = await addCopy(after, '10', '2026-11-20');
		expect(third?.add.id).toBe(Math.max(...adds.map((a) => a.id)) + 1);
		expect(third?.add.recentID).toBe(Math.max(...scans.map((s) => s.id)) + 1);
		expect(after.session.adds.map((a) => a.copyID)).toEqual([41, 41, 41]);
	});

	it('undoes an add made after a reload, leaving the earlier ones', async () => {
		vi.useFakeTimers({ now, toFake: ['Date'] });
		const storage = memory();
		const before = await load(storage);
		await addCopy(before, '25', '');
		const after = await load(storage);
		const last = await addCopy(after, '10', '');
		const deleted: string[] = [];
		const undoFetch: typeof fetch = async (input, init) => {
			deleted.push(`${init?.method} ${String(input)}`);
			return new Response(null, { status: 204 });
		};
		if (!last) throw new Error('the add did not go through');
		await after.undoAdd(undoFetch, last.add);
		expect(deleted).toEqual([`DELETE /api/copies/${last.saved.copy.id}`]);
		expect(after.session.adds.map((a) => a.priceCents)).toEqual([2500]);
		expect(after.recentScans.list.map((r) => r.outcome)).toEqual(['undone', 'added']);

		const again = await load(storage);
		expect(again.session.adds.map((a) => a.priceCents)).toEqual([2500]);
		expect(again.recentScans.list.map((r) => r.outcome)).toEqual(['undone', 'added']);
	});

	it('starts empty 12 hours after the last add, an undo since then included', async () => {
		vi.useFakeTimers({ now, toFake: ['Date'] });
		const storage = memory();
		const before = await load(storage);
		await addCopy(before, '25', '2026-11-20');
		vi.setSystemTime(now + visitLifetime / 2);
		await before.undoAdd(async () => new Response(null, { status: 204 }), before.session.adds[0]);

		vi.setSystemTime(now + visitLifetime + 60_000);
		const after = await load(storage);
		expect(after.session.adds).toEqual([]);
		expect(after.recentScans.list).toEqual([]);
		const flow = new after.ScanFlow(shop(), memory(), device);
		flow.start('movie');
		expect(flow.form.purchased_on).toBe('');
	});

	it('works without storage, keeping the visit until the next load', async () => {
		const refusing = {
			getItem: () => {
				throw new DOMException('denied', 'SecurityError');
			},
			setItem: () => {
				throw new DOMException('full', 'QuotaExceededError');
			}
		};
		const before = await load(refusing);
		expect(await addCopy(before, '25', '2026-11-20')).not.toBeNull();
		expect(before.session.adds).toHaveLength(1);
		expect(before.recentScans.list).toHaveLength(1);

		const after = await load(refusing);
		expect(after.session.adds).toEqual([]);
		expect(after.recentScans.list).toEqual([]);
	});
});
