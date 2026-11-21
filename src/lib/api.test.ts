import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError, errorMessage, request } from './api';

type Call = { url: string; init: RequestInit | undefined };

function fake(status: number, body: string | null, calls: Call[] = []): typeof fetch {
	return async (url, init) => {
		calls.push({ url: String(url), init });
		return new Response(body, { status });
	};
}

async function failure(p: Promise<unknown>): Promise<ApiError> {
	const err = await p.catch((e: unknown) => e);
	expect(err).toBeInstanceOf(ApiError);
	return err as ApiError;
}

describe('request', () => {
	it('sends JSON and decodes the body', async () => {
		const calls: Call[] = [];
		const got = await request<{ id: number }>(fake(201, '{"id":7}', calls), 'POST', '/api/titles', {
			name: 'Miś'
		});
		expect(got).toEqual({ id: 7 });
		expect(calls[0].url).toBe('/api/titles');
		expect(calls[0].init?.method).toBe('POST');
		expect(calls[0].init?.body).toBe('{"name":"Miś"}');
		expect(calls[0].init?.headers).toMatchObject({ 'content-type': 'application/json' });
	});

	it('sends no body on GET', async () => {
		const calls: Call[] = [];
		await request(fake(200, '[]', calls), 'GET', '/api/titles');
		expect(calls[0].init?.body).toBeUndefined();
	});

	it('returns undefined on 204', async () => {
		await expect(request(fake(204, null), 'DELETE', '/api/copies/1')).resolves.toBeUndefined();
	});

	it.each([
		[
			409,
			'{"error":"dup","field":"barcode"}',
			'Wydanie z tym kodem kreskowym już istnieje.',
			'barcode'
		],
		[
			422,
			'{"error":"bad","field":"barcode"}',
			'Nieprawidłowy kod kreskowy: sprawdź cyfry.',
			'barcode'
		],
		[
			422,
			'{"error":"cannot be empty","field":"name"}',
			'Nieprawidłowe dane: cannot be empty.',
			'name'
		],
		[
			409,
			'{"error":"title still has editions"}',
			'Nie można usunąć: pozycja ma powiązane wydania lub egzemplarze.',
			undefined
		],
		[404, '{"error":"not found"}', 'Nie znaleziono.', undefined],
		[500, '{"error":"internal server error"}', 'Serwer zwrócił błąd 500.', undefined],
		[502, '<html>', 'Serwer zwrócił błąd 502.', undefined],
		[200, '<html>', 'Serwer zwrócił nieczytelną odpowiedź.', undefined]
	])('maps %i %s', async (status, body, message, field) => {
		const err = await failure(request(fake(status, body), 'GET', '/api/x'));
		expect(err.message).toBe(message);
		expect(err.status).toBe(status);
		expect(err.field).toBe(field);
	});

	it('maps a null error body', async () => {
		const err = await failure(request(fake(500, 'null'), 'GET', '/api/x'));
		expect(err.message).toBe('Serwer zwrócił błąd 500.');
	});

	it('reports network failures', async () => {
		const offline = (async () => {
			throw new TypeError('offline');
		}) as typeof fetch;
		const err = await failure(request(offline, 'GET', '/api/x'));
		expect(err.message).toBe('Serwer nie odpowiada.');
	});
});

describe('request deadline', () => {
	afterEach(() => {
		vi.useRealTimers();
	});

	/** A fetch that never settles, not even when its signal aborts; records the signal it got. */
	function stalled(signals: (AbortSignal | undefined)[] = []): typeof fetch {
		return (_input, init) => {
			signals.push(init?.signal ?? undefined);
			return new Promise<Response>(() => undefined);
		};
	}

	/** Tracks a promise's outcome without letting a rejection go unhandled. */
	function track(p: Promise<unknown>) {
		const state: { settled: boolean; error?: unknown } = { settled: false };
		p.then(
			() => (state.settled = true),
			(err: unknown) => {
				state.settled = true;
				state.error = err;
			}
		);
		return state;
	}

	it('fails as no answer once 20 seconds pass, and not before', async () => {
		vi.useFakeTimers();
		const signals: (AbortSignal | undefined)[] = [];
		const state = track(request(stalled(signals), 'GET', '/api/lookup/5901234123457'));

		await vi.advanceTimersByTimeAsync(19_999);
		expect(state.settled).toBe(false);
		expect(signals[0]?.aborted).toBe(false);

		await vi.advanceTimersByTimeAsync(1);
		expect(state.error).toBeInstanceOf(ApiError);
		expect(state.error).toMatchObject({ status: 0, message: 'Serwer nie odpowiada.' });
		expect(signals[0]?.aborted).toBe(true);
	});

	it('fails a response whose body never arrives', async () => {
		vi.useFakeTimers();
		const headersOnly: typeof fetch = async () =>
			new Response(new ReadableStream({ start() {} }), { status: 200 });
		const state = track(request(headersOnly, 'GET', '/api/collection'));

		await vi.advanceTimersByTimeAsync(20_000);
		expect(state.error).toMatchObject({ status: 0, message: 'Serwer nie odpowiada.' });
	});

	it('waits as long as a longer deadline allows', async () => {
		vi.useFakeTimers();
		const state = track(
			request(stalled(), 'PUT', '/api/editions/3/cover', new Blob(['x']), 120_000)
		);

		await vi.advanceTimersByTimeAsync(119_999);
		expect(state.settled).toBe(false);

		await vi.advanceTimersByTimeAsync(1);
		expect(state.error).toMatchObject({ status: 0 });
	});

	it('leaves no timer behind once answered', async () => {
		vi.useFakeTimers();
		await expect(request(fake(200, '[]'), 'GET', '/api/titles')).resolves.toEqual([]);
		await expect(request(fake(500, '{}'), 'GET', '/api/titles')).rejects.toBeInstanceOf(ApiError);
		expect(vi.getTimerCount()).toBe(0);
	});
});

describe('errorMessage', () => {
	it('reads ApiError messages and hides others', () => {
		expect(errorMessage(new ApiError('Nie znaleziono.', 404))).toBe('Nie znaleziono.');
		expect(errorMessage(new Error('boom'))).toBe('Nieznany błąd.');
	});
});
