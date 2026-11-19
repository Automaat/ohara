import { beforeEach, describe, expect, it } from 'vitest';
import { ApiError } from './api';
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
type Call = { key: string; body: unknown };

/** A fetch that answers "METHOD /path" from routes, in order when a route has several replies. */
function server(routes: Record<string, Reply | Reply[]>) {
	const calls: string[] = [];
	const sent: Call[] = [];
	const fetchFn: typeof fetch = async (input, init) => {
		const key = `${init?.method ?? 'GET'} ${String(input)}`;
		calls.push(key);
		sent.push({ key, body: init?.body ? JSON.parse(String(init.body)) : undefined });
		const route = routes[key];
		const reply = Array.isArray(route?.[0]) ? (route as Reply[]).shift() : (route as Reply);
		if (!reply) throw new TypeError(`unexpected ${key}`);
		const [status, body] = reply;
		return new Response(body === null ? null : JSON.stringify(body), { status });
	};
	return { fetchFn, calls, sent };
}

function add(over: Partial<Omit<SessionAdd, 'id' | 'recentID'>> = {}): SessionAdd {
	const recentID = logScan('5901234123457', 'added', 'Diuna', '/copies/40');
	return recordAdd({
		copyID: 40,
		editionID: 21,
		titleID: 9,
		createdEdition: false,
		createdTitle: false,
		queued: null,
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
			['DELETE /api/copies/40', 'DELETE /api/editions/21']
		],
		[
			'the copy, edition and title it made',
			{ createdEdition: true, createdTitle: true },
			['DELETE /api/copies/40', 'DELETE /api/editions/21', 'DELETE /api/titles/9']
		]
	])('removes %s', async (_, over, want) => {
		const s = server({
			'DELETE /api/copies/40': gone,
			'DELETE /api/editions/21': gone,
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
			'DELETE /api/editions/21': [409, { error: 'edition still has copies' }]
		});
		await undoAdd(s.fetchFn, add({ createdEdition: true, createdTitle: true }));
		expect(s.calls).toEqual(['DELETE /api/copies/40', 'DELETE /api/editions/21']);
		expect(session.adds).toEqual([]);
	});

	it('keeps a title another edition joined', async () => {
		const s = server({
			'DELETE /api/copies/40': gone,
			'DELETE /api/editions/21': gone,
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
			'DELETE /api/editions/21': [
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
			'DELETE /api/editions/21',
			'DELETE /api/copies/40',
			'DELETE /api/editions/21',
			'DELETE /api/titles/9'
		]);
		expect(session.adds).toEqual([]);
	});

	describe('with a scan queue entry the edition took out', () => {
		const queued = {
			id: 3,
			barcode: '5901234123457',
			reason: 'lookup_failed' as const,
			detail: 'bn: timeout',
			scans: 2,
			created_at: '2026-11-01T10:00:00Z',
			last_scanned_at: '2026-11-02T10:00:00Z'
		};

		it('puts the entry back with its scans and times once the edition is gone', async () => {
			const s = server({
				'DELETE /api/copies/40': gone,
				'DELETE /api/editions/21': gone,
				'DELETE /api/titles/9': gone,
				'POST /api/scan-queue': [201, queued]
			});
			await undoAdd(s.fetchFn, add({ createdEdition: true, createdTitle: true, queued }));
			expect(s.calls.at(-1)).toBe('POST /api/scan-queue');
			expect(s.sent.at(-1)?.body).toEqual({
				barcode: '5901234123457',
				reason: 'lookup_failed',
				detail: 'bn: timeout',
				restore: {
					scans: 2,
					created_at: '2026-11-01T10:00:00Z',
					last_scanned_at: '2026-11-02T10:00:00Z'
				}
			});
			expect(session.adds).toEqual([]);
		});

		it('leaves the queue alone while another copy keeps the edition', async () => {
			const s = server({
				'DELETE /api/copies/40': gone,
				'DELETE /api/editions/21': [409, { error: 'edition still has copies' }]
			});
			await undoAdd(s.fetchFn, add({ createdEdition: true, queued }));
			expect(s.calls).not.toContain('POST /api/scan-queue');
		});

		it('accepts a barcode another edition carries by now', async () => {
			const s = server({
				'DELETE /api/copies/40': gone,
				'DELETE /api/editions/21': gone,
				'POST /api/scan-queue': [409, { error: 'in collection', field: 'barcode' }]
			});
			await undoAdd(s.fetchFn, add({ createdEdition: true, queued }));
			expect(session.adds).toEqual([]);
		});

		it('keeps the add when the queue does not answer', async () => {
			const s = server({
				'DELETE /api/copies/40': gone,
				'DELETE /api/editions/21': gone,
				'POST /api/scan-queue': [500, { error: 'boom' }]
			});
			await expect(
				undoAdd(s.fetchFn, add({ createdEdition: true, queued }))
			).rejects.toBeInstanceOf(ApiError);
			expect(session.adds).toHaveLength(1);
		});
	});
});
