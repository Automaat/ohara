import type { CopyDiscogs } from './api.gen';

export type { CopyDiscogs } from './api.gen';

/** What the copy page offers for the Discogs collection. */
export type DiscogsShelf = {
	/** The Discogs page of the release the copy is an instance of, once pushed. */
	releaseUrl: string | null;
	/** The push button's label, null when the copy cannot be pushed or an add awaits the owner. */
	action: string | null;
	/** An earlier add's outcome is unknown: the owner confirms or discards it. */
	addUnknown: boolean;
};

/** What the page says while an add's outcome is unknown. */
export const addUnknownNotice =
	'Nie wiadomo, czy ostatnie dodanie do Discogs się udało. Jeśli to wydanie jest już w Twojej kolekcji Discogs i dodała je Ohara, potwierdź; jeśli dodano je inaczej, porzuć to dodanie. Jeśli go tam jeszcze nie ma, poczekaj kilka minut: Discogs może pokazać je z opóźnieniem.';

/**
 * The copy page's Discogs collection part, or null when it shows none: without a token, or for a
 * copy neither pushable nor pushed before (a wish, a film, a book, an album without a release) with
 * no add awaiting the owner.
 */
export function discogsShelf(state: CopyDiscogs | null): DiscogsShelf | null {
	if (!state?.enabled) return null;
	const releaseUrl = state.instance_id !== null ? state.release_url : null;
	const addUnknown = state.add_unknown;
	const action =
		state.pushable && !addUnknown
			? releaseUrl
				? 'Zaktualizuj stan w Discogs'
				: 'Dodaj do kolekcji Discogs'
			: null;
	return releaseUrl || action || addUnknown ? { releaseUrl, action, addUnknown } : null;
}
