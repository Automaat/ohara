/**
 * A title's personal tags as the server compares them: trimmed by Go's rules and matched case
 * aside, one character at a time as PostgreSQL's `lower` maps them.
 */
import { lowerCase, trimSpace } from './go-text';

/** The tag the server filters by when the address carries `tag`: trimmed by Go's rules. */
export function tagTerm(tag: string): string {
	return trimSpace(tag);
}

/** Whether a title's tags hold the filter's tag, case aside; an empty tag matches every title. */
export function tagMatches(tags: readonly string[] | undefined, tag = ''): boolean {
	const wanted = lowerCase(tagTerm(tag));
	return !wanted || (tags ?? []).some((t) => lowerCase(t) === wanted);
}
