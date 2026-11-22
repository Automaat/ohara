import type { CopyDiscogs } from './api.gen';

export type { CopyDiscogs } from './api.gen';

/** What the copy page offers for the Discogs collection. */
export type DiscogsShelf = {
	/** The Discogs page of the release the copy is an instance of, once pushed. */
	releaseUrl: string | null;
	/** The push button's label, null when the copy cannot be pushed. */
	action: string | null;
};

/**
 * The copy page's Discogs collection part, or null when it shows none: without a token, or for a
 * copy neither pushable nor pushed before (a wish, a film, a book, an album without a release).
 */
export function discogsShelf(state: CopyDiscogs | null): DiscogsShelf | null {
	if (!state?.enabled) return null;
	const releaseUrl = state.instance_id !== null ? state.release_url : null;
	const action = state.pushable
		? releaseUrl
			? 'Zaktualizuj stan w Discogs'
			: 'Dodaj do kolekcji Discogs'
		: null;
	return releaseUrl || action ? { releaseUrl, action } : null;
}
