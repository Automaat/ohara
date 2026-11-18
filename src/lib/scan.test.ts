import { describe, expect, it } from 'vitest';
import { ApiError } from './api';
import type { EditionDetail, Format } from './collection';
import {
	FormRejected,
	RepeatGuard,
	barcodeInputError,
	cameraErrorMessage,
	candidateLabel,
	checkDigitMatches,
	cleanBarcode,
	cropRect,
	emptySticky,
	isBookBarcode,
	isInvalidBarcode,
	loadSticky,
	looksLikeBarcode,
	newRequestKey,
	quickForm,
	resolveScan,
	saveScan,
	saveSticky,
	scanEditionPayload,
	searchTitles,
	scanTitlePayload,
	wedgeBarcodeIn,
	type Candidate,
	type KeyValueStore,
	type QuickForm
} from './scan';

type Reply = [number, unknown] | (() => [number, unknown]);
type Call = { method: string; url: string; body: unknown };

/** A fetch that answers "METHOD /path" from routes and records every call. */
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
		const [status, body] = typeof reply === 'function' ? reply() : (reply as [number, unknown]);
		return new Response(body === null ? null : JSON.stringify(body), { status });
	};
	return { fetchFn, calls, paths: () => calls.map((c) => `${c.method} ${c.url}`) };
}

const movie: Candidate = {
	type: 'movie',
	title: { name: 'Seksmisja', original_title: 'Seksmisja', year: 1984, tmdb_id: 11202 },
	edition: {
		barcode: '5901234123457',
		label: 'Edycja kolekcjonerska',
		publisher: 'Best Film',
		format: 'bluray',
		pl_subtitles: 'yes',
		pl_voiceover: 'no',
		packaging: ['steelbook']
	},
	provider: 'filmweb',
	confidence: 0.9
};

const album: Candidate = {
	type: 'music',
	title: { name: 'The Dark Side Of The Moon', authors: ['Pink Floyd'] },
	edition: {
		barcode: '5099902987620',
		label: 'Reissue, Remastered · Europe',
		publisher: 'EMI',
		year: 2011,
		format: 'cd',
		discogs_release_id: 1873013
	},
	cover_url: 'https://i.discogs.com/dsotm.jpeg',
	provider: 'music',
	confidence: 0.9
};

const book: Candidate = {
	type: 'book',
	title: { name: 'Lalka', authors: ['Bolesław Prus'], bn_id: 'b1', openlibrary_id: 'OL1W' },
	edition: { barcode: '9788324000016', publisher: 'Znak', year: 2019, binding: 'hardcover' },
	provider: 'bn',
	confidence: 0.8
};

const ownedEdition = {
	id: 5,
	title_id: 3,
	barcode: '5901234123457',
	title: { id: 3, name: 'Rejs' },
	copies: []
} as unknown as EditionDetail;

function form(over: Partial<QuickForm> = {}): QuickForm {
	return {
		kind: 'movie',
		name: 'Seksmisja',
		format: 'bluray',
		binding: '',
		price: '',
		shop: '',
		acquired_as: '',
		...over
	};
}

describe('barcode input', () => {
	it.each([
		[' 590-1234 123457\n', '5901234123457'],
		['0-306-40615-x', '030640615X']
	])('cleans %j to %s', (raw, want) => {
		expect(cleanBarcode(raw)).toBe(want);
	});

	it.each([
		['5901234123457', true],
		['96385074', true],
		['036000291452', true],
		['10012345678902', true],
		['030640615X', true],
		['1234567', false],
		['123456789012345', false],
		['59012341234X7', false],
		['', false]
	])('looksLikeBarcode(%j) is %s', (code, want) => {
		expect(looksLikeBarcode(code)).toBe(want);
	});

	it.each([
		['EAN-8', '96385074', true],
		['EAN-8', '96385075', false],
		['UPC-A', '036000291452', true],
		['UPC-A', '036000291453', false],
		['EAN-13', '5901234123457', true],
		['EAN-13', '5907000000014', true],
		['EAN-13', '5907000000017', false],
		['EAN-13 ISBN', '9780306406157', true],
		['EAN-13 ISBN', '9780306406158', false],
		['GTIN-14', '15901234123454', true],
		['GTIN-14', '15901234123455', false],
		['ISBN-10', '0306406152', true],
		['ISBN-10', '0306406153', false],
		['ISBN-10 with X', '080442957X', true],
		['ISBN-10 with X', '030640615X', false],
		['X inside ISBN-10', '03X6406152', false],
		['too short', '1234567', false],
		['empty', '', false]
	])('checkDigitMatches for %s %j is %s', (_, code, want) => {
		expect(checkDigitMatches(code)).toBe(want);
	});

	it('accepts exactly one check digit for every body', () => {
		for (const body of ['9638507', '03600029145', '590700000001', '1590123412345']) {
			const fits = [...'0123456789'].filter((d) => checkDigitMatches(body + d));
			expect(fits, body).toHaveLength(1);
		}
		const isbn = [...'0123456789X'].filter((d) => checkDigitMatches('030640615' + d));
		expect(isbn).toEqual(['2']);
	});

	it.each([
		['5907000000014', undefined],
		['5907000000017', 'Nieprawidłowy kod kreskowy: sprawdź cyfry.'],
		['59070000000', 'Wpisz pełny kod kreskowy lub ISBN i spróbuj ponownie.'],
		['', 'Wpisz pełny kod kreskowy lub ISBN i spróbuj ponownie.']
	])('barcodeInputError(%j) is %j', (code, want) => {
		expect(barcodeInputError(code)).toBe(want);
	});

	it.each([
		['a refused barcode', new ApiError('x', 422, 'barcode'), true],
		['another field', new ApiError('x', 422, 'price'), false],
		['a barcode conflict', new ApiError('x', 409, 'barcode'), false],
		['a server failure', new ApiError('x', 500), false],
		['a network failure', new TypeError('Failed to fetch'), false]
	])('isInvalidBarcode for %s is %s', (_, err, want) => {
		expect(isInvalidBarcode(err)).toBe(want);
	});

	it.each([
		['9788324000016', true],
		['9798324000015', true],
		['030640615X', true],
		['5901234123457', false],
		['96385074', false]
	])('isBookBarcode(%j) is %s', (code, want) => {
		expect(isBookBarcode(code)).toBe(want);
	});

	it.each([
		['5901234123457', '5901234123457'],
		[' 9788324000016 ', '9788324000016'],
		['49,99', null],
		['1234567', null],
		['12345678901', null],
		['', null]
	])('wedgeBarcodeIn(%j) is %j', (price, want) => {
		expect(wedgeBarcodeIn(price)).toBe(want);
	});
});

describe('resolveScan', () => {
	it('returns the edition that already carries the barcode', async () => {
		const s = server({
			'GET /api/editions?barcode=5901234123457': [200, [{ id: 5, barcode: '5901234123457' }]],
			'GET /api/editions/5': [200, ownedEdition]
		});
		const got = await resolveScan(s.fetchFn, ' 590-1234-123457 ');
		expect(got).toEqual({ kind: 'owned', barcode: '5901234123457', edition: ownedEdition });
		expect(s.paths()).not.toContain('GET /api/lookup/5901234123457');
	});

	it('returns lookup candidates under the normalized barcode', async () => {
		const s = server({
			'GET /api/editions?barcode=036000291452': [200, []],
			'GET /api/lookup/036000291452': [
				200,
				{
					barcode: '0036000291452',
					candidates: [movie],
					errors: [{ provider: 'x', error: 'down' }]
				}
			]
		});
		const got = await resolveScan(s.fetchFn, '036000291452');
		expect(got).toEqual({
			kind: 'found',
			barcode: '0036000291452',
			candidates: [movie],
			errors: [{ provider: 'x', error: 'down' }]
		});
	});

	it.each([
		[
			'no provider knows it',
			[200, { barcode: '5901234123457', candidates: [], errors: [] }] as Reply,
			{ barcode: '5901234123457', reason: 'not_found', detail: '' }
		],
		[
			'providers failed',
			[
				200,
				{
					barcode: '5901234123457',
					candidates: [],
					errors: [
						{ provider: 'bn', error: 'timeout' },
						{ provider: 'tmdb', error: '503' }
					]
				}
			] as Reply,
			{ barcode: '5901234123457', reason: 'lookup_failed', detail: 'bn: timeout; tmdb: 503' }
		],
		[
			'the lookup itself failed',
			[500, { error: 'boom' }] as Reply,
			{ barcode: '5901234123457', reason: 'lookup_failed', detail: 'Serwer zwrócił błąd 500.' }
		]
	])('queues the barcode when %s', async (_, lookup, queued) => {
		const entry = { id: 1, ...queued, scans: 1, created_at: '', last_scanned_at: '' };
		const s = server({
			'GET /api/editions?barcode=5901234123457': [200, []],
			'GET /api/lookup/5901234123457': lookup,
			'POST /api/scan-queue': [201, entry]
		});
		const got = await resolveScan(s.fetchFn, '5901234123457');
		expect(got).toEqual({ kind: 'queued', barcode: '5901234123457', entry });
		expect(s.calls.at(-1)?.body).toEqual(queued);
	});

	it('rejects a malformed barcode before any lookup', async () => {
		const s = server({
			'GET /api/editions?barcode=5901234123458': [422, { error: 'checksum', field: 'barcode' }]
		});
		const err = await resolveScan(s.fetchFn, '5901234123458').catch((e: unknown) => e);
		expect(err).toBeInstanceOf(ApiError);
		expect((err as ApiError).field).toBe('barcode');
		expect(s.calls).toHaveLength(1);
	});

	it('fails loudly when not even the queue answers', async () => {
		const s = server({
			'GET /api/editions?barcode=5901234123457': [200, []],
			'GET /api/lookup/5901234123457': [502, null],
			'POST /api/scan-queue': [502, null]
		});
		await expect(resolveScan(s.fetchFn, '5901234123457')).rejects.toBeInstanceOf(ApiError);
	});
});

describe('quickForm', () => {
	const sticky = { shop: 'Empik', acquired_as: 'used' as const };

	it('starts from the candidate and the sticky shop', () => {
		expect(quickForm(movie, sticky, movie.edition.barcode)).toEqual({
			kind: 'movie',
			name: 'Seksmisja',
			format: 'bluray',
			binding: '',
			price: '',
			shop: 'Empik',
			acquired_as: 'used'
		});
		expect(quickForm(book, emptySticky, book.edition.barcode)).toMatchObject({
			kind: 'book',
			binding: 'hardcover',
			format: ''
		});
	});

	it('guesses the kind of a hand-added barcode from its prefix', () => {
		expect(quickForm(null, sticky, '9788324000016')).toMatchObject({ kind: 'book', name: '' });
		expect(quickForm(null, sticky, '5901234123457')).toMatchObject({ kind: 'movie', name: '' });
	});
});

describe('scan payloads', () => {
	it('carries the candidate fields of the chosen kind', () => {
		expect(scanTitlePayload(form({ name: ' Seksmisja ' }), movie)).toEqual({
			ok: true,
			value: {
				kind: 'movie',
				name: 'Seksmisja',
				original_title: 'Seksmisja',
				polish_title: null,
				year: 1984,
				tmdb_id: 11202,
				authors: [],
				bn_id: null,
				openlibrary_id: null,
				notes: ''
			}
		});
		expect(scanEditionPayload(form({ format: 'uhd' }), movie, '5901234123457', 9)).toEqual({
			ok: true,
			value: {
				title_id: 9,
				barcode: '5901234123457',
				label: 'Edycja kolekcjonerska',
				publisher: 'Best Film',
				year: null,
				format: 'uhd',
				pl_subtitles: 'yes',
				pl_voiceover: 'no',
				packaging: ['steelbook'],
				binding: null,
				discogs_release_id: null,
				notes: ''
			}
		});
	});

	it('keeps album fields for an album', () => {
		const f = form({ kind: 'music', name: 'The Dark Side Of The Moon', format: 'cd' });
		expect(scanTitlePayload(f, album)).toMatchObject({
			ok: true,
			value: { kind: 'music', authors: ['Pink Floyd'], tmdb_id: null, bn_id: null }
		});
		expect(scanEditionPayload(f, album, '5099902987620', 7)).toMatchObject({
			ok: true,
			value: {
				format: 'cd',
				discogs_release_id: 1873013,
				cover_url: 'https://i.discogs.com/dsotm.jpeg',
				publisher: 'EMI',
				label: 'Reissue, Remastered · Europe',
				binding: null,
				packaging: []
			}
		});
		const noMedium = scanEditionPayload(form({ kind: 'music', format: 'dvd' }), album, 'x', 7);
		expect(!noMedium.ok && noMedium.issue.field).toBe('format');
	});

	it('labels an album candidate with the details that tell pressings apart', () => {
		expect(candidateLabel(album)).toBe(
			'Pink Floyd – The Dark Side Of The Moon · CD · 2011 · EMI · Reissue, Remastered · Europe · music · 90%'
		);
		expect(candidateLabel(movie)).toBe('Seksmisja · 1984 · filmweb · 90%');
	});

	it('keeps book fields for a book', () => {
		const f = form({ kind: 'book', name: 'Lalka', format: '', binding: 'paperback' });
		expect(scanTitlePayload(f, book)).toMatchObject({
			ok: true,
			value: { authors: ['Bolesław Prus'], bn_id: 'b1', openlibrary_id: 'OL1W', tmdb_id: null }
		});
		expect(scanEditionPayload(f, book, '9788324000016', 1)).toMatchObject({
			ok: true,
			value: { binding: 'paperback', format: null, year: 2019, publisher: 'Znak', packaging: [] }
		});
	});

	it('drops the candidate when the kind was switched', () => {
		const f = form({ kind: 'book', name: 'Seksmisja', format: '', binding: '' });
		expect(scanTitlePayload(f, movie)).toMatchObject({
			ok: true,
			value: { kind: 'book', original_title: null, year: null, tmdb_id: null }
		});
		expect(scanEditionPayload(f, movie, '5901234123457', 1)).toMatchObject({
			ok: true,
			value: { label: '', publisher: '', binding: null, packaging: [], pl_subtitles: 'unknown' }
		});
	});

	it('works without a candidate', () => {
		expect(scanTitlePayload(form({ name: 'Rejs' }), null)).toMatchObject({
			ok: true,
			value: { name: 'Rejs', authors: [], tmdb_id: null }
		});
		expect(scanEditionPayload(form({ format: 'dvd' }), null, '5901234123457', 1)).toMatchObject({
			ok: true,
			value: { format: 'dvd', label: '', pl_voiceover: 'unknown', packaging: [] }
		});
	});

	it('asks for a name and a medium', () => {
		expect(scanTitlePayload(form({ name: '  ' }), movie)).toEqual({
			ok: false,
			issue: { field: 'name', message: 'Podaj tytuł.' }
		});
		expect(scanEditionPayload(form({ format: '' }), movie, '5901234123457', 1)).toEqual({
			ok: false,
			issue: { field: 'format', message: 'Wybierz nośnik.' }
		});
	});
});

describe('saveScan', () => {
	const title = { id: 9, name: 'Seksmisja' };
	const edition = { id: 21, title_id: 9, barcode: '5901234123457' };
	const copy = { id: 40, edition_id: 21 };

	it('creates title, edition and copy for a new barcode', async () => {
		const s = server({
			'GET /api/titles?tmdb_id=11202': [200, []],
			'POST /api/titles': [201, title],
			'POST /api/editions': [201, edition],
			'POST /api/copies?cover=auto': [201, copy]
		});
		const got = await saveScan(
			s.fetchFn,
			{ kind: 'new', barcode: '5901234123457', candidate: movie },
			form({ price: '49,99', shop: ' Empik ', acquired_as: 'new' }),
			'k1'
		);
		expect(got).toEqual({ titleID: 9, titleName: 'Seksmisja', editionID: 21, copy });
		expect(s.calls[2].body).toMatchObject({
			title_id: 9,
			barcode: '5901234123457',
			format: 'bluray'
		});
		expect(s.calls[3].body).toEqual({
			edition_id: 21,
			status: 'owned',
			price_cents: 4999,
			purchased_on: null,
			shop: 'Empik',
			acquired_as: 'new',
			condition: null,
			notes: '',
			max_price_cents: null,
			request_key: 'k1'
		});
	});

	it('reuses the title with the same TMDB ID', async () => {
		const s = server({
			'GET /api/titles?tmdb_id=11202': [200, [{ id: 3, name: 'Seksmisja (1984)' }]],
			'POST /api/editions': [201, { ...edition, title_id: 3 }],
			'POST /api/copies?cover=auto': [201, copy]
		});
		const got = await saveScan(
			s.fetchFn,
			{ kind: 'new', barcode: '5901234123457', candidate: movie },
			form(),
			'k1'
		);
		expect(got.titleID).toBe(3);
		expect(s.paths()).not.toContain('POST /api/titles');
		expect(s.calls[1].body).toMatchObject({ title_id: 3 });
	});

	it('does not look up a TMDB ID that is not there', async () => {
		const s = server({
			'POST /api/titles': [201, { id: 1, name: 'Lalka' }],
			'POST /api/editions': [201, { id: 2, title_id: 1 }],
			'POST /api/copies?cover=auto': [201, copy]
		});
		await saveScan(
			s.fetchFn,
			{ kind: 'new', barcode: '9788324000016', candidate: null },
			form({ kind: 'book', name: 'Lalka', format: '' }),
			'k1'
		);
		expect(s.paths()).toEqual([
			'POST /api/titles',
			'POST /api/editions',
			'POST /api/copies?cover=auto'
		]);
	});

	it('adds a copy to an owned edition', async () => {
		const s = server({ 'POST /api/copies?cover=auto': [201, copy] });
		const got = await saveScan(
			s.fetchFn,
			{ kind: 'owned', edition: ownedEdition },
			form({ price: '10' }),
			'k1'
		);
		expect(got).toEqual({ titleID: 3, titleName: 'Rejs', editionID: 5, copy });
		expect(s.calls[0].body).toMatchObject({ edition_id: 5, price_cents: 1000, request_key: 'k1' });
	});

	it('joins an edition another save created and removes its own title', async () => {
		const s = server({
			'GET /api/titles?tmdb_id=11202': [200, []],
			'POST /api/titles': [201, title],
			'POST /api/editions': [409, { error: 'dup', field: 'barcode' }],
			'GET /api/editions?barcode=5901234123457': [200, [{ ...edition, id: 7, title_id: 4 }]],
			'DELETE /api/titles/9': [204, null],
			'GET /api/titles/4': [200, { id: 4, name: 'Seksmisja' }],
			'POST /api/copies?cover=auto': [201, copy]
		});
		const got = await saveScan(
			s.fetchFn,
			{ kind: 'new', barcode: '5901234123457', candidate: movie },
			form(),
			'k1'
		);
		expect(got).toMatchObject({ titleID: 4, editionID: 7 });
		expect(s.paths()).toContain('DELETE /api/titles/9');
		expect(s.calls.at(-1)?.body).toMatchObject({ edition_id: 7 });
	});

	it('keeps a reused title when the edition races', async () => {
		const s = server({
			'GET /api/titles?tmdb_id=11202': [200, [{ id: 3, name: 'Seksmisja' }]],
			'POST /api/editions': [409, { error: 'dup', field: 'barcode' }],
			'GET /api/editions?barcode=5901234123457': [200, [{ ...edition, id: 7, title_id: 3 }]],
			'GET /api/titles/3': [200, { id: 3, name: 'Seksmisja' }],
			'POST /api/copies?cover=auto': [201, copy]
		});
		await saveScan(
			s.fetchFn,
			{ kind: 'new', barcode: '5901234123457', candidate: movie },
			form(),
			'k1'
		);
		expect(s.paths().some((p) => p.startsWith('DELETE'))).toBe(false);
	});

	it('removes its title and fails when the edition is refused', async () => {
		const s = server({
			'GET /api/titles?tmdb_id=11202': [200, []],
			'POST /api/titles': [201, title],
			'POST /api/editions': [422, { error: 'bad', field: 'year' }],
			'DELETE /api/titles/9': [500, { error: 'boom' }]
		});
		const err = await saveScan(
			s.fetchFn,
			{ kind: 'new', barcode: '5901234123457', candidate: movie },
			form(),
			'k1'
		).catch((e: unknown) => e);
		expect(err).toBeInstanceOf(ApiError);
		expect((err as ApiError).field).toBe('year');
		expect(s.paths()).toContain('DELETE /api/titles/9');
		expect(s.paths()).not.toContain('POST /api/copies?cover=auto');
	});

	it('fails on a barcode conflict whose edition vanished', async () => {
		const s = server({
			'POST /api/titles': [201, title],
			'POST /api/editions': [409, { error: 'dup', field: 'barcode' }],
			'GET /api/editions?barcode=5901234123457': [200, []],
			'DELETE /api/titles/9': [204, null]
		});
		await expect(
			saveScan(s.fetchFn, { kind: 'new', barcode: '5901234123457', candidate: null }, form(), 'k1')
		).rejects.toBeInstanceOf(ApiError);
	});

	it.each([
		['name', form({ name: '' })],
		['format', form({ format: '' })],
		['price_cents', form({ price: 'dużo' })]
	])('refuses a bad %s before any request', async (field, f) => {
		const s = server({});
		const err = await saveScan(
			s.fetchFn,
			{ kind: 'new', barcode: '5901234123457', candidate: movie },
			f,
			'k1'
		).catch((e: unknown) => e);
		expect(err).toBeInstanceOf(FormRejected);
		expect((err as FormRejected).field).toBe(field);
		expect(s.calls).toHaveLength(0);
	});

	it('sends the same request key when a save is retried after a lost response', async () => {
		const lost = (): [number, unknown] => {
			throw new TypeError('network connection was lost');
		};
		const s = server({
			'POST /api/titles': [
				[201, title],
				[201, { id: 10, name: 'Seksmisja' }]
			],
			'POST /api/editions': [
				[201, edition],
				[409, { error: 'dup', field: 'barcode' }]
			],
			'POST /api/copies?cover=auto': [lost, [200, copy]],
			'GET /api/editions?barcode=5901234123457': [200, [edition]],
			'DELETE /api/titles/10': [204, null],
			'GET /api/titles/9': [200, title]
		});
		const target = { kind: 'new', barcode: '5901234123457', candidate: null } as const;
		const first = await saveScan(s.fetchFn, target, form(), 'k7').catch((e: unknown) => e);
		expect(first).toBeInstanceOf(ApiError);
		const got = await saveScan(s.fetchFn, target, form(), 'k7');
		expect(got).toMatchObject({ titleID: 9, editionID: 21, copy });
		const keys = s.calls.filter((c) => c.url === '/api/copies?cover=auto').map((c) => c.body);
		expect(keys).toMatchObject([{ request_key: 'k7' }, { request_key: 'k7' }]);
		expect(s.paths()).toContain('DELETE /api/titles/10');
	});

	it('refuses a bad price for an owned edition before any request', async () => {
		const s = server({});
		await expect(
			saveScan(s.fetchFn, { kind: 'owned', edition: ownedEdition }, form({ price: '-1' }), 'k1')
		).rejects.toBeInstanceOf(FormRejected);
		expect(s.calls).toHaveLength(0);
	});
});

describe('newRequestKey', () => {
	it('makes distinct 32-character hex keys', () => {
		const a = newRequestKey();
		expect(a).toMatch(/^[0-9a-f]{32}$/);
		expect(newRequestKey()).not.toBe(a);
	});
});

describe('RepeatGuard', () => {
	it('lets other barcodes through at once', () => {
		const g = new RepeatGuard(1500);
		expect(g.accepts('5901234123457', 0)).toBe(true);
		g.remember('5901234123457', 1000);
		expect(g.accepts('9788324000016', 1100)).toBe(true);
	});

	it('skips the same barcode while it stays in view', () => {
		const g = new RepeatGuard(1500);
		g.remember('5901234123457', 0);
		expect(g.accepts('5901234123457', 1000)).toBe(false);
		expect(g.accepts('5901234123457', 2400)).toBe(false);
		expect(g.accepts('5901234123457', 3800)).toBe(false);
	});

	it('takes the same barcode again once it was out of view for the window', () => {
		const g = new RepeatGuard(1500);
		g.remember('5901234123457', 0);
		expect(g.accepts('5901234123457', 1000)).toBe(false);
		expect(g.accepts('5901234123457', 2500)).toBe(true);
	});
});

describe('cropRect', () => {
	it('takes the middle half of a frame at full width', () => {
		expect(cropRect(1280, 720)).toEqual({ sx: 0, sy: 180, sw: 1280, sh: 360, dw: 1280, dh: 360 });
	});

	it('scales a wide frame down', () => {
		expect(cropRect(1920, 1080)).toEqual({
			sx: 0,
			sy: 270,
			sw: 1920,
			sh: 540,
			dw: 1280,
			dh: 360
		});
	});
});

describe('cameraErrorMessage', () => {
	it.each([
		['NotAllowedError', 'Brak zgody na użycie aparatu. Zezwól na nią w ustawieniach przeglądarki.'],
		['NotFoundError', 'Nie znaleziono aparatu.'],
		['OverconstrainedError', 'Nie znaleziono aparatu.'],
		['NotReadableError', 'Aparat jest zajęty przez inną aplikację.'],
		['AbortError', 'Nie udało się włączyć aparatu.']
	])('explains %s', (name, want) => {
		expect(cameraErrorMessage(new DOMException('x', name))).toBe(want);
	});

	it('explains anything else', () => {
		expect(cameraErrorMessage('nope')).toBe('Nie udało się włączyć aparatu.');
	});
});

describe('sticky settings', () => {
	function memory(initial: Record<string, string> = {}): KeyValueStore & { data: typeof initial } {
		const data = { ...initial };
		return {
			data,
			getItem: (k) => data[k] ?? null,
			setItem: (k, v) => {
				data[k] = v;
			}
		};
	}

	it('round-trips through storage', () => {
		const store = memory();
		saveSticky(store, { shop: 'Empik', acquired_as: 'used' });
		expect(loadSticky(store)).toEqual({ shop: 'Empik', acquired_as: 'used' });
	});

	it.each([
		['nothing stored', memory()],
		['broken JSON', memory({ 'ohara.scan.sticky': '{' })],
		['not an object', memory({ 'ohara.scan.sticky': '7' })],
		['no storage', undefined]
	])('falls back to blanks with %s', (_, store) => {
		expect(loadSticky(store)).toEqual(emptySticky);
	});

	it('drops unknown values', () => {
		const store = memory({ 'ohara.scan.sticky': '{"shop":3,"acquired_as":"stolen"}' });
		expect(loadSticky(store)).toEqual(emptySticky);
	});

	it('ignores storage that refuses', () => {
		const refusing: KeyValueStore = {
			getItem: () => {
				throw new Error('denied');
			},
			setItem: () => {
				throw new Error('quota');
			}
		};
		expect(() => saveSticky(refusing, emptySticky)).not.toThrow();
		expect(loadSticky(refusing)).toEqual(emptySticky);
	});
});

describe('searchTitles', () => {
	const album: Candidate = {
		type: 'music',
		title: { name: '1-800-OŚWIECENIE', authors: ['Taco Hemingway'] },
		edition: { barcode: '0602458447651', format: 'cd', discogs_release_id: 28352929 },
		provider: 'discogs',
		confidence: 0.6
	};
	it.each([
		{
			name: 'album with barcode and format',
			kind: 'music' as const,
			query: '  Taco Hemingway ',
			barcode: '0602458447651',
			format: 'cd' as Format | '',
			url: '/api/search?kind=music&q=Taco+Hemingway&barcode=0602458447651&format=cd'
		},
		{
			name: 'film without barcode or format',
			kind: 'movie' as const,
			query: 'Łowca androidów',
			barcode: '',
			format: '' as Format | '',
			url: '/api/search?kind=movie&q=%C5%81owca+android%C3%B3w'
		}
	])('$name', async ({ kind, query, barcode, format, url }) => {
		const s = server({ [`GET ${url}`]: [200, { candidates: [album] }] });
		await expect(searchTitles(s.fetchFn, kind, query, barcode, format)).resolves.toEqual([album]);
		expect(s.paths()).toEqual([`GET ${url}`]);
	});

	it.each([
		{ status: 502, want: 'Źródło nie odpowiada, spróbuj za chwilę.' },
		{ status: 503, want: 'Wyszukiwanie dla tego typu jest wyłączone: brak klucza API.' },
		{ status: 500, want: 'Serwer zwrócił błąd 500.' }
	])('explains a $status', async ({ status, want }) => {
		const s = server({ 'GET /api/search?kind=book&q=x': [status, { error: 'x' }] });
		await expect(searchTitles(s.fetchFn, 'book', 'x', '', '')).rejects.toThrow(want);
	});
});

describe('candidateLabel for search results', () => {
	it('puts the author before a book and the year after it', () => {
		const book: Candidate = {
			type: 'book',
			title: { name: 'Solaris', authors: ['Stanisław Lem'], year: 1961 },
			edition: { barcode: '' },
			provider: 'openlibrary',
			confidence: 0.6
		};
		expect(candidateLabel(book)).toBe('Stanisław Lem – Solaris · 1961 · openlibrary · 60%');
	});
});
