import { coverTooLargeMessage } from '#lib/api.js';
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

/** Why a chosen photo is not sent, or null. A file over the server's limit gets the server's own
 * message before it uploads, so a phone never sends megabytes only to have them refused. */
export function photoProblem(photo: Blob): string | null {
	return photo.size > maxCoverBytes ? coverTooLargeMessage : null;
}
