/**
 * A title's personal tags as the server compares them: trimmed by Go's rules and matched by the
 * key the server's foldCase makes, so the browser's filter lists what `?tag=` lists.
 */
import { lowerCase, trimSpace } from './go-text';

/**
 * The key tags match and merge by, as the server's foldCase makes it: lower case one character at
 * a time, with a final sigma as σ, since ΟΣ and ος are one tag.
 */
export function tagKey(tag: string): string {
	return lowerCase(tag).replaceAll('ς', 'σ');
}

/** The tag the server filters by when the address carries `tag`: trimmed by Go's rules. */
export function tagTerm(tag: string): string {
	return trimSpace(tag);
}

/** Whether a title's tags hold the filter's tag, case aside; an empty tag matches every title. */
export function tagMatches(tags: readonly string[] | undefined, tag = ''): boolean {
	const wanted = tagKey(tagTerm(tag));
	return !wanted || (tags ?? []).some((t) => tagKey(t) === wanted);
}
