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

/** The JPEG quality the cover sheet encodes at. */
export const coverQuality = 0.85;

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

/** A decoded photo, upright, with its size in pixels. */
export type DecodedPhoto = { readonly width: number; readonly height: number; close(): void };

/** How a photo is read and drawn again; the browser's by default, a stand-in in tests. */
export type PhotoCodec<D extends DecodedPhoto> = {
	decode(photo: Blob): Promise<D>;
	/** The photo drawn at the given size as a JPEG, or null when the browser could not. */
	encode(photo: D, width: number, height: number): Promise<Blob | null>;
};

/**
 * The browser's codec. `createImageBitmap` turns the photo upright from its EXIF orientation and
 * needs no URL for the file, so the page's Content-Security-Policy stays as it is. Only the small
 * canvas the photo is drawn into is ever allocated, never one at the photo's own size, which Safari
 * on iOS refuses above 16 megapixels. A transparent PNG gets a white background.
 */
export const browserCodec: PhotoCodec<ImageBitmap> = {
	decode: (photo) => createImageBitmap(photo),
	async encode(photo, width, height) {
		const canvas = document.createElement('canvas');
		canvas.width = width;
		canvas.height = height;
		try {
			const context = canvas.getContext('2d');
			if (!context) return null;
			context.fillStyle = '#fff';
			context.fillRect(0, 0, width, height);
			context.imageSmoothingQuality = 'high';
			context.drawImage(photo, 0, 0, width, height);
			return await new Promise<Blob | null>((resolve) =>
				canvas.toBlob(resolve, 'image/jpeg', coverQuality)
			);
		} finally {
			// Safari keeps a canvas's memory until its size drops, even once nothing refers to it.
			canvas.width = 0;
			canvas.height = 0;
		}
	}
};

/**
 * The photo the cover sheet sends for the one chosen: upright, at most `maxCoverEdge` pixels on
 * its long edge, as JPEG. A file the browser cannot read, or a result over the server's limit,
 * throws a PhotoError with the message to show, and nothing is sent.
 */
export async function shrinkPhoto<D extends DecodedPhoto>(
	photo: Blob,
	codec: PhotoCodec<D>
): Promise<Blob> {
	let decoded: D;
	try {
		decoded = await codec.decode(photo);
	} catch {
		throw new PhotoError(invalidCoverMessage);
	}
	let shrunk: Blob | null;
	try {
		if (decoded.width <= 0 || decoded.height <= 0) throw new PhotoError(invalidCoverMessage);
		const size = coverSize(decoded.width, decoded.height);
		shrunk = await codec.encode(decoded, size.width, size.height);
	} catch (err) {
		throw err instanceof PhotoError ? err : new PhotoError(shrinkFailedMessage);
	} finally {
		decoded.close();
	}
	if (!shrunk) throw new PhotoError(shrinkFailedMessage);
	const problem = photoProblem(shrunk);
	if (problem) throw new PhotoError(problem);
	return shrunk;
}
