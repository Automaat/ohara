import { coverTooLargeMessage, invalidCoverMessage } from '#lib/api.js';
import type { Edition, Title } from '#lib/collection.js';

/** The largest photo `PUT /api/editions/{id}/cover` takes, as `maxCoverBytes` in the backend. */
export const maxCoverBytes = 8 << 20;

/** How long a cover upload or removal may take before the sheet gives up on it: long enough for
 * 8 MiB over a slow phone connection, short enough that a stalled request does not hold the
 * sheet, which refuses to close while it works. */
export const coverDeadlineMs = 120_000;

/** `fetchFn` with every request aborted after `ms`, which `request` reports as no answer. */
export function withDeadline(fetchFn: typeof fetch, ms: number): typeof fetch {
	return (input, init) => fetchFn(input, { ...init, signal: AbortSignal.timeout(ms) });
}

/** How the cover sheet drops a personal photo: `restore` shows the provider cover it hides again,
 * `remove` leaves the edition without a cover. */
export type CoverUndo = 'restore' | 'remove';

/** What drops the edition's personal photo, or null when it has none. */
export function coverUndo(
	edition: Pick<Edition, 'cover_override_url' | 'cover_url'> & { title: Pick<Title, 'cover_url'> }
): CoverUndo | null {
	if (!edition.cover_override_url) return null;
	return edition.cover_url || edition.title.cover_url ? 'restore' : 'remove';
}

/** Why a shrunk photo is not sent, or null. One over the server's limit gets the server's own
 * message before it uploads, so a phone never sends megabytes only to have them refused. */
export function photoProblem(photo: Blob): string | null {
	return photo.size > maxCoverBytes ? coverTooLargeMessage : null;
}

/** The longest edge, in pixels, of the photo the cover sheet sends: enough for the shelf and the
 * hero at a phone's pixel density, and a few hundred kilobytes as JPEG. */
export const maxCoverEdge = 1600;

/** The JPEG qualities the cover sheet tries, best first: a detailed photo that stays over
 * `coverTargetBytes` at one is encoded again at the next. */
export const coverQualities = [0.85, 0.75, 0.65, 0.5, 0.35] as const;

/** The upload size the cover sheet aims below. The lowest quality is sent even when over it, since
 * the server's own limit is far higher. */
export const coverTargetBytes = 1_000_000;

/** What a photo the browser read but could not draw again says. */
export const shrinkFailedMessage = 'Nie udało się zmniejszyć zdjęcia. Spróbuj ponownie.';

/** Why a chosen photo cannot be sent; its message is for the reader. */
export class PhotoError extends Error {}

/** The size a photo is sent at: its own when its long edge fits in `maxCoverEdge`, otherwise
 * scaled down to it with the same proportions. */
export function coverSize(width: number, height: number): { width: number; height: number } {
	const scale = Math.min(1, maxCoverEdge / Math.max(width, height));
	return {
		width: Math.max(1, Math.round(width * scale)),
		height: Math.max(1, Math.round(height * scale))
	};
}

const jpegSignature = [0xff, 0xd8, 0xff];
const pngSignature = [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a];

/** Whether the file starts like a JPEG or a PNG, the formats the server takes. The browser would
 * read and convert others too, such as GIF or HEIC, which the sheet does not offer. */
export async function isJpegOrPng(photo: Blob): Promise<boolean> {
	const head = new Uint8Array(await photo.slice(0, pngSignature.length).arrayBuffer());
	const startsWith = (signature: number[]) => signature.every((byte, i) => head[i] === byte);
	return startsWith(jpegSignature) || startsWith(pngSignature);
}

/** A decoded photo, upright, with its size in pixels. */
export type DecodedPhoto = { readonly width: number; readonly height: number; close(): void };

/** A photo drawn at its cover size, ready to encode as JPEG until released. */
export type DrawnPhoto = {
	/** The drawing as a JPEG at the quality, or null when the browser could not encode it. */
	encode(quality: number): Promise<Blob | null>;
	release(): void;
};

/** How a photo is read and drawn again; the browser's by default, a stand-in in tests. */
export type PhotoCodec<D extends DecodedPhoto> = {
	decode(photo: Blob): Promise<D>;
	/** The photo drawn at the given size, or null when the browser could not draw it. */
	draw(photo: D, width: number, height: number): DrawnPhoto | null;
};

/**
 * The browser's codec. `createImageBitmap` turns the photo upright from its EXIF orientation and
 * needs no URL for the file, so the page's Content-Security-Policy stays as it is. Only the small
 * canvas the photo is drawn into is ever allocated, never one at the photo's own size, which Safari
 * on iOS refuses above 16 megapixels. A transparent PNG gets a white background.
 */
export const browserCodec: PhotoCodec<ImageBitmap> = {
	decode: (photo) => createImageBitmap(photo),
	draw(photo, width, height) {
		const canvas = document.createElement('canvas');
		canvas.width = width;
		canvas.height = height;
		// Safari keeps a canvas's memory until its size drops, even once nothing refers to it.
		const release = () => {
			canvas.width = 0;
			canvas.height = 0;
		};
		const context = canvas.getContext('2d');
		if (!context) {
			release();
			return null;
		}
		context.fillStyle = '#fff';
		context.fillRect(0, 0, width, height);
		context.imageSmoothingQuality = 'high';
		context.drawImage(photo, 0, 0, width, height);
		return {
			encode: (quality) =>
				new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, 'image/jpeg', quality)),
			release
		};
	}
};

/**
 * The photo the cover sheet sends for the one chosen: upright, at most `maxCoverEdge` pixels on
 * its long edge, as JPEG at the best of `coverQualities` that stays below `coverTargetBytes`. A
 * file that is no JPEG or PNG, one the browser cannot read, or a result over the server's limit
 * throws a PhotoError with the message to show, and nothing is sent.
 */
export async function shrinkPhoto<D extends DecodedPhoto>(
	photo: Blob,
	codec: PhotoCodec<D>
): Promise<Blob> {
	let decoded: D;
	try {
		if (!(await isJpegOrPng(photo))) throw new PhotoError(invalidCoverMessage);
		decoded = await codec.decode(photo);
	} catch {
		throw new PhotoError(invalidCoverMessage);
	}
	let drawn: DrawnPhoto | null;
	try {
		if (decoded.width <= 0 || decoded.height <= 0) throw new PhotoError(invalidCoverMessage);
		const size = coverSize(decoded.width, decoded.height);
		drawn = codec.draw(decoded, size.width, size.height);
	} catch (err) {
		throw err instanceof PhotoError ? err : new PhotoError(shrinkFailedMessage);
	} finally {
		decoded.close();
	}
	if (!drawn) throw new PhotoError(shrinkFailedMessage);
	let shrunk: Blob | null = null;
	try {
		for (const quality of coverQualities) {
			shrunk = await drawn.encode(quality);
			if (!shrunk || shrunk.size < coverTargetBytes) break;
		}
	} catch {
		shrunk = null;
	} finally {
		drawn.release();
	}
	if (!shrunk) throw new PhotoError(shrinkFailedMessage);
	const problem = photoProblem(shrunk);
	if (problem) throw new PhotoError(problem);
	return shrunk;
}
