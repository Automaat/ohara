/**
 * The notices the Discogs API Terms of Use ask for, in English word for word, as the terms give
 * them.
 */
export const discogsCredit = 'Data provided by Discogs.';

export const discogsNotice =
	"This application uses Discogs' API but is not affiliated with, sponsored or endorsed by Discogs. 'Discogs' is a trademark of Zink Media, LLC.";

/** The discogs.com page of a release, or null without a usable release ID. */
export function discogsReleaseUrl(id: number | null | undefined): string | null {
	return id != null && Number.isSafeInteger(id) && id > 0
		? `https://www.discogs.com/release/${id}`
		: null;
}
