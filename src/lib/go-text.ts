/** The white space Go's strings.TrimSpace drops: unicode.IsSpace, U+0085 included, U+FEFF not. */
const goSpace =
	'[\\t\\n\\v\\f\\r \\u0085\\u00a0\\u1680\\u2000-\\u200a\\u2028\\u2029\\u202f\\u205f\\u3000]';
const goTrim = new RegExp(`^${goSpace}+|${goSpace}+$`, 'g');

/**
 * The text as Go's strings.TrimSpace leaves it, so a value the browser trims matches the one the
 * server stores: unlike String.prototype.trim it keeps a byte order mark.
 */
export function trimSpace(text: string): string {
	return text.replace(goTrim, '');
}

/**
 * Lower case one character at a time, as PostgreSQL's `lower` and Go's unicode.ToLower map each
 * character on its own: a final capital sigma becomes σ, not ς, and a character whose lower case
 * is longer keeps only its first.
 */
export function lowerCase(text: string): string {
	let out = '';
	for (const char of text) {
		const lower = char.toLowerCase();
		out += [...lower][0] ?? lower;
	}
	return out;
}
