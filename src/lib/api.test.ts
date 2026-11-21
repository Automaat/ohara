import { describe, expect, it, vi } from 'vitest';
import {
	ApiError,
	deadlineFetch,
	errorMessage,
	isNoAnswer,
	isSessionExpired,
	request,
	sessionExpiredMessage
} from './api';

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
		expect(isNoAnswer(err)).toBe(true);
		expect(isSessionExpired(err)).toBe(false);
	});

	it('takes a body that breaks off for no answer, unlike one that is not JSON', async () => {
		const broken = (async () =>
			new Response(
				new ReadableStream({
					start: (controller) => controller.error(new TypeError('network'))
				}),
				{ status: 200 }
			)) as typeof fetch;
		const err = await failure(request(broken, 'GET', '/api/editions'));
		expect(err).toMatchObject({ status: 0, message: 'Serwer nie odpowiada.' });
		expect(isNoAnswer(err)).toBe(true);
	});

	it('aborts a request still open at its deadline', async () => {
		vi.useFakeTimers();
		const signals: AbortSignal[] = [];
		const held = deadlineFetch(
			(_, init) =>
				new Promise<Response>((_resolve, reject) => {
					const signal = init?.signal;
					if (signal) signals.push(signal);
					signal?.addEventListener('abort', () => reject(signal.reason));
				}),
			1000
		);
		const pending = failure(request(held, 'GET', '/api/editions'));
		await vi.advanceTimersByTimeAsync(999);
		expect(signals[0]?.aborted).toBe(false);
		await vi.advanceTimersByTimeAsync(1);
		expect(await pending).toMatchObject({ status: 0 });
		vi.useRealTimers();
	});

	it('leaves a redirect unfollowed, so one to the sign-in is not taken for no answer', async () => {
		const calls: Call[] = [];
		await request(fake(200, '[]', calls), 'GET', '/api/titles');
		expect(calls[0].init?.redirect).toBe('manual');
	});

	it.each([
		['a 401', fake(401, 'Unauthorized')],
		['a 401 with JSON', fake(401, '{"error":"unauthorized"}')],
		[
			'a redirect',
			(async () => {
				const res = new Response(null, { status: 200 });
				Object.defineProperty(res, 'type', { value: 'opaqueredirect' });
				Object.defineProperty(res, 'status', { value: 0 });
				return res;
			}) as typeof fetch
		]
	])('takes %s for an expired session', async (_, fetchFn) => {
		const err = await failure(request(fetchFn, 'POST', '/api/scan-queue', {}));
		expect(err).toMatchObject({ status: 401, message: sessionExpiredMessage });
		expect(isSessionExpired(err)).toBe(true);
		expect(isNoAnswer(err)).toBe(false);
	});
});

describe('errorMessage', () => {
	it('reads ApiError messages and hides others', () => {
		expect(errorMessage(new ApiError('Nie znaleziono.', 404))).toBe('Nie znaleziono.');
		expect(errorMessage(new Error('boom'))).toBe('Nieznany błąd.');
	});
});
