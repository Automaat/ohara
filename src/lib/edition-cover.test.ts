import { describe, expect, it } from 'vitest';
import { request } from './api';
import { coverUndo, maxCoverBytes, photoProblem, withDeadline } from './edition-cover';

describe('coverUndo', () => {
	const own = '/api/editions/3/cover?v=1700000000';

	it.each([
		{
			name: 'no personal photo',
			override: undefined,
			edition: 'https://e/c.jpg',
			title: undefined,
			want: null
		},
		{
			name: 'photo over the edition cover',
			override: own,
			edition: 'https://e/c.jpg',
			title: undefined,
			want: 'restore'
		},
		{
			name: 'photo over the title cover',
			override: own,
			edition: undefined,
			title: 'https://t/c.jpg',
			want: 'restore'
		},
		{
			name: 'photo with empty provider covers',
			override: own,
			edition: '',
			title: '',
			want: 'remove'
		},
		{
			name: 'photo without a provider cover',
			override: own,
			edition: undefined,
			title: undefined,
			want: 'remove'
		}
	])('$name → $want', ({ override, edition, title, want }) => {
		expect(
			coverUndo({ cover_override_url: override, cover_url: edition, title: { cover_url: title } })
		).toBe(want);
	});
});

describe('photoProblem', () => {
	const photo = (size: number) => ({ size }) as Blob;

	it.each([
		{ size: 0, want: null },
		{ size: maxCoverBytes, want: null },
		{ size: maxCoverBytes + 1, want: 'Wybierz plik o rozmiarze do 8 MiB.' },
		{ size: 9 * 1024 * 1024, want: 'Wybierz plik o rozmiarze do 8 MiB.' }
	])('$size bytes → $want', ({ size, want }) => {
		expect(photoProblem(photo(size))).toBe(want);
	});

	it('takes the limit the server enforces', () => {
		expect(maxCoverBytes).toBe(8 * 1024 * 1024);
	});
});

describe('withDeadline', () => {
	it('gives up on a request that never answers', async () => {
		const stalled: typeof fetch = (_input, init) =>
			new Promise((_resolve, reject) => {
				const signal = init?.signal;
				signal?.addEventListener('abort', () => reject(signal.reason));
			});
		await expect(
			request(withDeadline(stalled, 5), 'PUT', '/api/editions/3/cover', new Blob(['x']))
		).rejects.toMatchObject({ status: 0, message: 'Serwer nie odpowiada.' });
	});

	it('sends the request as given', async () => {
		const seen: RequestInit[] = [];
		const answered: typeof fetch = async (_input, init) => {
			seen.push(init ?? {});
			return new Response(null, { status: 204 });
		};
		await expect(
			request(withDeadline(answered, 1000), 'DELETE', '/api/editions/3/cover')
		).resolves.toBeUndefined();
		expect(seen).toHaveLength(1);
		expect(seen[0].method).toBe('DELETE');
		expect(seen[0].signal?.aborted).toBe(false);
	});
});
