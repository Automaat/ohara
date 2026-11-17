import { describe, expect, it } from 'vitest';
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

describe('errorMessage', () => {
	it('reads ApiError messages and hides others', () => {
		expect(errorMessage(new ApiError('Nie znaleziono.', 404))).toBe('Nie znaleziono.');
		expect(errorMessage(new Error('boom'))).toBe('Nieznany błąd.');
	});
});
