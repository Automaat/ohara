import { describe, expect, it } from 'vitest';
import { databaseBadge, fetchStatus, StatusError, type DatabaseState } from './status';

function respond(body: string, status: number): typeof fetch {
	return async () => new Response(body, { status });
}

describe('fetchStatus', () => {
	it('returns the status on 200', async () => {
		const body = JSON.stringify({ status: 'ok', version: 'v0.1.0', database: 'ok' });
		await expect(fetchStatus(respond(body, 200))).resolves.toEqual({
			status: 'ok',
			version: 'v0.1.0',
			database: 'ok'
		});
	});

	it('reads the body of a 503', async () => {
		const body = JSON.stringify({ status: 'degraded', version: 'v0.1.0', database: 'unavailable' });
		await expect(fetchStatus(respond(body, 503))).resolves.toMatchObject({
			database: 'unavailable'
		});
	});

	it.each([
		['other HTTP errors', respond('nope', 502), 'Serwer zwrócił błąd 502.'],
		['unparseable bodies', respond('<html>', 200), 'Serwer zwrócił nieczytelną odpowiedź.'],
		[
			'network failures',
			(async () => {
				throw new TypeError('offline');
			}) as typeof fetch,
			'Serwer nie odpowiada.'
		]
	])('rejects %s with a StatusError', async (_name, fetchFn, message) => {
		const err = await fetchStatus(fetchFn).catch((e: unknown) => e);
		expect(err).toBeInstanceOf(StatusError);
		expect((err as StatusError).message).toBe(message);
	});
});

describe('databaseBadge', () => {
	it.each([
		['ok', 'success'],
		['unavailable', 'danger'],
		['unconfigured', 'warning'],
		['bogus', 'warning']
	])('%s → %s', (state, tone) => {
		expect(databaseBadge(state as DatabaseState).tone).toBe(tone);
	});
});
