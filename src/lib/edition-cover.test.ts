import { describe, expect, it } from 'vitest';
import { coverTooLargeMessage, invalidCoverMessage, request } from './api';
import {
	coverQualities,
	coverSize,
	coverTargetBytes,
	coverUndo,
	isJpegOrPng,
	maxCoverBytes,
	maxCoverEdge,
	photoProblem,
	PhotoError,
	shrinkFailedMessage,
	shrinkPhoto,
	withDeadline,
	type DrawnPhoto,
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

describe('isJpegOrPng', () => {
	it.each([
		{ name: 'JPEG', bytes: [0xff, 0xd8, 0xff, 0xe0, 0, 0x10], want: true },
		{ name: 'PNG', bytes: [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0], want: true },
		{ name: 'GIF', bytes: [0x47, 0x49, 0x46, 0x38, 0x39, 0x61], want: false },
		{ name: 'truncated PNG', bytes: [0x89, 0x50, 0x4e, 0x47], want: false },
		{ name: 'empty', bytes: [], want: false }
	])('$name → $want', async ({ bytes, want }) => {
		await expect(isJpegOrPng(new Blob([new Uint8Array(bytes)]))).resolves.toBe(want);
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
	const photo = new Blob([new Uint8Array([0xff, 0xd8, 0xff, 0xe0, 1, 2, 3, 4])]);

	/** A drawing whose encodes answer from sizes, one per quality tried, recording each quality. */
	function drawing(sizes: (number | null)[]) {
		const qualities: number[] = [];
		const state = { released: false, qualities };
		const drawn: DrawnPhoto = {
			encode: async (quality) => {
				qualities.push(quality);
				const size = sizes[qualities.length - 1];
				return size === null ? null : jpeg(size);
			},
			release: () => {
				state.released = true;
			}
		};
		return { drawn, state };
	}

	it('sends the photo drawn at its cover size, at the first quality under the target', async () => {
		const image = decoded(4032, 3024);
		const { drawn, state } = drawing([300_000]);
		const drawnAt: [number, number][] = [];
		const codec: PhotoCodec<Fake> = {
			decode: async (p) => {
				expect(p).toBe(photo);
				return image;
			},
			draw: (p, width, height) => {
				expect(p).toBe(image);
				expect(image.closed).toBe(false);
				drawnAt.push([width, height]);
				return drawn;
			}
		};
		const out = await shrinkPhoto(photo, codec);
		expect(out.size).toBe(300_000);
		expect(drawnAt).toEqual([[1600, 1200]]);
		expect(state.qualities).toEqual([coverQualities[0]]);
		expect(image.closed).toBe(true);
		expect(state.released).toBe(true);
	});

	it('lowers the quality until a detailed photo fits the target', async () => {
		const { drawn, state } = drawing([2_100_000, 1_300_000, 990_000, 1]);
		const out = await shrinkPhoto(photo, {
			decode: async () => decoded(4032, 3024),
			draw: () => drawn
		});
		expect(out.size).toBe(990_000);
		expect(state.qualities).toEqual(coverQualities.slice(0, 3));
		expect(state.released).toBe(true);
	});

	it('sends the lowest quality when none fits the target', async () => {
		const sizes = coverQualities.map((_, i) => coverTargetBytes + 100 - i);
		const { drawn, state } = drawing(sizes);
		const out = await shrinkPhoto(photo, {
			decode: async () => decoded(10, 10),
			draw: () => drawn
		});
		expect(out.size).toBe(sizes.at(-1));
		expect(state.qualities).toEqual([...coverQualities]);
	});

	it('reads no file that is no JPEG or PNG', async () => {
		const gif = new Blob([new Uint8Array([0x47, 0x49, 0x46, 0x38, 0x39, 0x61])]);
		let decodes = 0;
		const err = await shrinkPhoto(gif, {
			decode: async () => {
				decodes++;
				return decoded(1, 1);
			},
			draw: () => drawing([1]).drawn
		}).catch((e: unknown) => e);
		expect(err).toBeInstanceOf(PhotoError);
		expect((err as PhotoError).message).toBe(invalidCoverMessage);
		expect(decodes).toBe(0);
	});

	it.each([
		{
			name: 'a file the browser cannot read',
			codec: {
				decode: async () => Promise.reject(new DOMException('bad', 'InvalidStateError')),
				draw: () => drawing([1]).drawn
			},
			want: invalidCoverMessage
		},
		{
			name: 'a photo without pixels',
			codec: { decode: async () => decoded(0, 10), draw: () => drawing([1]).drawn },
			want: invalidCoverMessage
		},
		{
			name: 'a canvas that cannot draw',
			codec: { decode: async () => decoded(10, 10), draw: () => null },
			want: shrinkFailedMessage
		},
		{
			name: 'a canvas that throws while drawing',
			codec: {
				decode: async () => decoded(10, 10),
				draw: () => {
					throw new Error('out of memory');
				}
			},
			want: shrinkFailedMessage
		},
		{
			name: 'a canvas that yields nothing',
			codec: { decode: async () => decoded(10, 10), draw: () => drawing([null]).drawn },
			want: shrinkFailedMessage
		},
		{
			name: 'a canvas that throws while encoding',
			codec: {
				decode: async () => decoded(10, 10),
				draw: () => ({
					encode: async () => Promise.reject(new Error('out of memory')),
					release: () => {}
				})
			},
			want: shrinkFailedMessage
		},
		{
			name: 'a result over the server limit',
			codec: {
				decode: async () => decoded(10, 10),
				draw: () => drawing(coverQualities.map(() => maxCoverBytes + 1)).drawn
			},
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

	it('releases the decoded photo and the drawing when encoding fails', async () => {
		const image = decoded(10, 10);
		const { drawn, state } = drawing([null]);
		await expect(
			shrinkPhoto(photo, { decode: async () => image, draw: () => drawn })
		).rejects.toBeInstanceOf(PhotoError);
		expect(image.closed).toBe(true);
		expect(state.released).toBe(true);
	});
});
