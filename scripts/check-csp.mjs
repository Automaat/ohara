import { createHash } from 'node:crypto';
import { readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';

const root = process.argv[2] ?? 'build';
const forbidden = ["'unsafe-inline'", "'unsafe-eval'"];

/** @param {string} dir @returns {string[]} */
function pages(dir) {
	return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
		const path = join(dir, entry.name);
		if (entry.isDirectory()) return pages(path);
		return entry.name.endsWith('.html') ? [path] : [];
	});
}

/** Lists why a built page would run without the enforced policy or with an unlisted inline script. */
function problems(/** @type {string} */ html) {
	const meta = html.match(/<meta http-equiv="content-security-policy" content="([^"]*)">/);
	if (!meta) return ['no enforced content-security-policy <meta> tag'];
	const policy = meta[1]
		.replaceAll('&#39;', "'")
		.replaceAll('&quot;', '"')
		.replaceAll('&amp;', '&');
	const scriptSrc = policy
		.split(';')
		.map((directive) => directive.trim().split(/\s+/))
		.find(([name]) => name === 'script-src');
	if (!scriptSrc) return ['policy has no script-src'];
	const found = forbidden
		.filter((source) => scriptSrc.includes(source))
		.map((source) => `script-src allows ${source}`);
	for (const [, body] of html.matchAll(/<script(?![^>]*\ssrc=)[^>]*>([\s\S]*?)<\/script>/g)) {
		const hash = `'sha256-${createHash('sha256').update(body).digest('base64')}'`;
		if (!scriptSrc.includes(hash)) found.push(`inline script ${hash} is not in script-src`);
	}
	return found;
}

const built = pages(root);
const failures = built.flatMap((page) =>
	problems(readFileSync(page, 'utf8')).map((problem) => `${page}: ${problem}`)
);
if (built.length === 0) failures.push(`${root}: no built pages`);
for (const failure of failures) console.error(failure);
if (failures.length > 0) process.exit(1);
console.log(`Content-Security-Policy enforced on ${built.length} built page(s)`);
