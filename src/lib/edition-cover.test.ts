import { describe, expect, it } from 'vitest';
import { coverTooLargeMessage, invalidCoverMessage, request } from './api';
import {
	coverSize,
	coverUndo,
	maxCoverBytes,
	maxCoverEdge,
	photoProblem,
	PhotoError,
	shrinkFailedMessage,
	shrinkPhoto,
	withDeadline,
	type PhotoCodec
} from './edition-cover';

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

describe('coverSize', () => {
	it.each([
		{ width: 4032, height: 3024, want: { width: 1600, height: 1200 } },
		{ width: 6000, height: 4000, want: { width: 1600, height: 1067 } },
		{ width: 3024, height: 4032, want: { width: 1200, height: 1600 } },
		{ width: 800, height: 600, want: { width: 800, height: 600 } },
		{ width: 1600, height: 1600, want: { width: 1600, height: 1600 } },
		{ width: 1601, height: 1, want: { width: 1600, height: 1 } },
		{ width: 20000, height: 2, want: { width: 1600, height: 1 } }
	])('$width × $height → $want.width × $want.height', ({ width, height, want }) => {
		expect(coverSize(width, height)).toEqual(want);
	});

	it('keeps the long edge within the limit', () => {
		expect(maxCoverEdge).toBe(1600);
	});
});

describe('shrinkPhoto', () => {
	type Fake = { width: number; height: number; closed: boolean; close(): void };
	const decoded = (width: number, height: number): Fake => ({
		width,
		height,
		closed: false,
		close() {
			this.closed = true;
		}
	});
	const jpeg = (size: number) => new Blob([new Uint8Array(size)], { type: 'image/jpeg' });
	const photo = new Blob(['photo']);

	it('sends the photo drawn at its cover size', async () => {
		const image = decoded(4032, 3024);
		const drawn: [number, number][] = [];
		const out = jpeg(300_000);
		const codec: PhotoCodec<Fake> = {
			decode: async (p) => {
				expect(p).toBe(photo);
				return image;
			},
			encode: async (p, width, height) => {
				expect(p).toBe(image);
				drawn.push([width, height]);
				return out;
			}
		};
		await expect(shrinkPhoto(photo, codec)).resolves.toBe(out);
		expect(drawn).toEqual([[1600, 1200]]);
		expect(image.closed).toBe(true);
	});

	it.each([
		{
			name: 'a file the browser cannot read',
			codec: {
				decode: async () => Promise.reject(new DOMException('bad', 'InvalidStateError')),
				encode: async () => jpeg(1)
			},
			want: invalidCoverMessage
		},
		{
			name: 'a photo without pixels',
			codec: { decode: async () => decoded(0, 10), encode: async () => jpeg(1) },
			want: invalidCoverMessage
		},
		{
			name: 'a canvas that yields nothing',
			codec: { decode: async () => decoded(10, 10), encode: async () => null },
			want: shrinkFailedMessage
		},
		{
			name: 'a canvas that throws',
			codec: {
				decode: async () => decoded(10, 10),
				encode: async () => Promise.reject(new Error('out of memory'))
			},
			want: shrinkFailedMessage
		},
		{
			name: 'a result over the server limit',
			codec: { decode: async () => decoded(10, 10), encode: async () => jpeg(maxCoverBytes + 1) },
			want: coverTooLargeMessage
		}
	] satisfies { name: string; codec: PhotoCodec<Fake>; want: string }[])(
		'$name fails with its message',
		async ({ codec, want }) => {
			const err = await shrinkPhoto(photo, codec).catch((e: unknown) => e);
			expect(err).toBeInstanceOf(PhotoError);
			expect((err as PhotoError).message).toBe(want);
		}
	);

	it('releases the decoded photo when drawing fails', async () => {
		const image = decoded(10, 10);
		await expect(
			shrinkPhoto(photo, { decode: async () => image, encode: async () => null })
		).rejects.toBeInstanceOf(PhotoError);
		expect(image.closed).toBe(true);
	});
});
