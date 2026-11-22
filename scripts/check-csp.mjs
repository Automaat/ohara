import { readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { problems } from './csp-problems.mjs';

const root = process.argv[2] ?? 'build';

/** @param {string} dir @returns {string[]} */
function pages(dir) {
	return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
		const path = join(dir, entry.name);
		if (entry.isDirectory()) return pages(path);
		return entry.name.endsWith('.html') ? [path] : [];
	});
}

const built = pages(root);
const failures = built.flatMap((page) =>
	problems(readFileSync(page, 'utf8')).map((problem) => `${page}: ${problem}`)
);
if (built.length === 0) failures.push(`${root}: no built pages`);
for (const failure of failures) console.error(failure);
if (failures.length > 0) process.exit(1);
console.log(`Content-Security-Policy enforced on ${built.length} built page(s)`);
