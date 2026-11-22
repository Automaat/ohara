import { createHash } from 'node:crypto';
import { html as spec, parse } from 'parse5';

/** @typedef {import('parse5').DefaultTreeAdapterTypes.Element} Element */
/** @typedef {import('parse5').DefaultTreeAdapterTypes.ParentNode} ParentNode */
/** @typedef {Map<string, string[]>} Policy directive name (lowercase) → source expressions */

/** Keywords no script directive may carry, compared ASCII case-insensitively as browsers do. */
const forbidden = ["'unsafe-inline'", "'unsafe-eval'"];

/** Each script directive and the directives a browser falls back to when it is absent. */
const fallbacks = {
	'script-src': ['script-src', 'default-src'],
	'script-src-elem': ['script-src-elem', 'script-src', 'default-src'],
	'script-src-attr': ['script-src-attr', 'script-src', 'default-src']
};

/** The HTML standard's JavaScript MIME type essences: a `type` naming one runs as a classic script. */
const javascriptTypes = new Set([
	'application/ecmascript',
	'application/javascript',
	'application/x-ecmascript',
	'application/x-javascript',
	'text/ecmascript',
	'text/javascript',
	'text/javascript1.0',
	'text/javascript1.1',
	'text/javascript1.2',
	'text/javascript1.3',
	'text/javascript1.4',
	'text/javascript1.5',
	'text/jscript',
	'text/livescript',
	'text/x-ecmascript',
	'text/x-javascript'
]);

/** Script types other than classic whose inline text the policy still has to allow. */
const otherScriptTypes = new Set(['module', 'importmap', 'speculationrules']);

/** @param {Element} element @param {string} name */
function attribute(element, name) {
	return element.attrs.find((attr) => attr.name === name && !attr.namespace)?.value;
}

/** @param {Element} element @param {string} tagName */
function isHTML(element, tagName) {
	return element.tagName === tagName && element.namespaceURI === spec.NS.HTML;
}

/**
 * Elements in tree order, the order the parser inserts them; template contents are inert.
 * @param {ParentNode} parent
 * @returns {Generator<Element>}
 */
function* elements(parent) {
	for (const child of parent.childNodes) {
		if (!('tagName' in child)) continue;
		yield child;
		yield* elements(child);
	}
}

/** A `<meta>` that would install an enforced policy if it were a child of `<head>`. */
function policyText(/** @type {Element} */ element) {
	if (!isHTML(element, 'meta')) return undefined;
	if (attribute(element, 'http-equiv')?.toLowerCase() !== 'content-security-policy')
		return undefined;
	return attribute(element, 'content') || undefined;
}

/** Parses a serialized policy; a repeated directive is ignored, as browsers ignore it. */
function parsePolicy(/** @type {string} */ text) {
	/** @type {Policy} */
	const policy = new Map();
	for (const directive of text.split(';')) {
		const [name, ...sources] = directive.trim().split(/[\t\n\f\r ]+/);
		const key = name.toLowerCase();
		if (key && !policy.has(key)) policy.set(key, sources);
	}
	return policy;
}

/**
 * The directive a browser applies for `directive`, after its fallbacks.
 * @param {Policy} policy
 * @param {keyof typeof fallbacks} directive
 */
function effective(policy, directive) {
	for (const name of fallbacks[directive]) {
		const sources = policy.get(name);
		if (sources) return { name, sources };
	}
	return undefined;
}

/** Whether a source list carries a hash of `text`; base64url digits count, as in CSP Level 3. */
function allowsHash(/** @type {string[]} */ sources, /** @type {string} */ text) {
	return sources.some((source) => {
		const match = /^'(sha256|sha384|sha512)-([\w+/-]+={0,2})'$/i.exec(source);
		if (!match) return false;
		const expected = match[2].replaceAll('-', '+').replaceAll('_', '/');
		return createHash(match[1].toLowerCase()).update(text).digest('base64') === expected;
	});
}

/**
 * How a browser treats a script element: `undefined` when it never runs (a data block,
 * an empty inline script, a `nomodule` classic script), otherwise whether it is inline
 * and its text, following the HTML standard's "prepare the script element".
 */
function script(/** @type {Element} */ element) {
	if (element.tagName !== 'script') return undefined;
	const svg = element.namespaceURI === spec.NS.SVG;
	if (!svg && element.namespaceURI !== spec.NS.HTML) return undefined;
	const url = svg
		? element.attrs.find((attr) => attr.name === 'href')?.value
		: attribute(element, 'src');
	const external = url !== undefined;
	const text = element.childNodes.map((node) => ('value' in node ? node.value : '')).join('');
	if (!external && text === '') return undefined;
	const type = attribute(element, 'type');
	const language = attribute(element, 'language');
	const kind = (
		type === '' || (type === undefined && !language)
			? 'text/javascript'
			: (type?.replace(/^[\t\n\f\r ]+|[\t\n\f\r ]+$/g, '') ?? `text/${language}`)
	).toLowerCase();
	if (javascriptTypes.has(kind)) {
		if (attribute(element, 'nomodule') !== undefined) return undefined;
	} else if (!otherScriptTypes.has(kind)) return undefined;
	return { external, url, text };
}

/** The base64 SHA-256 hash source a policy needs to allow `text`. */
function hashSource(/** @type {string} */ text) {
	return `'sha256-${createHash('sha256').update(text).digest('base64')}'`;
}

/**
 * Lists why a built page would run without the enforced policy, with a script keyword
 * it forbids, or with an inline script the policy blocks. The page is parsed as a
 * browser parses it: only a policy `<meta>` that is a child of `<head>` counts, it
 * covers only the scripts after it, and every such policy must allow each script.
 */
export function problems(/** @type {string} */ html) {
	/** @type {string[]} */
	const found = [];
	/** @type {Policy[]} */
	const policies = [];
	for (const element of elements(parse(html))) {
		const text = policyText(element);
		if (text !== undefined) {
			const parent = element.parentNode;
			if (parent && 'tagName' in parent && isHTML(parent, 'head')) policies.push(parsePolicy(text));
			else found.push('content-security-policy <meta> outside <head> is ignored by browsers');
			continue;
		}
		const run = script(element);
		if (!run) continue;
		if (policies.length === 0) {
			found.push(
				run.external
					? `script ${run.url} runs before any enforced content-security-policy`
					: `inline script ${hashSource(run.text)} runs before any enforced content-security-policy`
			);
			continue;
		}
		if (run.external) continue;
		policies.forEach((policy, index) => {
			const directive = effective(policy, 'script-src-elem');
			if (directive && !allowsHash(directive.sources, run.text))
				found.push(
					`inline script ${hashSource(run.text)} is not in ${directive.name} of policy ${index + 1}`
				);
		});
	}
	if (policies.length === 0) found.push('no enforced content-security-policy <meta> in <head>');
	else if (!policies.some((policy) => effective(policy, 'script-src')))
		found.push('policy has no script-src');
	policies.forEach((policy, index) => {
		const names = new Set(
			Object.keys(fallbacks).map(
				(directive) =>
					effective(policy, /** @type {keyof typeof fallbacks} */ (directive))?.name ?? ''
			)
		);
		for (const name of names) {
			const sources = policy.get(name)?.map((source) => source.toLowerCase()) ?? [];
			for (const keyword of forbidden)
				if (sources.includes(keyword))
					found.push(`${name} of policy ${index + 1} allows ${keyword}`);
		}
	});
	return found;
}
