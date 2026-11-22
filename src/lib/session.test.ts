import { beforeEach, describe, expect, it } from 'vitest';
import { ApiError } from './api';
import { queueTally } from './queue-tally.svelte';
import { logScan, recentScans } from './recent.svelte';
import {
	addLine,
	recordAdd,
	resetSession,
	session,
	sessionSpent,
	undoAdd,
	type SessionAdd
} from './session.svelte';

type Reply = [number, unknown];

/** A fetch that answers "METHOD /path" from routes, in order when a route has several replies. */
function server(routes: Record<string, Reply | Reply[]>) {
	const calls: string[] = [];
	const fetchFn: typeof fetch = async (input, init) => {
		const key = `${init?.method ?? 'GET'} ${String(input)}`;
		calls.push(key);
		const route = routes[key];
		const reply = Array.isArray(route?.[0]) ? (route as Reply[]).shift() : (route as Reply);
		if (!reply) throw new TypeError(`unexpected ${key}`);
		const [status, body] = reply;
		return new Response(body === null ? null : JSON.stringify(body), { status });
	};
	return { fetchFn, calls };
}

function add(over: Partial<Omit<SessionAdd, 'id' | 'recentID'>> = {}): SessionAdd {
	const recentID = logScan('5901234123457', 'added', 'Diuna', '/copies/40');
	return recordAdd({
		copyID: 40,
		editionID: 21,
		titleID: 9,
		createdEdition: false,
		createdTitle: false,
		name: 'Diuna: Część druga',
		medium: '4K UHD',
		kind: 'movie',
		status: 'owned',
		priceCents: 5000,
		recentID,
		...over
	});
}

const gone: Reply = [204, null];

beforeEach(() => {
	resetSession();
	recentScans.list = [];
});

describe('session adds', () => {
	it('lists the newest add first', () => {
		add({ name: 'A' });
		add({ name: 'B' });
		expect(session.adds.map((a) => a.name)).toEqual(['B', 'A']);
		expect(new Set(session.adds.map((a) => a.id)).size).toBe(2);
	});

	it('counts what owned copies cost, never a wish or a blank price', () => {
		add({ priceCents: 5000 });
		add({ status: 'wishlist', priceCents: null });
		add({ priceCents: null });
		expect(session.adds).toHaveLength(3);
		expect(sessionSpent(session.adds)).toBe(5000);
		expect(sessionSpent([])).toBe(0);
	});

	it('names the title and its medium', () => {
		expect(addLine({ name: 'Diuna: Część druga', medium: '4K UHD' })).toBe(
			'Diuna: Część druga · 4K UHD'
		);
		expect(addLine({ name: 'Solaris', medium: '' })).toBe('Solaris');
	});
});

describe('undoAdd', () => {
	it.each([
		['only the copy of an edition it found', {}, ['DELETE /api/copies/40']],
		[
			'the copy and the edition it made for a title it found',
			{ createdEdition: true },
			['DELETE /api/copies/40', 'DELETE /api/editions/21?requeue=1']
		],
		[
			'the copy, edition and title it made',
			{ createdEdition: true, createdTitle: true },
			['DELETE /api/copies/40', 'DELETE /api/editions/21?requeue=1', 'DELETE /api/titles/9']
		]
	])('removes %s', async (_, over, want) => {
		const s = server({
			'DELETE /api/copies/40': gone,
			'DELETE /api/editions/21?requeue=1': gone,
			'DELETE /api/titles/9': gone
		});
		const a = add(over);
		await undoAdd(s.fetchFn, a);
		expect(s.calls).toEqual(want);
		expect(session.adds).toEqual([]);
		expect(recentScans.list[0]).toMatchObject({ outcome: 'undone', href: undefined });
	});

	it('keeps an edition another copy joined, and its title', async () => {
		const s = server({
			'DELETE /api/copies/40': gone,
			'DELETE /api/editions/21?requeue=1': [409, { error: 'edition still has copies' }]
		});
		await undoAdd(s.fetchFn, add({ createdEdition: true, createdTitle: true }));
		expect(s.calls).toEqual(['DELETE /api/copies/40', 'DELETE /api/editions/21?requeue=1']);
		expect(session.adds).toEqual([]);
	});

	it('keeps a title another edition joined', async () => {
		const s = server({
			'DELETE /api/copies/40': gone,
			'DELETE /api/editions/21?requeue=1': gone,
			'DELETE /api/titles/9': [409, { error: 'title still has editions' }]
		});
		await undoAdd(s.fetchFn, add({ createdEdition: true, createdTitle: true }));
		expect(session.adds).toEqual([]);
	});

	it('treats a copy deleted elsewhere as undone', async () => {
		const s = server({ 'DELETE /api/copies/40': [404, { error: 'not found' }] });
		await undoAdd(s.fetchFn, add());
		expect(session.adds).toEqual([]);
	});

	it('leaves the add in place when the server fails, and finishes on a retry', async () => {
		const s = server({
			'DELETE /api/copies/40': [gone, [404, { error: 'not found' }]],
			'DELETE /api/editions/21?requeue=1': [
				[500, { error: 'boom' }],
				[204, null]
			],
			'DELETE /api/titles/9': gone
		});
		const a = add({ createdEdition: true, createdTitle: true });
		await expect(undoAdd(s.fetchFn, a)).rejects.toBeInstanceOf(ApiError);
		expect(session.adds).toHaveLength(1);
		expect(recentScans.list[0].outcome).toBe('added');
		await undoAdd(s.fetchFn, a);
		expect(s.calls).toEqual([
			'DELETE /api/copies/40',
			'DELETE /api/editions/21?requeue=1',
			'DELETE /api/copies/40',
			'DELETE /api/editions/21?requeue=1',
			'DELETE /api/titles/9'
		]);
		expect(session.adds).toEqual([]);
	});

	it('asks the server to put back the queue entry with the edition it created', async () => {
		const s = server({
			'DELETE /api/copies/40': gone,
			'DELETE /api/editions/21?requeue=1': gone
		});
		await undoAdd(s.fetchFn, add({ createdEdition: true }));
		expect(s.calls).toEqual(['DELETE /api/copies/40', 'DELETE /api/editions/21?requeue=1']);
	});

	it.each([
		['counts the code again once the edition is gone', gone, 1],
		['leaves the count while another copy keeps the edition', [409, { error: 'in use' }], 0]
	] satisfies [string, Reply, number][])('%s', async (_, editionReply, count) => {
		await queueTally.load(
			server({ 'GET /api/scan-queue': [200, [{ id: 1, barcode: '5901234123457' }]] }).fetchFn
		);
		queueTally.resolved(21, '5901234123457');
		expect(queueTally.count).toBe(0);
		const s = server({
			'DELETE /api/copies/40': gone,
			'DELETE /api/editions/21?requeue=1': editionReply
		});
		await undoAdd(s.fetchFn, add({ createdEdition: true }));
		expect(queueTally.count).toBe(count);
	});

	it('undoes an add without a barcode and leaves the recent scans alone', async () => {
		const s = server({ 'DELETE /api/copies/40': gone });
		const scanned = add();
		const typed = recordAdd({ ...scanned, recentID: null });
		await undoAdd(s.fetchFn, typed);
		expect(session.adds).toEqual([scanned]);
		expect(recentScans.list[0]).toMatchObject({ outcome: 'added', href: '/copies/40' });
	});
});
