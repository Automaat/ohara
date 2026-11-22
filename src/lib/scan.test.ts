import { describe, expect, it, vi } from 'vitest';
import { ApiError } from './api';
import { deleteQueueEntry } from './queue-deletes';
import type { Binding, EditionDetail, Format, Packaging } from './collection';
import {
	FormRejected,
	LatestLoad,
	barcodeGroups,
	RepeatGuard,
	barcodeInputError,
	cameraErrorMessage,
	candidateLabel,
	canonicalBarcode,
	checkDigitMatches,
	cleanBarcode,
	bandCrop,
	emptySticky,
	isBookBarcode,
	isoDate,
	isInvalidBarcode,
	loadSticky,
	looksLikeBarcode,
	newMade,
	newRequestKey,
	providerLabel,
	queuedMessage,
	quickForm,
	rememberShop,
	resolveScan,
	saveScan,
	saveSticky,
	scanCopyPayload,
	scanEditionPayload,
	searchTitles,
	scanTitlePayload,
	stickyAfter,
	torchSupported,
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
	sources: [],
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
	sources: [],
	confidence: 0.9
};

const book: Candidate = {
	type: 'book',
	title: { name: 'Lalka', authors: ['Bolesław Prus'], bn_id: 'b1', openlibrary_id: 'OL1W' },
	edition: { barcode: '9788324000016', publisher: 'Znak', year: 2019, binding: 'hardcover' },
	provider: 'bn',
	sources: [],
	confidence: 0.8
};

const ownedEdition = {
	id: 5,
	title_id: 3,
	barcode: '5901234123457',
	format: 'dvd',
	title: { id: 3, name: 'Rejs', kind: 'movie' },
	copies: []
} as unknown as EditionDetail;

function form(over: Partial<QuickForm> = {}): QuickForm {
	return {
		kind: 'movie',
		name: 'Seksmisja',
		format: 'bluray',
		binding: '',
		packaging: [],
		status: 'owned',
		price: '',
		purchased_on: '',
		shop: '',
		acquired_as: '',
		condition: '',
		exact_edition: false,
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
		['080442957X', '080442957X'],
		['0-8044-2957-x', '080442957X'],
		['0306406152', '0306406152'],
		['0804429570', null],
		['030640615X', null],
		['03X6406152', null],
		['49,99', null],
		['1234567', null],
		['12345678901', null],
		['', null]
	])('wedgeBarcodeIn(%j) is %j', (price, want) => {
		expect(wedgeBarcodeIn(price)).toBe(want);
	});
});

describe('barcodeGroups', () => {
	it.each([
		['5901234123457', ['5', '901234', '123457']],
		['0602445790098', ['0', '602445', '790098']],
		['9788375780635', ['978', '8375', '780635']],
		['9790260000438', ['979', '0260', '000438']],
		['96385074', ['9638', '5074']],
		['036000291452', ['0', '36000', '29145', '2']],
		['10012345678902', ['1', '00', '12345', '67890', '2']]
	])('groups %s like the digits under its bars', (code, groups) => {
		expect(barcodeGroups(code)).toEqual(groups);
		expect(barcodeGroups(code).join('')).toBe(code);
	});

	it.each(['123', '123456789X', ''])('keeps %j whole when it has no known shape', (code) => {
		expect(barcodeGroups(code)).toEqual([code]);
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
					needs_confirmation: false,
					errors: [{ provider: 'x', error: 'down' }]
				}
			]
		});
		const got = await resolveScan(s.fetchFn, '036000291452');
		expect(got).toEqual({
			kind: 'found',
			barcode: '0036000291452',
			candidates: [movie],
			needsConfirmation: false,
			errors: [{ provider: 'x', error: 'down' }]
		});
	});

	it.each([
		['asks for a pick', true, true],
		['trusts its lone candidate', false, false],
		['does not say', undefined, true]
	])('needs a pick when the lookup %s', async (_, needs, want) => {
		const s = server({
			'GET /api/editions?barcode=5901234123457': [200, []],
			'GET /api/lookup/5901234123457': [
				200,
				{
					barcode: '5901234123457',
					candidates: [movie, album],
					errors: [],
					...(needs === undefined ? {} : { needs_confirmation: needs })
				}
			]
		});
		const got = await resolveScan(s.fetchFn, '5901234123457');
		expect(got.kind === 'found' && got.needsConfirmation).toBe(want);
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

	it('fails without queuing when the lookup gets no answer in 20 seconds', async () => {
		vi.useFakeTimers();
		try {
			const s = server({ 'GET /api/editions?barcode=5901234123457': [200, []] });
			const stalled: typeof fetch = (input, init) =>
				String(input).startsWith('/api/lookup/')
					? new Promise<Response>(() => undefined)
					: s.fetchFn(input, init);
			const outcome: { error?: unknown } = {};
			const settled = resolveScan(stalled, '5901234123457').then(
				() => undefined,
				(err: unknown) => {
					outcome.error = err;
				}
			);

			await vi.advanceTimersByTimeAsync(19_999);
			expect(outcome.error).toBeUndefined();
			await vi.advanceTimersByTimeAsync(1);
			await settled;
			expect(outcome.error).toBeInstanceOf(ApiError);
			expect(outcome.error).toMatchObject({ status: 0, message: 'Serwer nie odpowiada.' });
			expect(s.paths()).toEqual(['GET /api/editions?barcode=5901234123457']);
		} finally {
			vi.useRealTimers();
		}
	});

	it.each([
		['a scan', false, 'GET /api/lookup/5901234123457'],
		['a recheck', true, 'GET /api/lookup/5901234123457?recheck=1']
	])('asks the lookup for %s', async (_, recheck, path) => {
		const s = server({
			'GET /api/editions?barcode=5901234123457': [200, []],
			[path]: [200, { barcode: '5901234123457', candidates: [movie], errors: [] }]
		});
		const got = await resolveScan(s.fetchFn, '5901234123457', recheck);
		expect(got.kind).toBe('found');
		expect(s.paths()).toEqual(['GET /api/editions?barcode=5901234123457', path]);
	});

	it('queues a rescanned barcode only after a queue removal on its way has settled', async () => {
		let answer: () => void = () => {};
		vi.stubGlobal(
			'fetch',
			() =>
				new Promise<Response>(
					(resolve) => (answer = () => resolve(new Response(null, { status: 204 })))
				)
		);
		const removal = deleteQueueEntry(9, undefined);
		vi.unstubAllGlobals();
		const entry = {
			id: 1,
			barcode: '5901234123457',
			reason: 'not_found',
			detail: '',
			scans: 1,
			created_at: '',
			last_scanned_at: ''
		};
		const s = server({
			'GET /api/editions?barcode=5901234123457': [200, []],
			'GET /api/lookup/5901234123457': [
				200,
				{ barcode: '5901234123457', candidates: [], errors: [] }
			],
			'POST /api/scan-queue': [201, entry]
		});
		const scan = resolveScan(s.fetchFn, '5901234123457');
		await new Promise((resolve) => setTimeout(resolve, 0));
		expect(s.paths()).not.toContain('POST /api/scan-queue');
		answer();
		await removal;
		expect(await scan).toEqual({ kind: 'queued', barcode: '5901234123457', entry });
		expect(s.paths().at(-1)).toBe('POST /api/scan-queue');
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
	const sticky = { shop: 'Empik', acquired_as: 'used' as const, shops: ['Empik'] };

	it('starts from the candidate, the sticky shop and the visit date', () => {
		expect(quickForm(movie, sticky, movie.edition.barcode, '2026-11-19')).toEqual({
			kind: 'movie',
			name: 'Seksmisja',
			format: 'bluray',
			binding: '',
			packaging: ['steelbook'],
			status: 'owned',
			price: '',
			purchased_on: '2026-11-19',
			shop: 'Empik',
			acquired_as: 'used',
			condition: '',
			exact_edition: false
		});
		expect(quickForm(movie, sticky, movie.edition.barcode).purchased_on).toBe('');
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
		expect(
			scanEditionPayload(
				form({ format: 'uhd', packaging: ['steelbook'] }),
				movie,
				'5901234123457',
				9
			)
		).toEqual({
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

	it('takes the packaging the user toggled for a film only', () => {
		const picked = form({ packaging: ['slipcover', 'digipak'] });
		expect(scanEditionPayload(picked, movie, '5901234123457', 1)).toMatchObject({
			ok: true,
			value: { packaging: ['slipcover', 'digipak'] }
		});
		const asAlbum = form({ kind: 'music', format: 'cd', packaging: ['digipak'] });
		expect(scanEditionPayload(asAlbum, album, '5099902987620', 1)).toMatchObject({
			ok: true,
			value: { packaging: [] }
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

	it('drops the Discogs release of a pressing on another or an unknown medium', () => {
		const vinyl = form({ kind: 'music', name: 'The Dark Side Of The Moon', format: 'vinyl' });
		expect(scanEditionPayload(vinyl, album, '', 7)).toMatchObject({
			ok: true,
			value: { format: 'vinyl', discogs_release_id: null }
		});
		const unknown = { ...album, edition: { ...album.edition, format: null } };
		expect(scanEditionPayload(vinyl, unknown, '', 7)).toMatchObject({
			ok: true,
			value: { discogs_release_id: null }
		});
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

	it('carries a series the candidate names, dropping values the API would refuse', () => {
		const f = form({ kind: 'book', name: 'Lalka', format: '', binding: '' });
		const inSeries = {
			...book,
			title: { ...book.title, series_name: ' Pozytywizm ', series_number: 2 }
		};
		expect(scanTitlePayload(f, inSeries)).toMatchObject({
			ok: true,
			value: { series_name: 'Pozytywizm', series_number: 2 }
		});
		const badNumber = {
			...book,
			title: { ...book.title, series_name: 'Saga', series_number: 2.5 }
		};
		expect(scanTitlePayload(f, badNumber)).toMatchObject({
			ok: true,
			value: { series_name: 'Saga', series_number: null }
		});
		for (const series_name of ['Sa\nga', ' ', null]) {
			const dropped = scanTitlePayload(f, {
				...book,
				title: { ...book.title, series_name, series_number: 4 }
			});
			expect(dropped.ok && 'series_name' in dropped.value).toBe(false);
			expect(dropped.ok && 'series_number' in dropped.value).toBe(false);
		}
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

	it('leaves out provider values the API would refuse', () => {
		const long = 'ą'.repeat(251);
		const broken: Candidate = {
			type: 'music',
			title: {
				name: 'Album',
				original_title: long,
				polish_title: 'Ok\0',
				year: 1799,
				authors: ['Artysta', long]
			},
			edition: {
				barcode: '',
				label: long,
				publisher: ` ${'x'.repeat(500)} `,
				year: 2201,
				format: 'cd',
				discogs_release_id: 0
			},
			cover_url: 'https://user:pass@img.example/a.jpg',
			provider: 'music',
			sources: [],
			confidence: 0.9
		};
		const f = form({ kind: 'music', name: 'Album\0\u0085', format: 'cd' });
		expect(scanTitlePayload(f, broken)).toMatchObject({
			ok: true,
			value: { name: 'Album', original_title: null, polish_title: null, year: null, authors: [] }
		});
		expect(scanEditionPayload(f, broken, '', 1)).toMatchObject({
			ok: true,
			value: {
				label: '',
				publisher: 'x'.repeat(500),
				year: null,
				discogs_release_id: null,
				cover_url: undefined
			}
		});
		const odd = {
			...movie,
			edition: { ...movie.edition, pl_subtitles: 'maybe', pl_voiceover: ' yes ' }
		} as unknown as Candidate;
		const oddForm = form({ packaging: ['steelbook', 'box'] as unknown as Packaging[] });
		expect(scanEditionPayload(oddForm, odd, '', 1)).toMatchObject({
			value: { pl_subtitles: 'unknown', pl_voiceover: 'yes', packaging: ['steelbook'] }
		});
		const oddBook = form({ kind: 'book', name: 'Lalka', format: '', binding: 'spiral' as Binding });
		expect(scanEditionPayload(oddBook, book, '', 1)).toMatchObject({ value: { binding: null } });
		expect(
			scanEditionPayload({ ...oddBook, binding: 'constructor' as Binding }, book, '', 1)
		).toMatchObject({
			value: { binding: null }
		});
		// Go trims U+0085 as space, so a name of it alone is empty there.
		expect(scanTitlePayload(form({ name: '\u0085 ' }), movie)).toMatchObject({ ok: false });
		const tooMany = { ...book, title: { ...book.title, authors: Array(21).fill('A') } };
		const bookForm = form({ kind: 'book', name: 'Lalka', format: '' });
		expect(scanTitlePayload(bookForm, tooMany)).toMatchObject({ value: { authors: [] } });
		for (const cover_url of [
			'ftp://img.example/a.jpg',
			'https://img.example/a b.jpg',
			`https://img.example/${'a'.repeat(10000)}`,
			'nie adres',
			// A browser's URL parser takes these; Go's refuses them.
			'https://img.example/a%zz.jpg',
			'https://img.example/a.jpg#%',
			'https://%41.example/a.jpg',
			'https://@img.example/a.jpg',
			'https:/img.example/a.jpg',
			'https:///img.example/a.jpg',
			'https://img.example\\a.jpg'
		]) {
			expect(scanEditionPayload(f, { ...album, cover_url }, '', 1)).toMatchObject({
				value: { cover_url: undefined }
			});
		}
		for (const cover_url of [
			'https://i.discogs.com/dsotm.jpeg',
			'HTTP://img.example:8080/okładka%20(1).jpg?w=600&h=600#top'
		]) {
			expect(scanEditionPayload(f, { ...album, cover_url }, '', 1)).toMatchObject({
				value: { cover_url }
			});
		}
	});
});

describe('saveScan', () => {
	const title = { id: 9, name: 'Seksmisja', kind: 'movie' };
	const edition = { id: 21, title_id: 9, barcode: '5901234123457', format: 'uhd', binding: null };
	const copy = { id: 40, edition_id: 21 };
	const lost = (): [number, unknown] => {
		throw new TypeError('network connection was lost');
	};

	/** The body of the first request to "METHOD /path". */
	function bodyOf(s: ReturnType<typeof server>, path: string): unknown {
		return s.calls.find((c) => `${c.method} ${c.url}` === path)?.body;
	}

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
			form({
				format: 'uhd',
				packaging: ['steelbook', 'slipcover'],
				price: '49,99',
				purchased_on: '2026-11-18',
				shop: ' Empik ',
				acquired_as: 'used',
				condition: 'very_good'
			}),
			'k1'
		);
		expect(got).toEqual({
			titleID: 9,
			titleName: 'Seksmisja',
			kind: 'movie',
			editionID: 21,
			edition,
			cover: undefined,
			copy,
			createdTitle: true,
			createdEdition: true
		});
		expect(bodyOf(s, 'POST /api/titles')).toMatchObject({ tmdb_id: 11202, request_key: 'k1' });
		expect(bodyOf(s, 'POST /api/editions')).toMatchObject({
			title_id: 9,
			barcode: '5901234123457',
			format: 'uhd',
			packaging: ['steelbook', 'slipcover'],
			request_key: 'k1'
		});
		expect(bodyOf(s, 'POST /api/copies?cover=auto')).toEqual({
			edition_id: 21,
			status: 'owned',
			price_cents: 4999,
			purchased_on: '2026-11-18',
			shop: 'Empik',
			acquired_as: 'used',
			condition: 'very_good',
			notes: '',
			max_price_cents: null,
			request_key: 'k1'
		});
	});

	it('adds a wish with its price limit and nothing about a purchase', async () => {
		const s = server({ 'POST /api/copies?cover=auto': [201, copy] });
		await saveScan(
			s.fetchFn,
			{ kind: 'owned', edition: ownedEdition },
			form({
				status: 'wishlist',
				price: '60',
				purchased_on: '2026-11-18',
				shop: 'Empik',
				acquired_as: 'used',
				condition: 'good'
			}),
			'k2'
		);
		expect(s.calls[0].body).toEqual({
			edition_id: 5,
			status: 'wishlist',
			price_cents: null,
			purchased_on: null,
			shop: '',
			acquired_as: null,
			condition: null,
			notes: '',
			max_price_cents: 6000,
			request_key: 'k2'
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
		expect(got).toMatchObject({ titleID: 3, createdTitle: false, createdEdition: true });
		expect(s.paths()).not.toContain('POST /api/titles');
		expect(bodyOf(s, 'POST /api/editions')).toMatchObject({ title_id: 3 });
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
		expect(got).toMatchObject({
			titleID: 3,
			titleName: 'Rejs',
			kind: 'movie',
			editionID: 5,
			edition: { id: 5, format: 'dvd' },
			copy,
			createdTitle: false,
			createdEdition: false
		});
		expect(s.paths()).toEqual(['POST /api/copies?cover=auto']);
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
		expect(got).toMatchObject({
			titleID: 4,
			editionID: 7,
			createdTitle: false,
			createdEdition: false
		});
		expect(s.paths()).toContain('DELETE /api/titles/9');
		expect(s.calls.at(-1)?.body).toMatchObject({ edition_id: 7 });
	});

	it('counts the edition an attempt whose answer was lost created', async () => {
		const made = newMade();
		const s = server({
			'GET /api/titles?tmdb_id=11202': [
				[200, []],
				[200, [title]]
			],
			'POST /api/titles': [201, title],
			'POST /api/editions': [lost, [200, edition]],
			'POST /api/copies?cover=auto': [201, copy]
		});
		const target = { kind: 'new', barcode: '5901234123457', candidate: movie } as const;
		await expect(saveScan(s.fetchFn, target, form(), 'k1', made)).rejects.toBeInstanceOf(ApiError);
		const got = await saveScan(s.fetchFn, target, form(), 'k1', made);
		expect(got).toMatchObject({
			titleID: 9,
			editionID: 21,
			createdTitle: true,
			createdEdition: true
		});
		expect(made).toMatchObject({ titles: [9], editions: [21], titleUnanswered: false });
		expect(s.calls.filter((c) => c.url === '/api/editions').map((c) => c.body)).toMatchObject([
			{ request_key: 'k1' },
			{ request_key: 'k1' }
		]);
	});

	it('asks the key, not the TMDB ID, after a title request whose answer was lost', async () => {
		const made = newMade();
		const s = server({
			'GET /api/titles?tmdb_id=11202': [200, []],
			'POST /api/titles': [lost, [200, title]],
			'POST /api/editions': [201, edition],
			'POST /api/copies?cover=auto': [201, copy]
		});
		const target = { kind: 'new', barcode: '5901234123457', candidate: movie } as const;
		await saveScan(s.fetchFn, target, form(), 'k1', made).catch(() => null);
		expect(made.titleUnanswered).toBe(true);
		const got = await saveScan(s.fetchFn, target, form(), 'k1', made);
		expect(got).toMatchObject({ titleID: 9, createdTitle: true, createdEdition: true });
		expect(s.paths().filter((p) => p.startsWith('GET /api/titles?'))).toHaveLength(1);
		expect(s.calls.filter((c) => c.url === '/api/titles').map((c) => c.body)).toMatchObject([
			{ request_key: 'k1' },
			{ request_key: 'k1' }
		]);
	});

	it('reuses a title another add made with the same TMDB ID after a lost answer', async () => {
		const made = newMade();
		const s = server({
			'GET /api/titles?tmdb_id=11202': [
				[200, []],
				[200, [{ ...title, id: 3 }]]
			],
			'POST /api/titles': [lost, [409, { error: 'dup', field: 'tmdb_id' }]],
			'POST /api/editions': [201, { ...edition, title_id: 3 }],
			'POST /api/copies?cover=auto': [201, copy]
		});
		const target = { kind: 'new', barcode: '5901234123457', candidate: movie } as const;
		await saveScan(s.fetchFn, target, form(), 'k1', made).catch(() => null);
		const got = await saveScan(s.fetchFn, target, form(), 'k1', made);
		expect(got).toMatchObject({ titleID: 3, createdTitle: false, createdEdition: true });
		expect(made).toMatchObject({ titles: [], editions: [21], titleUnanswered: false });
	});

	it('still counts a title it failed to remove', async () => {
		const made = newMade();
		const s = server({
			'GET /api/titles?tmdb_id=11202': [200, []],
			'POST /api/titles': [201, title],
			'POST /api/editions': [422, { error: 'bad', field: 'year' }],
			'DELETE /api/titles/9': [500, { error: 'boom' }]
		});
		await saveScan(
			s.fetchFn,
			{ kind: 'new', barcode: '5901234123457', candidate: movie },
			form(),
			'k1',
			made
		).catch(() => null);
		expect(made.titles).toEqual([9]);
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

	it('refuses a bad price limit or purchase date before any request', async () => {
		const s = server({});
		const target = { kind: 'owned', edition: ownedEdition } as const;
		const limit = await saveScan(
			s.fetchFn,
			target,
			form({ status: 'wishlist', price: 'dużo' }),
			'k1'
		).catch((e: unknown) => e);
		expect((limit as FormRejected).field).toBe('max_price_cents');
		const date = await saveScan(
			s.fetchFn,
			target,
			form({ purchased_on: '18.11.2026' }),
			'k1'
		).catch((e: unknown) => e);
		expect((date as FormRejected).field).toBe('purchased_on');
		expect(s.calls).toHaveLength(0);
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

	it('keeps every remembered barcode guarded', () => {
		const g = new RepeatGuard(3000);
		g.remember('5901234123457', 0);
		g.remember('9788324000012', 0);
		expect(g.accepts('5901234123457', 2000)).toBe(false);
		expect(g.accepts('9788324000012', 2500)).toBe(false);
		expect(g.accepts('5901234123457', 4900)).toBe(false);
		expect(g.accepts('9788324000012', 5600)).toBe(true);
	});

	it('takes the same barcode again once it was out of view for the window', () => {
		const g = new RepeatGuard(1500);
		g.remember('5901234123457', 0);
		expect(g.accepts('5901234123457', 1000)).toBe(false);
		expect(g.accepts('5901234123457', 2500)).toBe(true);
	});
});

describe('LatestLoad', () => {
	it('lets only the newest load apply its outcome', () => {
		const loads = new LatestLoad();
		const first = loads.start();
		expect(first()).toBe(true);
		const second = loads.start();
		expect(first()).toBe(false);
		expect(second()).toBe(true);
		loads.start();
		expect(second()).toBe(false);
	});

	it('keeps separate pages apart', () => {
		const one = new LatestLoad().start();
		new LatestLoad().start();
		expect(one()).toBe(true);
	});
});

describe('bandCrop', () => {
	const phone = { left: 0, top: 0, width: 390, height: 844 };

	it('takes the frame under the band when the video spills past the sides', () => {
		// A portrait 1080 x 1920 frame covers 390 x 844 at 844 / 1920: 474.75 px wide, so
		// 42.375 px are cut off at each side.
		const band = { left: 36, top: 264, width: 318, height: 128 };
		expect(bandCrop(1080, 1920, phone, band)).toEqual({
			sx: 178,
			sy: 601,
			sw: 724,
			sh: 291,
			dw: 724,
			dh: 291
		});
	});

	it('takes the frame under the band when the video spills past the top and bottom', () => {
		// A 1920 x 1080 frame covers an 800 x 300 box at 800 / 1920: 450 px tall, 75 px cut off
		// at the top and at the bottom.
		const view = { left: 100, top: 50, width: 800, height: 300 };
		const band = { left: 300, top: 136, width: 400, height: 128 };
		expect(bandCrop(1920, 1080, view, band)).toEqual({
			sx: 480,
			sy: 386,
			sw: 960,
			sh: 308,
			dw: 960,
			dh: 308
		});
	});

	it('scales a wide crop down', () => {
		const band = { left: 0, top: 0, width: 390, height: 844 };
		expect(bandCrop(3840, 2160, { left: 0, top: 0, width: 1600, height: 900 }, band, 640)).toEqual({
			sx: 0,
			sy: 0,
			sw: 936,
			sh: 2026,
			dw: 640,
			dh: 1385
		});
	});

	it('keeps a band that reaches past the frame inside it', () => {
		const view = { left: 0, top: 0, width: 640, height: 480 };
		const band = { left: -50, top: 400, width: 800, height: 200 };
		expect(bandCrop(640, 480, view, band)).toEqual({
			sx: 0,
			sy: 400,
			sw: 640,
			sh: 80,
			dw: 640,
			dh: 80
		});
	});

	it('gives nothing for a band off the frame or a video without a size', () => {
		const band = { left: 36, top: 264, width: 318, height: 128 };
		expect(bandCrop(1080, 1920, phone, { ...band, top: 900 })).toBeNull();
		expect(bandCrop(0, 0, phone, band)).toBeNull();
		expect(bandCrop(1080, 1920, { ...phone, width: 0 }, band)).toBeNull();
	});
});

describe('torchSupported', () => {
	it.each([
		[undefined, false],
		[{}, false],
		[{ torch: false }, false],
		[{ torch: true }, true],
		[{ torch: [false] }, false],
		[{ torch: [false, true] }, true],
		[{ torch: 'true' }, false]
	])('reads %j as %s', (capabilities, want) => {
		expect(torchSupported(capabilities)).toBe(want);
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
		const sticky = { shop: 'Empik', acquired_as: 'used' as const, shops: ['Empik', 'OLX'] };
		saveSticky(store, sticky);
		expect(loadSticky(store)).toEqual(sticky);
	});

	it('offers the last shop when no recent shops were stored', () => {
		const store = memory({ 'ohara.scan.sticky': '{"shop":"Empik","acquired_as":"new"}' });
		expect(loadSticky(store)).toEqual({ shop: 'Empik', acquired_as: 'new', shops: ['Empik'] });
	});

	it('cleans the stored recent shops', () => {
		const shops = [' Empik ', 3, '', 'empik', 'OLX', 'A', 'B', 'C', 'D', 'E'];
		const store = memory({ 'ohara.scan.sticky': JSON.stringify({ shop: '', shops }) });
		expect(loadSticky(store).shops).toEqual(['Empik', 'OLX', 'A', 'B', 'C', 'D']);
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
		const store = memory({
			'ohara.scan.sticky': '{"shop":3,"acquired_as":"stolen","shops":"Empik"}'
		});
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

describe('recent shops', () => {
	it('moves a used shop to the front once, newest first', () => {
		expect(rememberShop(['OLX', 'Empik'], ' empik ')).toEqual(['empik', 'OLX']);
		expect(rememberShop(['OLX'], '  ')).toEqual(['OLX']);
		expect(rememberShop(['A', 'B', 'C', 'D', 'E', 'F'], 'G')).toEqual([
			'G',
			'A',
			'B',
			'C',
			'D',
			'E'
		]);
	});

	it('carries the shop and new or used of an owned copy only', () => {
		const before = { shop: 'OLX', acquired_as: 'used' as const, shops: ['OLX'] };
		expect(stickyAfter(before, form({ shop: ' Empik ', acquired_as: 'new' }))).toEqual({
			shop: 'Empik',
			acquired_as: 'new',
			shops: ['Empik', 'OLX']
		});
		expect(stickyAfter(before, form({ shop: '', acquired_as: '' }))).toEqual({
			shop: '',
			acquired_as: '',
			shops: ['OLX']
		});
		const wish = form({ status: 'wishlist', shop: 'Empik', acquired_as: 'new' });
		expect(stickyAfter(before, wish)).toBe(before);
	});
});

describe('copy payload', () => {
	it('sends a condition only with a used copy', () => {
		const used = scanCopyPayload(form({ acquired_as: 'used', condition: 'good' }), 1, 'k');
		expect(used.ok && used.value.condition).toBe('good');
		const fresh = scanCopyPayload(form({ acquired_as: 'new', condition: 'good' }), 1, 'k');
		expect(fresh.ok && fresh.value.condition).toBeNull();
	});

	it('sends a wish of only its edition, never for an album or an owned copy', () => {
		const wish = scanCopyPayload(form({ status: 'wishlist', exact_edition: true }), 1, 'k');
		expect(wish.ok && wish.value.exact_edition).toBe(true);
		const album = scanCopyPayload(
			form({ kind: 'music', format: 'cd', status: 'wishlist', exact_edition: true }),
			1,
			'k'
		);
		expect(album.ok && 'exact_edition' in album.value).toBe(false);
		const owned = scanCopyPayload(form({ exact_edition: true }), 1, 'k');
		expect(owned.ok && 'exact_edition' in owned.value).toBe(false);
		const any = scanCopyPayload(form({ status: 'wishlist' }), 1, 'k');
		expect(any.ok && 'exact_edition' in any.value).toBe(false);
	});
});

describe('queuedMessage', () => {
	it.each([
		[
			'not_found',
			'',
			'Kod trafił do kolejki do uzupełnienia. Dodaj go ręcznie teraz albo później.'
		],
		[
			'lookup_failed',
			'Serwer zwrócił błąd 503.',
			'Nie udało się sprawdzić kodu (Serwer zwrócił błąd 503). Kod trafił do kolejki do uzupełnienia.'
		],
		[
			'lookup_failed',
			'bn: timeout; tmdb: 503',
			'Nie udało się sprawdzić kodu (bn: timeout; tmdb: 503). Kod trafił do kolejki do uzupełnienia.'
		],
		['lookup_failed', ' ', 'Nie udało się sprawdzić kodu. Kod trafił do kolejki do uzupełnienia.']
	] as const)('explains %s with detail %j', (reason, detail, want) => {
		expect(queuedMessage({ reason, detail })).toBe(want);
	});
});

describe('isoDate', () => {
	it('gives the local calendar day', () => {
		expect(isoDate(new Date(2026, 0, 5, 23, 59))).toBe('2026-01-05');
		expect(isoDate(new Date(2026, 10, 19, 0, 1))).toBe('2026-11-19');
	});
});

describe('providerLabel', () => {
	it.each([
		['movie', {}, 'TMDB'],
		['tmdb', {}, 'TMDB'],
		['music', { discogs_release_id: 1 }, 'Discogs'],
		['music', {}, 'MusicBrainz'],
		['discogs', {}, 'Discogs'],
		['bn', {}, 'Biblioteka Narodowa'],
		['openlibrary', {}, 'Open Library'],
		['google_books', {}, 'Google Books'],
		['selection', {}, 'Twój wybór'],
		['stub', {}, 'stub']
	])('names %s', (provider, edition, want) => {
		const c: Candidate = {
			...movie,
			provider,
			edition: { barcode: '1', ...edition }
		};
		expect(providerLabel(c)).toBe(want);
	});
});

describe('searchTitles', () => {
	const album: Candidate = {
		type: 'music',
		title: { name: '1-800-OŚWIECENIE', authors: ['Taco Hemingway'] },
		edition: { barcode: '0602458447651', format: 'cd', discogs_release_id: 28352929 },
		provider: 'discogs',
		sources: [],
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
			sources: [],
			confidence: 0.6
		};
		expect(candidateLabel(book)).toBe('Stanisław Lem – Solaris · 1961 · openlibrary · 60%');
	});
});

describe('canonicalBarcode', () => {
	it.each([
		['an EAN-13', '5901234123457', '5901234123457'],
		['a UPC-A', '036000291452', '0036000291452'],
		['a GTIN-14 with a leading 0', '00036000291452', '0036000291452'],
		['a GTIN-14', '10036000291459', '10036000291459'],
		['an EAN-8', '40170725', '40170725'],
		['a padded EAN-8', '00000040170725', '40170725'],
		['an ISBN-10', '8324000011', '9788324000012'],
		['an ISBN-10 ending in X', '080442957X', '9780804429573'],
		['a wrong check digit', '5901234123458', '5901234123458']
	])('stores %s as the server does', (_, code, stored) => {
		expect(canonicalBarcode(code)).toBe(stored);
	});
});
